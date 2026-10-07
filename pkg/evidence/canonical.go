package evidence

import (
	"encoding/json"
	"strings"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

// RecordAddress hashes exactly the canonical record without its root id.
// Validate the complete record before deleting id so invalid content is refused.
func RecordAddress(prefix string, record any) (string, error) {
	raw, err := canonical.MarshalRecord(record)
	if err != nil {
		return "", err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return "", err
	}
	delete(fields, "id")
	digest, err := canonical.RecordDigest(fields)
	if err != nil {
		return "", err
	}
	return prefix + strings.TrimPrefix(digest, "sha256:"), nil
}
func validDigest(s string) bool {
	if !strings.HasPrefix(s, "sha256:") || len(s) != 71 {
		return false
	}
	for _, c := range s[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func validAddress(s string) bool {
	for _, p := range []string{"obs:", "note:", "ret:"} {
		if strings.HasPrefix(s, p) {
			return validDigest("sha256:" + strings.TrimPrefix(s, p))
		}
	}
	return false
}
func (v Import) Digest() (string, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	return canonical.Digest(v)
}
func (v Snapshot) Digest() (string, error) {
	if v.SchemaVersion != SchemaVersion || v.Kind != "evidence-snapshot" || v.TaxonomyVersion != "v1" || v.Imports == nil || !validDigest(v.RegistryDigest) || v.RegistryRef.Name == "" || v.RegistryRef.Version == "" {
		return "", refuse("evidence_invalid_snapshot", "invalid snapshot header")
	}
	if err := canonical.CheckOrdered(v.Imports, func(s string) string { return s }); err != nil {
		return "", err
	}
	for _, s := range v.Imports {
		if !validDigest(s) {
			return "", refuse("evidence_invalid_digest", "invalid import digest")
		}
	}
	return canonical.Digest(v)
}
