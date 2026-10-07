package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

// legacyRecordBytes is the former versioned-envelope algorithm, retained only
// as a compatibility oracle. No envelope bytes enter the record address.
func legacyRecordBytes(record any) ([]byte, error) {
	raw, err := canonical.Marshal(struct {
		SchemaVersion string `json:"schema_version"`
		Record        any    `json:"record"`
	}{SchemaVersion, record})
	if err != nil {
		return nil, err
	}
	var original struct {
		Record map[string]json.RawMessage `json:"record"`
	}
	if err = json.Unmarshal(raw, &original); err != nil {
		return nil, err
	}
	delete(original.Record, "id")
	b, err := canonical.Marshal(struct {
		SchemaVersion string                     `json:"schema_version"`
		Record        map[string]json.RawMessage `json:"record"`
	}{SchemaVersion, original.Record})
	if err != nil {
		return nil, err
	}
	var env struct {
		Record json.RawMessage `json:"record"`
	}
	if err = json.Unmarshal(b, &env); err != nil {
		return nil, err
	}
	return env.Record, nil
}

func TestRecordAddressGoldenCompatibility(t *testing.T) {
	fixtures, err := filepath.Glob("testdata/*.json")
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("fixtures=%v err=%v", fixtures, err)
	}
	counts := map[string]int{}
	for _, path := range fixtures {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// Walk RawMessages so number tokens never pass through float64.
			var walk func(json.RawMessage, string, bool)
			walk = func(raw json.RawMessage, location string, refusalVector bool) {
				raw = bytes.TrimSpace(raw)
				if len(raw) == 0 {
					t.Fatal("empty fixture value")
				}
				switch raw[0] {
				case '{':
					var fields map[string]json.RawMessage
					if err := json.Unmarshal(raw, &fields); err != nil {
						t.Fatal(err)
					}
					var refusal string
					if fields["error"] != nil {
						if err := json.Unmarshal(fields["error"], &refusal); err != nil {
							t.Fatal(err)
						}
					}
					refusalVector = refusalVector || refusal != ""
					var id string
					if fields["id"] != nil {
						if err := json.Unmarshal(fields["id"], &id); err != nil {
							t.Fatal(err)
						}
					}
					for _, prefix := range []string{"obs:", "note:", "ret:"} {
						if !strings.HasPrefix(id, prefix) {
							continue
						}
						t.Run(location, func(t *testing.T) {
							legacy, err := legacyRecordBytes(raw)
							if err != nil {
								t.Fatal(err)
							}
							withoutID := make(map[string]json.RawMessage, len(fields))
							for key, value := range fields {
								if key != "id" {
									withoutID[key] = value
								}
							}
							gotBytes, err := canonical.MarshalRecord(withoutID)
							if err != nil || !bytes.Equal(gotBytes, legacy) {
								t.Fatalf("record bytes drift: got=%s err=%v legacy=%s", gotBytes, err, legacy)
							}
							sum := sha256.Sum256(legacy)
							want := prefix + hex.EncodeToString(sum[:])
							digest, err := canonical.RecordDigest(withoutID)
							if err != nil || prefix+strings.TrimPrefix(digest, "sha256:") != want {
								t.Fatalf("record digest drift: got=%s err=%v legacy=%s", digest, err, want)
							}
							got, err := RecordAddress(prefix, raw)
							if err != nil || got != want {
								t.Fatalf("address drift: got=%s err=%v legacy=%s", got, err, want)
							}
							// Cycle refusals intentionally use fabricated graph identities.
							if !refusalVector && got != id {
								t.Fatalf("golden address drift: got=%s want=%s", got, id)
							}
							counts[prefix]++
						})
					}
					keys := make([]string, 0, len(fields))
					for key := range fields {
						keys = append(keys, key)
					}
					sort.Strings(keys)
					for _, key := range keys {
						walk(fields[key], location+"/"+key, refusalVector)
					}
				case '[':
					var values []json.RawMessage
					if err := json.Unmarshal(raw, &values); err != nil {
						t.Fatal(err)
					}
					for i, value := range values {
						walk(value, fmt.Sprintf("%s/%d", location, i), refusalVector)
					}
				}
			}
			walk(raw, "root", false)
		})
	}
	for _, prefix := range []string{"obs:", "note:", "ret:"} {
		if counts[prefix] == 0 {
			t.Fatalf("no golden records tested for %s", prefix)
		}
		t.Logf("%s golden record occurrences checked: %d", prefix, counts[prefix])
	}
}

func TestRecordAddressValidatesBeforeRemovingID(t *testing.T) {
	for _, tc := range []struct {
		name, raw, code string
	}{
		{"null-id", `{"id":null,"a":1}`, canonical.Null},
		{"duplicate-id", `{"id":"one","id":"two","a":1}`, canonical.DuplicateKey},
		{"invalid-id", `{"id":"\ud800","a":1}`, canonical.InvalidJSON},
		{"nonfinite-id", `{"id":1e999,"a":1}`, canonical.Nonfinite},
		{"array-root", `[]`, canonical.InvalidJSON},
		{"null-root", `null`, canonical.Null},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RecordAddress("obs:", json.RawMessage(tc.raw))
			var typed *canonical.Error
			if got != "" || !errors.As(err, &typed) || typed.Code != tc.code {
				t.Fatalf("got=%s error=%v want=%s", got, err, tc.code)
			}
		})
	}
}

func TestRecordAddressRemovesOnlyRootID(t *testing.T) {
	v := map[string]any{"id": "discard", "nested": map[string]any{"id": "keep"}, "a": []int{2, 1}}
	wantInput := map[string]any{"id": "discard", "nested": map[string]any{"id": "keep"}, "a": []int{2, 1}}
	sum := sha256.Sum256([]byte(`{"a":[2,1],"nested":{"id":"keep"}}`))
	for _, prefix := range []string{"obs:", "note:", "ret:"} {
		t.Run(prefix, func(t *testing.T) {
			want := prefix + hex.EncodeToString(sum[:])
			got, err := RecordAddress(prefix, v)
			if err != nil || got != want {
				t.Fatalf("got=%s error=%v want=%s", got, err, want)
			}
		})
	}
	if !reflect.DeepEqual(v, wantInput) {
		t.Fatalf("mutated record: %v", v)
	}
}

func TestRecordAddressDepth(t *testing.T) {
	for _, depth := range []int{999, 1000, 1001} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			raw := []byte(`{"nested":` + strings.Repeat("[", depth-1) + "0" + strings.Repeat("]", depth-1) + `}`)
			got, err := RecordAddress("obs:", json.RawMessage(raw))
			if depth > canonical.MaxDepth {
				var typed *canonical.Error
				if got != "" || !errors.As(err, &typed) || typed.Code != canonical.InvalidJSON {
					t.Fatalf("got=%s error=%v want=%s", got, err, canonical.InvalidJSON)
				}
				return
			}
			sum := sha256.Sum256(raw)
			want := "obs:" + hex.EncodeToString(sum[:])
			if err != nil || got != want {
				t.Fatalf("got=%s error=%v want=%s", got, err, want)
			}
			_, legacyErr := legacyRecordBytes(json.RawMessage(raw))
			if depth == 999 && legacyErr != nil {
				t.Fatal(legacyErr)
			}
			if depth == 1000 {
				var typed *canonical.Error
				if !errors.As(legacyErr, &typed) || typed.Code != canonical.InvalidJSON {
					t.Fatalf("legacy depth cap: got=%v want=%s", legacyErr, canonical.InvalidJSON)
				}
			}
		})
	}
}
