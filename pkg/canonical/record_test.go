package canonical

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRecordVectors(t *testing.T) {
	inputs, err := filepath.Glob("testdata/records/*.input.json")
	if err != nil || len(inputs) == 0 {
		t.Fatalf("vector inputs=%v err=%v", inputs, err)
	}
	for _, input := range inputs {
		name := strings.TrimSuffix(input, ".input.json")
		t.Run(filepath.Base(name), func(t *testing.T) {
			v := json.RawMessage(readFile(t, input))
			if expected, err := os.ReadFile(name + ".error"); err == nil {
				checkRecordRefusal(t, v, strings.TrimSpace(string(expected)))
				return
			}
			checkRecord(t, v, readFile(t, name+".canonical.json"), strings.TrimSpace(string(readFile(t, name+".digest"))))
		})
	}
}

func TestRecordPreservesDocumentVectors(t *testing.T) {
	inputs, err := filepath.Glob("testdata/vectors/*.input.json")
	if err != nil || len(inputs) == 0 {
		t.Fatalf("vector inputs=%v err=%v", inputs, err)
	}
	for _, input := range inputs {
		name := strings.TrimSuffix(input, ".input.json")
		if _, err := os.Stat(name + ".error"); err == nil {
			continue
		}
		t.Run(filepath.Base(name), func(t *testing.T) {
			checkRecord(t, json.RawMessage(readFile(t, input)), readFile(t, name+".canonical.json"), strings.TrimSpace(string(readFile(t, name+".digest"))))
		})
	}
}

func checkRecord(t *testing.T, v any, want []byte, wantDigest string) {
	t.Helper()
	got, err := MarshalRecord(v)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("canonical got=%s err=%v want=%s", got, err, want)
	}
	digest, err := RecordDigest(v)
	if err != nil || digest != wantDigest {
		t.Fatalf("digest got=%s err=%v want=%s", digest, err, wantDigest)
	}
	again, err := MarshalRecord(json.RawMessage(got))
	if err != nil || !bytes.Equal(again, got) {
		t.Fatalf("roundtrip got=%s err=%v want=%s", again, err, got)
	}
}

func checkRecordRefusal(t *testing.T, v any, code string) {
	t.Helper()
	b, marshalErr := MarshalRecord(v)
	digest, digestErr := RecordDigest(v)
	for _, err := range []error{marshalErr, digestErr} {
		var typed *Error
		if !errors.As(err, &typed) || typed.Code != code {
			t.Fatalf("got=%v want typed %s", err, code)
		}
	}
	if b != nil || digest != "" {
		t.Fatalf("refusal returned bytes=%s digest=%s", b, digest)
	}
}

// Custom marshalers still pass through the same raw JSON validation.
type recordMarshaler string

func (v recordMarshaler) MarshalJSON() ([]byte, error) { return []byte(v), nil }

func TestRecordGoValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    any
		want string
	}{
		{"empty", struct{}{}, `{}`},
		{"struct", struct {
			A int `json:"a"`
		}{1}, `{"a":1}`},
		{"map", map[string]any{"z": math.Copysign(0, -1), "id": "keep", "a": []int{2, 1}}, `{"a":[2,1],"id":"keep","z":0}`},
		{"integers", map[string]any{"numbers": []any{int64(0), int64(9007199254740993), int64(math.MaxInt64), int64(math.MinInt64), uint64(math.MaxUint64)}}, string(readFile(t, "testdata/records/integers.canonical.json"))},
		{"floats", map[string]any{"numbers": []float64{1e-7, 1e-6, 1e20, 1e21, math.SmallestNonzeroFloat64, math.MaxFloat64}}, string(readFile(t, "testdata/records/float-boundaries.canonical.json"))},
		{"custom", recordMarshaler(`{"a":1}`), `{"a":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Expected hashes use the independent literal bytes, never encoder output.
			checkRecord(t, tc.v, []byte(tc.want), literalRecordDigest([]byte(tc.want)))
		})
	}
	for _, tc := range []struct {
		name, code string
		v          any
	}{
		{"nil", Null, nil},
		{"nil-map", Null, map[string]any(nil)},
		{"null-member", Null, map[string]any{"a": []any{nil}}},
		{"array", InvalidJSON, []int{}},
		{"string", InvalidJSON, "record"},
		{"number", InvalidJSON, 1},
		{"bool", InvalidJSON, true},
		{"nan", Nonfinite, map[string]any{"a": math.NaN()}},
		{"positive-infinity", Nonfinite, map[string]any{"a": math.Inf(1)}},
		{"negative-infinity", Nonfinite, map[string]any{"a": math.Inf(-1)}},
		{"invalid-string", InvalidJSON, map[string]any{"a": string([]byte{0xff})}},
		{"invalid-key", InvalidJSON, map[string]any{string([]byte{0xff}): "a"}},
		{"invalid-struct-string", InvalidJSON, struct {
			A string `json:"a"`
		}{string([]byte{0xff})}},
		{"unsupported", InvalidJSON, map[string]any{"a": make(chan int)}},
		{"custom-duplicate", DuplicateKey, recordMarshaler(`{"id":"one","id":"two"}`)},
		{"custom-null", Null, recordMarshaler(`{"id":null}`)},
		{"custom-surrogate", InvalidJSON, recordMarshaler(`{"a":"\ud800"}`)},
		{"custom-utf8", InvalidJSON, recordMarshaler("{\"a\":\"" + string([]byte{0xff}) + "\"}")},
		{"custom-nonfinite", Nonfinite, recordMarshaler(`{"a":1e999}`)},
		{"custom-array", InvalidJSON, recordMarshaler(`[]`)},
		{"custom-trailing", InvalidJSON, recordMarshaler(`{} {}`)},
	} {
		t.Run(tc.name, func(t *testing.T) { checkRecordRefusal(t, tc.v, tc.code) })
	}
}

func TestRecordSchemaException(t *testing.T) {
	for _, tc := range []struct {
		name, raw, documentCode string
	}{
		{"missing", `{}`, MissingSchemaVersion},
		{"empty", `{"schema_version":""}`, MissingSchemaVersion},
		{"number", `{"schema_version":1}`, MissingSchemaVersion},
		{"bool", `{"schema_version":false}`, MissingSchemaVersion},
		{"object", `{"schema_version":{}}`, MissingSchemaVersion},
		{"valid", `{"schema_version":"v1"}`, ""},
		{"null", `{"schema_version":null}`, Null},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := json.RawMessage(tc.raw)
			if tc.documentCode == Null {
				checkRecordRefusal(t, v, Null)
			} else {
				checkRecord(t, v, []byte(tc.raw), literalRecordDigest([]byte(tc.raw)))
			}
			_, canonicalErr := Canonicalize([]byte(tc.raw))
			_, marshalErr := Marshal(v)
			_, digestErr := Digest(v)
			for _, err := range []error{canonicalErr, marshalErr, digestErr} {
				if tc.documentCode == "" {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					var typed *Error
					if !errors.As(err, &typed) || typed.Code != tc.documentCode {
						t.Fatalf("document got=%v want=%s", err, tc.documentCode)
					}
				}
			}
		})
	}
}

func TestRecordDoesNotMutate(t *testing.T) {
	v := map[string]any{"id": "keep", "nested": map[string]any{"id": "nested"}, "a": []int{2, 1}}
	want := map[string]any{"id": "keep", "nested": map[string]any{"id": "nested"}, "a": []int{2, 1}}
	bytes := []byte(`{"a":[2,1],"id":"keep","nested":{"id":"nested"}}`)
	checkRecord(t, v, bytes, literalRecordDigest(bytes))
	if !reflect.DeepEqual(v, want) {
		t.Fatalf("mutated input: %v", v)
	}
}

func TestRecordDepthVectors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		depth  int
		digest string
	}{
		{"depth-999", 999, "sha256:06ce5cbfd0a20f0b6e452865d766e1edb44add39dfbfc511980dbddee074e729"},
		{"depth-1000", 1000, "sha256:62bc31a10cb369fcb297106db4d0ed0809d71f92d455fa52710e21fcf30e3549"},
		{"depth-1001", 1001, ""},
	} {
		for _, shape := range []string{"array", "object", "mixed"} {
			t.Run(tc.name+"/"+shape, func(t *testing.T) {
				inner := "0"
				var goValue any = 0
				for i := 0; i < tc.depth-1; i++ {
					if shape == "array" || shape == "mixed" && i%2 == 0 {
						inner = "[" + inner + "]"
						goValue = []any{goValue}
					} else {
						inner = `{"x":` + inner + `}`
						goValue = map[string]any{"x": goValue}
					}
				}
				raw := []byte(`{"nested":` + inner + `}`)
				for _, v := range []any{json.RawMessage(raw), map[string]any{"nested": goValue}} {
					if tc.depth > MaxDepth {
						checkRecordRefusal(t, v, InvalidJSON)
					} else {
						digest := tc.digest
						if shape != "array" {
							digest = literalRecordDigest(raw)
						}
						checkRecord(t, v, raw, digest)
					}
				}
			})
		}
	}
}

func literalRecordDigest(b []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(b))
}
