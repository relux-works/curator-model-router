package cmrio

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/relux-works/curator-model-router/pkg/recommend"
	"golang.org/x/sys/unix"
)

type MigrationEntry struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Status      string `json:"status"`
}
type MigrationReport struct {
	DryRun  bool             `json:"dry_run"`
	Entries []MigrationEntry `json:"entries"`
}
type migrationFile struct {
	MigrationEntry
	mode                        fs.FileMode
	directory                   bool
	info                        fs.FileInfo
	sourceRoot, destinationRoot *migrationRoot
}

// Migrate preflights all conflicts before writing. Each file is published whole
// with an atomic no-replace link from a temporary file on the same filesystem.
// A later IO failure can leave completed files, which a retry treats as unchanged.
func Migrate(dryRun bool) (MigrationReport, error) {
	return migrate(dryRun, nil)
}

// afterPreflight allows tests to replace queued filesystem objects before copy.
func migrate(dryRun bool, afterPreflight func()) (MigrationReport, error) {
	report := MigrationReport{DryRun: dryRun, Entries: []MigrationEntry{}}
	files := []migrationFile{}
	pairs := [][3]string{
		{LegacyConfigPath("policy"), filepath.Join(ConfigRoot(), "routing.toml"), xdgRoot("XDG_CONFIG_HOME", ".config")},
		{LegacyConfigPath("catalog"), filepath.Join(ConfigRoot(), "catalog.json"), xdgRoot("XDG_CONFIG_HOME", ".config")},
		{LegacyStateRoot(), StateRoot(), xdgRoot("XDG_STATE_HOME", ".local/state")}, {LegacyDataRoot(), DataRoot(), xdgRoot("XDG_DATA_HOME", ".local/share")},
	}
	var failure error
	for index, pair := range pairs {
		rootInfo, err := os.Lstat(pair[0])
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || (index < 2 && !rootInfo.Mode().IsRegular()) || (index >= 2 && !rootInfo.IsDir()) {
			report.Entries = append(report.Entries, MigrationEntry{Source: pair[0], Destination: pair[1], Status: "error"})
			failure = &recommend.Refusal{Code: "cmr_migrate_io", Message: "cannot read legacy migration source of the expected type"}
			continue
		}
		sourceBase := pair[2]
		if index == 0 {
			sourceBase = os.Getenv("HOME")
		}
		sourceRoot, err := openMigrationRoot(sourceBase, false)
		if err != nil {
			failure = err
			continue
		}
		defer sourceRoot.file.Close()
		destinationRoot, err := openMigrationRoot(pair[2], true)
		if err != nil {
			failure = err
			continue
		}
		defer destinationRoot.file.Close()
		err = filepath.WalkDir(pair[0], func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(pair[0], path)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			file := migrationFile{MigrationEntry: MigrationEntry{Source: path, Destination: filepath.Join(pair[1], rel), Status: "copy"}, mode: info.Mode(), directory: entry.IsDir(), info: info, sourceRoot: sourceRoot, destinationRoot: destinationRoot}
			if !info.IsDir() && !info.Mode().IsRegular() {
				return &recommend.Refusal{Code: "cmr_migrate_io", Message: "migration requires regular files and directories"}
			}
			// Refuse symlinked destination ancestors as well as symlinked leaves.
			for parent := filepath.Dir(file.Destination); ; parent = filepath.Dir(parent) {
				if parent == filepath.Clean(pair[2]) {
					break
				}
				parentInfo, parentErr := os.Lstat(parent)
				if parentErr == nil && !parentInfo.IsDir() {
					file.Status = "conflict"
					files = append(files, file)
					return &recommend.Refusal{Code: "cmr_migrate_conflict", Message: "migration destination parent is not a directory"}
				}
				if parentErr != nil && !os.IsNotExist(parentErr) {
					return parentErr
				}
				if parent == filepath.Dir(parent) {
					break
				}
			}
			dest, err := os.Lstat(file.Destination)
			switch {
			case os.IsNotExist(err):
			case err != nil:
				return err
			case file.directory && dest.IsDir():
				file.Status = "unchanged"
			case !file.directory && dest.Mode().IsRegular() && dest.Mode() == info.Mode():
				old, err := sourceRoot.readFile(path, info)
				if err != nil {
					return err
				}
				current, err := destinationRoot.readFile(file.Destination, dest)
				if err != nil {
					return err
				}
				if bytes.Equal(old, current) {
					file.Status = "unchanged"
				} else {
					file.Status = "conflict"
				}
			default:
				file.Status = "conflict"
			}
			if file.Status == "conflict" {
				failure = &recommend.Refusal{Code: "cmr_migrate_conflict", Message: "migration destination conflicts; existing files were preserved"}
			}
			files = append(files, file)
			return nil
		})
		if err != nil {
			failure = err
		}
	}
	for _, file := range files {
		report.Entries = append(report.Entries, file.MigrationEntry)
	}
	if failure != nil {
		return report, migrationError(failure)
	}
	if dryRun {
		return report, nil
	}
	if afterPreflight != nil {
		afterPreflight()
	}
	for i, file := range files {
		if file.Status != "copy" {
			continue
		}
		var err error
		if file.directory {
			var dir *os.File
			dir, err = file.sourceRoot.directory(file.Source, false)
			if err == nil {
				info, statErr := dir.Stat()
				dir.Close()
				if statErr != nil || !os.SameFile(file.info, info) {
					err = &recommend.Refusal{Code: "cmr_migrate_io", Message: "migration source directory changed"}
				}
			}
			if err == nil {
				dir, err = file.destinationRoot.directory(file.Destination, true)
				if err == nil {
					err = dir.Close()
				}
			}
		} else {
			err = copyMigrationFile(file)
		}
		if err != nil {
			report.Entries[i].Status = "error"
			return report, migrationError(err)
		}
		report.Entries[i].Status = "copied"
	}
	return report, nil
}
func migrationError(err error) error {
	if refusal, ok := err.(*recommend.Refusal); ok {
		return refusal
	}
	return &recommend.Refusal{Code: "cmr_migrate_io", Message: "migration IO failed; legacy files and existing destinations were preserved"}
}

