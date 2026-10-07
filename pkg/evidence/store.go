package evidence

import (
	"bytes"
	"errors"
	"github.com/relux-works/curator-model-router/pkg/canonical"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// TODO(decision): retain data-only registry projections in registries/<digest>.json
// so a snapshot can replay exact efforts after the CLI's default module changes.
type Store struct {
	root     string
	registry Registry
}

func NewStore(root string, r Registry) (*Store, error) {
	if root == "" {
		return nil, refuse("evidence_store_root", "store root must be explicit")
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	b, err := canonical.Marshal(r)
	if err != nil {
		return nil, err
	}
	var frozen Registry
	if err = Decode(b, &frozen); err != nil {
		return nil, err
	}
	return &Store{root, frozen}, nil
}
func storeError(err error) error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return err
	}
	var c *canonical.Error
	if errors.As(err, &c) {
		return err
	}
	return refuse("evidence_store_io", "store I/O failed")
}
func (s *Store) lock() (func(), error) {
	if err := os.MkdirAll(s.root, 0700); err != nil {
		return nil, storeError(err)
	}
	lock := filepath.Join(s.root, "current.lock")
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil, refuse("evidence_locked", "writer lock exists; never automatically stolen")
	}
	if err != nil {
		return nil, storeError(err)
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(lock)
		return nil, storeError(err)
	}
	return func() { _ = os.Remove(lock) }, nil
}
func (s *Store) file(dir, id string) (string, error) {
	if !validDigest(id) {
		return "", refuse("evidence_invalid_digest", "invalid content id")
	}
	return filepath.Join(s.root, dir, id+".json"), nil
}

