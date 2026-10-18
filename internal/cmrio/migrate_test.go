package cmrio

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func migrationFixture(t *testing.T) map[string]string {
	t.Helper()
	configEnvironment(t)
	pairs := map[string]string{
		LegacyConfigPath("policy"):                                       filepath.Join(ConfigRoot(), "routing.toml"),
		LegacyConfigPath("catalog"):                                      filepath.Join(ConfigRoot(), "catalog.json"),
		filepath.Join(LegacyStateRoot(), "shadow", "observations.jsonl"): filepath.Join(StateRoot(), "shadow", "observations.jsonl"),
		filepath.Join(LegacyDataRoot(), "evidence", "record.json"):       filepath.Join(DataRoot(), "evidence", "record.json"),
	}
	for src := range pairs {
		putConfig(t, src, []byte("original"))
		if err := os.Chmod(src, 0640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(LegacyDataRoot(), "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	return pairs
}
func requireMigrationRefusal(t *testing.T, err error, code string) {
	t.Helper()
	var refusal *recommend.Refusal
	if !errors.As(err, &refusal) || refusal.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

// All legacy trees copy without deleting inputs, file modes survive, newly
// created directories are private, and a repeated migration changes nothing.
func TestMigrateCopiesAndIsIdempotent(t *testing.T) {
	pairs := migrationFixture(t)
	report, err := Migrate(false)
	if err != nil || len(report.Entries) == 0 {
		t.Fatal(report, err)
	}
	for src, dst := range pairs {
		for _, path := range []string{src, dst} {
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "original" {
				t.Fatal(path, string(data), err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0640 {
				t.Fatal(info, err)
			}
		}
	}
	for _, root := range []string{ConfigRoot(), StateRoot(), DataRoot()} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if info.Mode().Perm() != 0700 {
					t.Errorf("directory mode %s: %v", path, info.Mode())
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	report, err = Migrate(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range report.Entries {
		if entry.Status != "unchanged" {
			t.Fatal("second run copied", entry)
		}
	}
}

// A conflicting destination remains byte-for-byte intact and preflight prevents
// other planned copies. Matching content with a different mode is a conflict too.
func TestMigrateConflictNeverOverwrites(t *testing.T) {
	for _, sameContent := range []bool{false, true} {
		t.Run(map[bool]string{false: "content", true: "mode"}[sameContent], func(t *testing.T) {
			pairs := migrationFixture(t)
			dst := filepath.Join(ConfigRoot(), "routing.toml")
			data := "destination"
			if sameContent {
				data = "original"
			}
			putConfig(t, dst, []byte(data))
			report, err := Migrate(false)
			requireMigrationRefusal(t, err, "cmr_migrate_conflict")
			found := false
			for _, entry := range report.Entries {
				found = found || entry.Status == "conflict"
			}
			if !found {
				t.Fatal("conflict not reported", report)
			}
			got, err := os.ReadFile(dst)
			if err != nil || string(got) != data {
				t.Fatal("overwritten", string(got), err)
			}
			info, _ := os.Stat(dst)
			if info.Mode().Perm() != 0600 {
				t.Fatal("destination mode changed")
			}
			for _, other := range pairs {
				if other != dst {
					if _, err := os.Lstat(other); !os.IsNotExist(err) {
						t.Fatal("preflight wrote another file", other, err)
					}
				}
			}
		})
	}
}

// Dry-run reports copies/conflicts without creating a destination directory;
// a completely absent legacy tree is a successful no-op.
func TestMigrateDryRunAndAbsent(t *testing.T) {
	pairs := migrationFixture(t)
	report, err := Migrate(true)
	if err != nil || !report.DryRun {
		t.Fatal(report, err)
	}
	for _, dst := range pairs {
		if _, err := os.Lstat(dst); !os.IsNotExist(err) {
			t.Fatal(dst, err)
		}
	}
	for _, root := range []string{ConfigRoot(), StateRoot(), DataRoot()} {
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("dry-run created root", root)
		}
	}
	configEnvironment(t)
	report, err = Migrate(false)
	if err != nil || len(report.Entries) != 0 {
		t.Fatal(report, err)
	}
}

type failingMigrationReader struct{}

func (failingMigrationReader) Read([]byte) (int, error) {
	return 0, errors.New("injected read failure")
}

// A read error after bytes were copied removes the staging file and never
// publishes a partial destination; a destination appearing during copy also wins.
func TestMigrationAtomicPublication(t *testing.T) {
	root := configEnvironment(t)
	dst := filepath.Join(root, "destination")
	reader := io.MultiReader(strings.NewReader("prefix"), failingMigrationReader{})
	if err := writeMigrationFile(reader, dst, 0640); err == nil {
		t.Fatal("read failure ignored")
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatal("partial destination", err)
	}
	temps, err := filepath.Glob(filepath.Join(root, ".cmr-migrate-*"))
	if err != nil || len(temps) != 0 {
		t.Fatal("staging file leaked", temps, err)
	}
	putConfig(t, dst, []byte("winner"))
	requireMigrationRefusal(t, writeMigrationFile(strings.NewReader("loser"), dst, 0640), "cmr_migrate_conflict")
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "winner" {
		t.Fatal("concurrent destination overwritten", string(got), err)
	}
}

// Unsupported input links and blocked destination parents refuse with no
// partial destination file, including during dry-run.
func TestMigrateIOAndBlockedParents(t *testing.T) {
	for _, dry := range []bool{false, true} {
		configEnvironment(t)
		src := LegacyConfigPath("policy")
		if err := os.MkdirAll(filepath.Dir(src), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("missing", src); err != nil {
			t.Fatal(err)
		}
		_, err := Migrate(dry)
		requireMigrationRefusal(t, err, "cmr_migrate_io")
		if _, err := os.Stat(ConfigRoot()); !os.IsNotExist(err) {
			t.Fatal("IO failure wrote root", err)
		}
	}
	migrationFixture(t)
	putConfig(t, ConfigRoot(), []byte("blocked"))
	_, err := Migrate(false)
	requireMigrationRefusal(t, err, "cmr_migrate_conflict")
}

// Replacing a queued source after preflight refuses without publishing a file.
// The FIFO runs in a bounded subprocess so a blocking open cannot hang the suite.
func TestMigrateRefusesQueuedSourceReplacement(t *testing.T) {
	for _, replacement := range []string{"symlink", "fifo", "regular"} {
		t.Run(replacement, func(t *testing.T) {
			configEnvironment(t)
			if replacement == "fifo" && os.Getenv("CMR_TEST_FIFO_HELPER") != "1" {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMigrateRefusesQueuedSourceReplacement$/fifo$", "-test.timeout=4s")
				cmd.Env = append(os.Environ(), "CMR_TEST_FIFO_HELPER=1")
				output, err := cmd.CombinedOutput()
				if ctx.Err() != nil {
					t.Fatalf("migration hung on a queued FIFO: %v\n%s", ctx.Err(), output)
				}
				if err != nil {
					t.Fatalf("FIFO subprocess: %v\n%s", err, output)
				}
				return
			}
			src := LegacyConfigPath("policy")
			dst := filepath.Join(ConfigRoot(), "routing.toml")
			putConfig(t, src, []byte("original"))
			outside := t.TempDir()
			target := filepath.Join(outside, "target")
			putConfig(t, target, []byte("outside"))
			hooked := false
			report, err := migrate(false, func() {
				hooked = true
				// Rename preserves the original inode so regular-file replacement
				// cannot accidentally reuse it and evade the identity assertion.
				if err := os.Rename(src, src+".saved"); err != nil {
					t.Fatal(err)
				}
				var err error
				switch replacement {
				case "symlink":
					err = os.Symlink(target, src)
				case "fifo":
					err = unix.Mkfifo(src, 0600)
				case "regular":
					err = os.WriteFile(src, []byte("replacement"), 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
			})
			requireMigrationRefusal(t, err, "cmr_migrate_io")
			if !hooked || len(report.Entries) != 1 || report.Entries[0].Status != "error" {
				t.Fatal("copy boundary was not exercised", report)
			}
			if _, err := os.Lstat(dst); !os.IsNotExist(err) {
				t.Fatal("replacement source published", err)
			}
			if _, err := os.Lstat(ConfigRoot()); !os.IsNotExist(err) {
				t.Fatal("source refusal created a destination directory", err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 1 || entries[0].Name() != "target" {
				t.Fatal("outside tree changed", entries, err)
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != "outside" {
				t.Fatal("outside file changed", string(got), err)
			}
		})
	}
}

// Swapping a destination directory or its ancestor for a symlink after
// preflight refuses; neither the symlink target nor the old directory is written.
func TestMigrateRefusesQueuedDestinationSymlink(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "ancestor"}[ancestor], func(t *testing.T) {
			configEnvironment(t)
			putConfig(t, LegacyConfigPath("policy"), []byte("original"))
			if err := os.MkdirAll(ConfigRoot(), 0700); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			dir := ConfigRoot()
			if ancestor {
				dir = filepath.Dir(dir)
			}
			hooked := false
			report, err := migrate(false, func() {
				hooked = true
				if err := os.Rename(dir, dir+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, dir); err != nil {
					t.Fatal(err)
				}
			})
			requireMigrationRefusal(t, err, "cmr_migrate_io")
			if !hooked || len(report.Entries) != 1 || report.Entries[0].Status != "error" {
				t.Fatal("copy boundary was not exercised", report)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatal("migration wrote outside its root", entries, err)
			}
			err = filepath.WalkDir(dir+".saved", func(path string, entry fs.DirEntry, err error) error {
				if err == nil && !entry.IsDir() {
					t.Errorf("migration published a file in the replaced directory: %s", path)
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

type migrationReadHook struct {
	io.Reader
	hook func()
}

func (r *migrationReadHook) Read(p []byte) (int, error) {
	if r.hook != nil {
		r.hook()
		r.hook = nil
	}
	return r.Reader.Read(p)
}

// Replacements during IO refuse before publication, and descriptor-relative
// cleanup removes staging from the renamed directory without touching the target.
func TestMigrationRefusesDestinationReplacementDuringRead(t *testing.T) {
	for _, symlink := range []bool{true, false} {
		t.Run(map[bool]string{true: "symlink", false: "directory"}[symlink], func(t *testing.T) {
			configEnvironment(t)
			dir := ConfigRoot()
			outside := t.TempDir()
			reader := &migrationReadHook{Reader: strings.NewReader("complete"), hook: func() {
				if err := os.Rename(dir, dir+".saved"); err != nil {
					t.Fatal(err)
				}
				var err error
				if symlink {
					err = os.Symlink(outside, dir)
				} else {
					err = os.Mkdir(dir, 0700)
				}
				if err != nil {
					t.Fatal(err)
				}
			}}
			err := writeMigrationFile(reader, filepath.Join(dir, "routing.toml"), 0600)
			requireMigrationRefusal(t, migrationError(err), "cmr_migrate_io")
			for _, path := range []string{dir, dir + ".saved", outside} {
				entries, err := os.ReadDir(path)
				if err != nil || len(entries) != 0 {
					t.Fatal("staging or published file leaked", path, entries, err)
				}
			}
		})
	}
}

// An empty source directory replaced after preflight must refuse too, without
// creating its destination or following the replacement link.
func TestMigrateRefusesQueuedSourceDirectorySymlink(t *testing.T) {
	configEnvironment(t)
	src := LegacyStateRoot()
	if err := os.MkdirAll(src, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	_, err := migrate(false, func() {
		if err := os.Rename(src, src+".saved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, src); err != nil {
			t.Fatal(err)
		}
	})
	requireMigrationRefusal(t, err, "cmr_migrate_io")
	if _, err := os.Lstat(StateRoot()); !os.IsNotExist(err) {
		t.Fatal("source directory replacement created a destination", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("outside tree changed", entries, err)
	}
}