// migrationRoot anchors traversal before the copy phase. The existing parent of
// an XDG/HOME base is trusted; every component below it is opened without following
// links. Holding the descriptor keeps later ancestor replacements from redirecting IO.
type migrationRoot struct {
	file        *os.File
	path        string
	destination bool
}

func openMigrationRoot(base string, destination bool) (*migrationRoot, error) {
	base, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	for path := filepath.Dir(base); ; path = filepath.Dir(path) {
		fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err == nil {
			return &migrationRoot{file: os.NewFile(uintptr(fd), path), path: path, destination: destination}, nil
		}
		if !errors.Is(err, unix.ENOENT) || path == filepath.Dir(path) {
			return nil, err
		}
	}
}

func (root *migrationRoot) directory(path string, create bool) (*os.File, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root.path, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, &recommend.Refusal{Code: "cmr_migrate_io", Message: "migration path escapes its root"}
	}
	fd, err := unix.Openat(int(root.file.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), path)
	if rel == "." {
		return dir, nil
	}
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		if create {
			err = unix.Mkdirat(int(dir.Fd()), component, 0700)
			if err != nil && !errors.Is(err, unix.EEXIST) {
				dir.Close()
				return nil, err
			}
		}
		fd, err = unix.Openat(int(dir.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		dir.Close()
		if err != nil {
			return nil, err
		}
		dir = os.NewFile(uintptr(fd), path)
		var stat unix.Stat_t
		if err = unix.Fstat(fd, &stat); err != nil {
			dir.Close()
			return nil, err
		}
		if root.destination && stat.Uid != uint32(os.Geteuid()) {
			dir.Close()
			return nil, &recommend.Refusal{Code: "cmr_migrate_io", Message: "migration destination directory must be owned by the current user"}
		}
	}
	return dir, nil
}

func (root *migrationRoot) openFile(path string, expected fs.FileInfo) (*os.File, error) {
	dir, err := root.directory(filepath.Dir(path), false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	in := os.NewFile(uintptr(fd), path)
	info, err := in.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(expected, info) || expected.Mode() != info.Mode() {
		in.Close()
		return nil, &recommend.Refusal{Code: "cmr_migrate_io", Message: "migration file changed or is not a regular file"}
	}
	return in, nil
}

func (root *migrationRoot) readFile(path string, expected fs.FileInfo) ([]byte, error) {
	in, err := root.openFile(path, expected)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	return io.ReadAll(in)
}

func copyMigrationFile(file migrationFile) error {
	in, err := file.sourceRoot.openFile(file.Source, file.info)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeMigrationFileAt(in, file.destinationRoot, file.Destination, file.mode)
}

func writeMigrationFile(in io.Reader, destination string, mode fs.FileMode) error {
	root, err := openMigrationRoot(filepath.Dir(destination), true)
	if err != nil {
		return err
	}
	defer root.file.Close()
	return writeMigrationFileAt(in, root, destination, mode)
}

func writeMigrationFileAt(in io.Reader, root *migrationRoot, destination string, mode fs.FileMode) error {
	dir, err := root.directory(filepath.Dir(destination), true)
	if err != nil {
		return err
	}
	defer dir.Close()
	name := ".cmr-migrate-" + rand.Text()
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	out := os.NewFile(uintptr(fd), name)
	defer unix.Unlinkat(int(dir.Fd()), name, 0)
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Chmod(mode); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// Refuse a directory replacement detected during the read as well. Cleanup
	// still uses the original descriptor, so a renamed parent cannot leak staging.
	current, err := root.directory(filepath.Dir(destination), false)
	if err != nil {
		return err
	}
	defer current.Close()
	before, err := dir.Stat()
	if err != nil {
		return err
	}
	after, err := current.Stat()
	if err != nil || !os.SameFile(before, after) {
		return &recommend.Refusal{Code: "cmr_migrate_io", Message: "migration destination directory changed during copy"}
	}
	// Both names are relative to the verified directory descriptor, including
	// cleanup. Linkat atomically refuses a destination that appeared during copy.
	if err := unix.Linkat(int(dir.Fd()), name, int(dir.Fd()), filepath.Base(destination), 0); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return &recommend.Refusal{Code: "cmr_migrate_conflict", Message: "migration destination appeared during copy"}
		}
		return err
	}
	return nil
}
