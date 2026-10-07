package canonical

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Flag is immutable after flag parsing; it is not a global test seam.
var update = flag.Bool("update", false, "explicitly rewrite canonical golden bytes and digests")

func TestVectors(t *testing.T) {
	inputs, err := filepath.Glob("testdata/vectors/*.input.json")
	if err != nil || len(inputs) == 0 {
		t.Fatalf("vector inputs=%v err=%v", inputs, err)
	}
	for _, input := range inputs {
		name := strings.TrimSuffix(input, ".input.json")
		t.Run(filepath.Base(name), func(t *testing.T) {
			raw := readFile(t, input)
			var got []byte
			var err error
			if _, statErr := os.Stat(name + ".ordered"); statErr == nil {
				var v struct {
					Items []struct {
						ID string `json:"id"`
					} `json:"items"`
				}
				if e := json.Unmarshal(raw, &v); e != nil {
					t.Fatal(e)
				}
				err = CheckOrdered(v.Items, func(item struct {
					ID string `json:"id"`
				}) string {
					return item.ID
				})
			}
			if err == nil {
				got, err = Canonicalize(raw)
			}
			if expected, e := os.ReadFile(name + ".error"); e == nil {
				var typed *Error
				if !errors.As(err, &typed) || typed.Code != strings.TrimSpace(string(expected)) {
					t.Fatalf("got error=%v, want code=%q", err, strings.TrimSpace(string(expected)))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			h := sha256.Sum256(got)
			digest := "sha256:" + hex.EncodeToString(h[:])
			if *update {
				if err := os.WriteFile(name+".canonical.json", got, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name+".digest", []byte(digest+"\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			want := readFile(t, name+".canonical.json")
			if !bytes.Equal(got, want) {
				t.Fatalf("canonical got=%s\nwant=%s", got, want)
			}
			wantDigest := strings.TrimSpace(string(readFile(t, name+".digest")))
			if digest != wantDigest {
				t.Fatalf("digest got=%s want=%s", digest, wantDigest)
			}
			// Canonicalizing again must preserve the exact bytes.
			again, err := Canonicalize(got)
			if err != nil || !bytes.Equal(again, got) {
				t.Fatalf("roundtrip got=%s err=%v want=%s", again, err, got)
			}
		})
	}
}
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
func TestMarshal(t *testing.T) {
	for _, n := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := Marshal(map[string]any{"schema_version": "v1", "number": n})
		var typed *Error
		if !errors.As(err, &typed) || typed.Code != Nonfinite {
			t.Fatalf("number=%v got=%v want=%s", n, err, Nonfinite)
		}
	}
	for _, n := range []float64{1e20, 1e21, 1e-6, 1e-7, math.SmallestNonzeroFloat64, math.MaxFloat64} {
		if _, err := Marshal(map[string]any{"schema_version": "v1", "number": n}); err != nil {
			t.Fatalf("number=%v got=%v", n, err)
		}
	}
	_, err := Marshal(map[string]any{"schema_version": "v1", "nested": []any{nil}})
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != Null {
		t.Fatalf("got=%v want=%s", err, Null)
	}
}
func TestOrdered(t *testing.T) {
	tests := []struct {
		values []string
		code   string
	}{{[]string{}, ""}, {[]string{"a", "b"}, ""}, {[]string{"b", "a"}, Unordered}, {[]string{"a", "a"}, DuplicateKey}, {[]string{"b", "a", "b"}, DuplicateKey}}
	for _, tc := range tests {
		t.Run(strings.Join(tc.values, ","), func(t *testing.T) {
			err := CheckOrdered(tc.values, func(v string) string { return v })
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var typed *Error
			if !errors.As(err, &typed) || typed.Code != tc.code {
				t.Fatalf("got=%v want=%s", err, tc.code)
			}
		})
	}
	items := []string{"c", "a", "b"}
	SortByKey(items, func(v string) string { return v })
	if strings.Join(items, ",") != "a,b,c" {
		t.Fatalf("got=%v want=[a b c]", items)
	}
}

func TestInvalidGoString(t *testing.T) {
	for _, value := range []any{
		map[string]any{"schema_version": "v1", "value": string([]byte{255})},
		struct {
			SchemaVersion string `json:"schema_version"`
			Value         string `json:"value"`
		}{"v1", string([]byte{255})},
	} {
		_, err := Marshal(value)
		var typed *Error
		if !errors.As(err, &typed) || typed.Code != InvalidJSON {
			t.Fatalf("got=%v want=%s", err, InvalidJSON)
		}
	}
}

// Generated depth vectors count open containers, including the root object.
func TestDepthVectors(t *testing.T) {
	for _, tc := range []struct {
		name         string
		depth        int
		digest, code string
	}{
		{"depth-999", 999, "sha256:8a3c9d6d78c6bb6b0f32aaa2b87e94244a6179ae703ec718be052759dd90c8e7", ""},
		{"depth-1000", 1000, "sha256:d314e865c86cb2ecfffec871138a28151ca9dd7de3a4aadc9792f5d828c2c7ec", ""},
		{"depth-1001", 1001, "", "canonical_invalid_json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"nested":` + strings.Repeat("[", tc.depth-1) + "0" + strings.Repeat("]", tc.depth-1) + `,"schema_version":"v1"}`)
			got, err := Canonicalize(raw)
			if tc.code != "" {
				var typed *Error
				if !errors.As(err, &typed) || typed.Code != tc.code {
					t.Fatalf("got=%v want=%s", err, tc.code)
				}
				return
			}
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatalf("got=%s err=%v want=%s", got, err, raw)
			}
			hash := sha256.Sum256(got)
			digest := "sha256:" + hex.EncodeToString(hash[:])
			if digest != tc.digest {
				t.Fatalf("got=%s want=%s", digest, tc.digest)
			}
		})
	}
	// The same cap applies to object nesting and mixed containers.
	for _, depth := range []int{999, 1000, 1001} {
		raw := []byte(`{"schema_version":"v1","nested":` + strings.Repeat(`{"x":`, depth-1) + "0" + strings.Repeat("}", depth))
		_, err := Canonicalize(raw)
		if depth <= 1000 {
			if err != nil {
				t.Fatalf("object depth=%d got=%v want success", depth, err)
			}
		} else {
			var typed *Error
			if !errors.As(err, &typed) || typed.Code != InvalidJSON {
				t.Fatalf("depth=%d got=%v want=%s", depth, err, InvalidJSON)
			}
		}
	}
}