// immutable publishes fully synced bytes using an atomic hard link. Readers
// cannot see a partly written file; existing content must match byte for byte.
func (s *Store) immutable(dir, id string, b []byte) error {
	path, err := s.file(dir, id)
	if err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if err = os.MkdirAll(parent, 0700); err != nil {
		return storeError(err)
	}
	f, err := os.CreateTemp(parent, ".pending-")
	if err != nil {
		return storeError(err)
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return storeError(err)
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return storeError(err)
	}
	if err = f.Close(); err != nil {
		return storeError(err)
	}
	if err = os.Link(name, path); errors.Is(err, os.ErrExist) {
		old, readErr := os.ReadFile(path)
		if readErr != nil {
			return storeError(readErr)
		}
		if !bytes.Equal(old, b) {
			return refuse("evidence_store_corrupt", "immutable content differs")
		}
		return nil
	}
	return storeError(err)
}
func (s *Store) read(dir, id string, out any) error {
	path, err := s.file(dir, id)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return storeError(err)
	}
	if err = Decode(b, out); err != nil {
		return err
	}
	actual, err := canonical.Digest(out)
	if err != nil {
		return err
	}
	if actual != id {
		return refuse("evidence_store_corrupt", "stored digest mismatch")
	}
	return nil
}
func (s *Store) Current() (Snapshot, string, error) {
	b, err := os.ReadFile(filepath.Join(s.root, "current"))
	if errors.Is(err, os.ErrNotExist) {
		rd, e := canonical.Digest(s.registry)
		if e != nil {
			return Snapshot{}, "", e
		}
		v := Snapshot{SchemaVersion, "evidence-snapshot", []string{}, "v1", s.registry.Reference, rd}
		id, e := v.Digest()
		return v, id, e
	}
	if err != nil {
		return Snapshot{}, "", storeError(err)
	}
	id := strings.TrimSpace(string(b))
	var v Snapshot
	if err = s.read("snapshots", id, &v); err != nil {
		return v, "", err
	}
	if _, err = v.Digest(); err != nil {
		return v, "", err
	}
	return v, id, nil
}
func (s *Store) Load(v Snapshot) ([]Import, error) {
	out := []Import{}
	for _, id := range v.Imports {
		var doc Import
		if err := s.read("imports", id, &doc); err != nil {
			return nil, err
		}
		if err := doc.Validate(); err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, nil
}
func checkBenchmarks(docs []Import) error {
	benches := map[string]Benchmark{}
	for _, doc := range docs {
		for _, b := range doc.Benchmarks {
			if old, ok := benches[b.Address()]; ok {
				a, _ := canonical.Marshal(struct {
					SchemaVersion string    `json:"schema_version"`
					Benchmark     Benchmark `json:"benchmark"`
				}{SchemaVersion, old})
				bb, _ := canonical.Marshal(struct {
					SchemaVersion string    `json:"schema_version"`
					Benchmark     Benchmark `json:"benchmark"`
				}{SchemaVersion, b})
				if !bytes.Equal(a, bb) {
					return refuse("evidence_benchmark_conflict", "benchmark version has conflicting definitions")
				}
			}
			benches[b.Address()] = b
		}
	}
	for _, doc := range docs {
		for _, o := range doc.Observations {
			b, ok := benches[o.BenchmarkRef.Address()]
			if !ok {
				return refuse("evidence_benchmark_missing", "benchmark reference is not in snapshot")
			}
			if err := checkObservationBenchmark(o, b); err != nil {
				return err
			}
		}
	}
	return nil
}

type ImportResult struct {
	ImportDigest   string `json:"import_digest"`
	SnapshotDigest string `json:"snapshot_digest"`
	Report         Report `json:"report"`
	ImportedAt     string `json:"imported_at"`
	AlreadyPresent bool   `json:"already_present"`
	Status         string `json:"status"`
}

func (s *Store) Add(input Import) (ImportResult, error) {
	result := ImportResult{}
	unlock, err := s.lock()
	if err != nil {
		return result, err
	}
	defer unlock()
	snap, _, err := s.Current()
	if err != nil {
		return result, err
	}
	rd, err := canonical.Digest(s.registry)
	if err != nil {
		return result, err
	}
	if snap.RegistryDigest != rd || snap.RegistryRef != s.registry.Reference {
		return result, refuse("evidence_registry_mismatch", "store snapshot binds a different frozen registry; use cmr evidence rebind --registry FILE")
	}
	imports, err := s.Load(snap)
	if err != nil {
		return result, err
	}
	// Refuse a cross-import cycle before supplied address validation. Addressed
	// input permits this defensive check without inventing addresses for new rows.
	addressed := true
	for _, o := range input.Observations {
		if o.ID == "" {
			addressed = false
		}
	}
	for _, n := range input.Notes {
		if n.ID == "" {
			addressed = false
		}
	}
	for _, t := range input.Retractions {
		if t.ID == "" {
			addressed = false
		}
	}
	if addressed {
		if _, err = Resolve(append(append([]Import{}, imports...), input)); err != nil {
			return result, err
		}
	}
	doc, report, err := Prepare(input, s.registry)
	if err != nil {
		return result, err
	}
	id, err := doc.Digest()
	if err != nil {
		return result, err
	}
	present := false
	for _, x := range snap.Imports {
		if x == id {
			present = true
		}
	}
	if !present {
		imports = append(imports, doc)
		snap.Imports = append(snap.Imports, id)
		canonical.SortByKey(snap.Imports, func(s string) string { return s })
	}
	if err = checkBenchmarks(imports); err != nil {
		return result, err
	}
	active, err := Resolve(imports)
	if err != nil {
		return result, err
	}
	report.Issues = append(report.Issues, active.Issues...)
	canonical.SortByKey(report.Issues, func(i Issue) string { return i.Code + "\x00" + i.Ref + "\x00" + i.Detail })
	sid, err := snap.Digest()
	if err != nil {
		return result, err
	}
	rb, err := canonical.Marshal(s.registry)
	if err != nil {
		return result, err
	}
	if err = s.immutable("registries", rd, rb); err != nil {
		return result, err
	}
	b, err := canonical.Marshal(doc)
	if err != nil {
		return result, err
	}
	if err = s.immutable("imports", id, b); err != nil {
		return result, err
	}
	b, err = canonical.Marshal(snap)
	if err != nil {
		return result, err
	}
	if err = s.immutable("snapshots", sid, b); err != nil {
		return result, err
	}
	if err = s.publishCurrent(sid); err != nil {
		return result, err
	}
	status := "added"
	if present {
		status = "already present"
	}
	return ImportResult{ImportDigest: id, SnapshotDigest: sid, Report: report, ImportedAt: doc.ImportedAt, AlreadyPresent: present, Status: status}, nil
}

// publishCurrent syncs the file before rename and the parent directory afterward.
// Directory fsync is best effort: some platforms/filesystems do not support it.
func (s *Store) publishCurrent(sid string) error {
	f, err := os.CreateTemp(s.root, ".current-")
	if err != nil {
		return storeError(err)
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.WriteString(sid + "\n"); err != nil {
		_ = f.Close()
		return storeError(err)
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return storeError(err)
	}
	if err = f.Close(); err != nil {
		return storeError(err)
	}
	if err = os.Rename(name, filepath.Join(s.root, "current")); err != nil {
		return storeError(err)
	}
	parent, err := os.Open(s.root)
	if err != nil {
		return storeError(err)
	}
	defer parent.Close()
	err = parent.Sync()
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EBADF) {
		return nil
	}
	return storeError(err)
}

// RegistryFor returns the immutable registry bound to a snapshot, including old
// snapshots after rebind. Empty stores use their explicitly supplied registry.
func (s *Store) RegistryFor(snap Snapshot) (Registry, error) {
	rd, err := canonical.Digest(s.registry)
	if err != nil {
		return Registry{}, err
	}
	if rd == snap.RegistryDigest && s.registry.Reference == snap.RegistryRef {
		b, err := canonical.Marshal(s.registry)
		if err != nil {
			return Registry{}, err
		}
		var frozen Registry
		err = Decode(b, &frozen)
		return frozen, err
	}
	var r Registry
	if err = s.read("registries", snap.RegistryDigest, &r); err != nil {
		return r, err
	}
	if err = r.Validate(); err != nil {
		return r, err
	}
	if r.Reference != snap.RegistryRef {
		return r, refuse("evidence_registry_mismatch", "stored registry reference differs from snapshot")
	}
	return r, nil
}

// InspectSnapshot computes status and model resolution using this snapshot's
// bound registry, preserving both its imports and earlier snapshot bindings.
func (s *Store) InspectSnapshot(snap Snapshot) ([]Import, ActiveSet, error) {
	if _, err := snap.Digest(); err != nil {
		return nil, ActiveSet{}, err
	}
	docs, err := s.Load(snap)
	if err != nil {
		return nil, ActiveSet{}, err
	}
	r, err := s.RegistryFor(snap)
	if err != nil {
		return nil, ActiveSet{}, err
	}
	a, err := ResolveSnapshot(docs, r)
	return docs, a, err
}

// Rebind publishes a new snapshot over exactly the same imports, using the
// registry supplied to NewStore. Old snapshots and imports remain immutable.
func (s *Store) Rebind() (Snapshot, error) {
	unlock, err := s.lock()
	if err != nil {
		return Snapshot{}, err
	}
	defer unlock()
	snap, _, err := s.Current()
	if err != nil {
		return snap, err
	}
	if _, err = s.Load(snap); err != nil {
		return snap, err
	}
	rd, err := canonical.Digest(s.registry)
	if err != nil {
		return snap, err
	}
	snap.RegistryRef, snap.RegistryDigest = s.registry.Reference, rd
	sid, err := snap.Digest()
	if err != nil {
		return snap, err
	}
	rb, err := canonical.Marshal(s.registry)
	if err != nil {
		return snap, err
	}
	if err = s.immutable("registries", rd, rb); err != nil {
		return snap, err
	}
	b, err := canonical.Marshal(snap)
	if err != nil {
		return snap, err
	}
	if err = s.immutable("snapshots", sid, b); err != nil {
		return snap, err
	}
	return snap, s.publishCurrent(sid)
}
func (s *Store) Inspect() (Snapshot, string, []Import, ActiveSet, error) {
	snap, id, err := s.Current()
	if err != nil {
		return snap, id, nil, ActiveSet{}, err
	}
	docs, a, err := s.InspectSnapshot(snap)
	return snap, id, docs, a, err
}
func (s *Store) Show(id string) (Record, error) {
	if !validAddress(id) {
		return Record{}, refuse("evidence_invalid_target", "show requires a record address")
	}
	_, _, _, a, err := s.Inspect()
	if err != nil {
		return Record{}, err
	}
	for _, r := range a.Records {
		if r.ID == id {
			return r, nil
		}
	}
	return Record{}, refuse("evidence_not_found", "record is not in current snapshot")
}
func (s *Store) Unresolved() (Report, error) {
	snap, _, docs, _, err := s.Inspect()
	if err != nil {
		return Report{}, err
	}
	r, err := s.RegistryFor(snap)
	if err != nil {
		return Report{}, err
	}
	report := Report{Issues: []Issue{}}
	for _, doc := range docs {
		_, rep, err := Prepare(doc, r)
		if err != nil {
			return report, err
		}
		for _, i := range rep.Issues {
			if i.Code == "model_unresolved" {
				report.Issues = append(report.Issues, i)
			}
		}
	}
	canonical.SortByKey(report.Issues, func(i Issue) string { return i.Ref })
	return report, nil
}
func (s *Store) SaveView(v View) error {
	if err := v.Validate(); err != nil {
		return err
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	b, err := canonical.Marshal(v)
	if err != nil {
		return err
	}
	return s.immutable("views", v.ID, b)
}
func (s *Store) ReadView(id string) (View, error) {
	path, err := s.file("views", id)
	if err != nil {
		return View{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return View{}, storeError(err)
	}
	var v View
	if err = Decode(b, &v); err != nil {
		return v, err
	}
	if v.ID != id {
		return v, refuse("evidence_view_mismatch", "view id differs from file key")
	}
	return v, v.Validate()
}
