// Package canonical implements the project's JCS-based, null-free JSON digest.
package canonical

import (
	"bytes"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// MaxDepth limits open JSON containers, including the top-level object.
const MaxDepth = 1000

const (
	Null                 = "canonical_null"
	Nonfinite            = "canonical_nonfinite"
	MissingSchemaVersion = "canonical_missing_schema_version"
	Unordered            = "canonical_unordered"
	DuplicateKey         = "canonical_duplicate_key"
	InvalidJSON          = "canonical_invalid_json"
	// Unknown is the explicit marker for a required, unknown string/enum value.
	// Optional unknown values must be omitted. It is never a numeric zero.
	Unknown = "unknown"
)

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string           { return e.Code + ": " + e.Message }
func refusal(code, message string) error { return &Error{code, message} }

// Marshal passes Go values through encoding/json, then canonicalizes them.
// All required slices must be non-nil; optional unknown fields use omitempty.
func Marshal(v any) ([]byte, error) {
	return marshal(v, true)
}

// MarshalRecord canonicalizes an object without requiring schema_version.
// All other canonical rules apply, with depth counted from the record root.
// Every member, including id and a supplied schema_version, is retained.
func MarshalRecord(v any) ([]byte, error) {
	return marshal(v, false)
}

func marshal(v any, requireSchema bool) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		code := InvalidJSON
		if e, ok := err.(*json.UnsupportedValueError); ok && e.Value.IsValid() && (e.Value.Kind().String() == "float64" || e.Value.Kind().String() == "float32") {
			code = Nonfinite
		}
		return nil, refusal(code, err.Error())
	}
	if err := validGoStrings(reflect.ValueOf(v)); err != nil {
		return nil, err
	}
	return canonicalize(raw, requireSchema)
}

// Canonicalize preserves signed/unsigned 64-bit integer tokens exactly, extending
// JCS. Other number tokens are binary64, serialized as ECMAScript numbers.
// TODO(decision): plain integer tokens beyond both 64-bit domains are treated
// as binary64 numbers, like decimal/exponent tokens; they are not contract ints.
func Canonicalize(raw []byte) ([]byte, error) {
	return canonicalize(raw, true)
}

func canonicalize(raw []byte, requireSchema bool) ([]byte, error) {
	if err := validStrings(raw); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := readValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, refusal(InvalidJSON, "trailing JSON data")
	}
	object, ok := v.(map[string]any)
	if !ok {
		if !requireSchema {
			return nil, refusal(InvalidJSON, "record root must be an object")
		}
		return nil, refusal(MissingSchemaVersion, "top level must be an object with schema_version")
	}
	if requireSchema {
		if s, ok := object["schema_version"].(string); !ok || s == "" {
			return nil, refusal(MissingSchemaVersion, "schema_version must be a non-empty string")
		}
	}
	return appendValue(nil, v)
}

func Digest(v any) (string, error) {
	b, err := Marshal(v)
	if err != nil {
		return "", err
	}
	return digestBytes(b), nil
}

// RecordDigest returns sha256:<lowercase hex> over MarshalRecord's bytes.
// It retains id; callers applying domain identity rules must omit it themselves.
func RecordDigest(v any) (string, error) {
	b, err := MarshalRecord(v)
	if err != nil {
		return "", err
	}
	return digestBytes(b), nil
}

func digestBytes(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

// SortByKey explicitly normalizes a declared-key collection in place. Digesting
// never sorts arrays implicitly. Keys compare lexically by UTF-8 bytes.
func SortByKey[T any](items []T, key func(T) string) {
	sort.SliceStable(items, func(i, j int) bool { return key(items[i]) < key(items[j]) })
}
func CheckOrdered[T any](items []T, key func(T) string) error {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		k := key(item)
		if seen[k] {
			return refusal(DuplicateKey, "duplicate collection key: "+k)
		}
		seen[k] = true
	}
	for i := 1; i < len(items); i++ {
		if key(items[i-1]) > key(items[i]) {
			return refusal(Unordered, "collection keys must be ordered")
		}
	}
	return nil
}

func readValue(d *json.Decoder, depth int) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, refusal(InvalidJSON, err.Error())
	}
	if t == nil {
		return nil, refusal(Null, "null is forbidden")
	}
	if delim, ok := t.(json.Delim); ok {
		depth++
		if depth > MaxDepth {
			return nil, refusal(InvalidJSON, "nesting depth exceeds 1000")
		}
		switch delim {
		case '{':
			obj := map[string]any{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return nil, refusal(InvalidJSON, err.Error())
				}
				key, ok := k.(string)
				if !ok {
					return nil, refusal(InvalidJSON, "object key must be a string")
				}
				if _, exists := obj[key]; exists {
					return nil, refusal(DuplicateKey, "duplicate object key: "+key)
				}
				value, err := readValue(d, depth)
				if err != nil {
					return nil, err
				}
				obj[key] = value
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, refusal(InvalidJSON, "unclosed object")
			}
			return obj, nil
		case '[':
			arr := []any{}
			for d.More() {
				value, err := readValue(d, depth)
				if err != nil {
					return nil, err
				}
				arr = append(arr, value)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, refusal(InvalidJSON, "unclosed array")
			}
			return arr, nil
		}
		return nil, refusal(InvalidJSON, "unexpected delimiter")
	}
	return t, nil
}

func appendValue(out []byte, v any) ([]byte, error) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return utf16Less(keys[i], keys[j]) })
		out = append(out, '{')
		for i, k := range keys {
			if i > 0 {
				out = append(out, ',')
			}
			out = appendString(out, k)
			out = append(out, ':')
			var err error
			out, err = appendValue(out, x[k])
			if err != nil {
				return nil, err
			}
		}
		return append(out, '}'), nil
	case []any:
		out = append(out, '[')
		for i, item := range x {
			if i > 0 {
				out = append(out, ',')
			}
			var err error
			out, err = appendValue(out, item)
			if err != nil {
				return nil, err
			}
		}
		return append(out, ']'), nil
	case string:
		return appendString(out, x), nil
	case bool:
		return strconv.AppendBool(out, x), nil
	case json.Number:
		token := string(x)
		if !strings.ContainsAny(token, ".eE") {
			if n, err := strconv.ParseInt(token, 10, 64); err == nil {
				return strconv.AppendInt(out, n, 10), nil
			}
			if n, err := strconv.ParseUint(token, 10, 64); err == nil {
				return strconv.AppendUint(out, n, 10), nil
			}
		}
		n, err := strconv.ParseFloat(token, 64)
		if math.IsInf(n, 0) || math.IsNaN(n) {
			return nil, refusal(Nonfinite, "non-finite number")
		}
		if err != nil {
			return nil, refusal(InvalidJSON, err.Error())
		}
		if n == 0 {
			return append(out, '0'), nil
		}
		format := byte('e')
		if math.Abs(n) >= 1e-6 && math.Abs(n) < 1e21 {
			format = 'f'
		}
		s := strconv.FormatFloat(n, format, -1, 64)
		if format == 'e' {
			p := strings.IndexByte(s, 'e')
			exponent, _ := strconv.Atoi(s[p+1:])
			sign := ""
			if exponent >= 0 {
				sign = "+"
			}
			s = s[:p+1] + sign + strconv.Itoa(exponent)
		}
		return append(out, s...), nil
	}
	return nil, refusal(InvalidJSON, fmt.Sprintf("unsupported value %T", v))
}

func utf16Less(a, b string) bool {
	aa, bb := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(aa) && i < len(bb); i++ {
		if aa[i] != bb[i] {
			return aa[i] < bb[i]
		}
	}
	return len(aa) < len(bb)
}
func appendString(out []byte, s string) []byte {
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			out = append(out, '\\', byte(r))
		case '\b':
			out = append(out, '\\', 'b')
		case '\t':
			out = append(out, '\\', 't')
		case '\n':
			out = append(out, '\\', 'n')
		case '\f':
			out = append(out, '\\', 'f')
		case '\r':
			out = append(out, '\\', 'r')
		default:
			if r < 0x20 {
				const h = "0123456789abcdef"
				out = append(out, '\\', 'u', '0', '0', h[r>>4], h[r&15])
			} else {
				out = utf8.AppendRune(out, r)
			}
		}
	}
	return append(out, '"')
}

// encoding/json replaces invalid Unicode; reject it before decoding instead.
func validStrings(raw []byte) error {
	if !utf8.Valid(raw) {
		return refusal(InvalidJSON, "invalid UTF-8")
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		i++
		for i < len(raw) && raw[i] != '"' {
			if raw[i] != '\\' {
				i++
				continue
			}
			i++
			if i >= len(raw) {
				break
			}
			if raw[i] != 'u' {
				i++
				continue
			}
			if i+4 >= len(raw) {
				return refusal(InvalidJSON, "short Unicode escape")
			}
			n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
			if err != nil {
				return refusal(InvalidJSON, "invalid Unicode escape")
			}
			i += 5
			if n >= 0xdc00 && n <= 0xdfff {
				return refusal(InvalidJSON, "lone low surrogate")
			}
			if n >= 0xd800 && n <= 0xdbff {
				if i+5 >= len(raw) || raw[i] != '\\' || raw[i+1] != 'u' {
					return refusal(InvalidJSON, "lone high surrogate")
				}
				low, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return refusal(InvalidJSON, "invalid surrogate pair")
				}
				i += 6
			}
		}
	}
	return nil
}

// Check Go strings before encoding/json's replacement of malformed UTF-8 loses
// information. Custom JSON marshalers own their values; their output is checked
// by Canonicalize. Successful standard marshaling has already excluded cycles.
func validGoStrings(v reflect.Value) error {
	if !v.IsValid() {
		return nil
	}
	if v.CanInterface() {
		if _, ok := v.Interface().(json.Marshaler); ok {
			return nil
		}
		if _, ok := v.Interface().(encoding.TextMarshaler); ok {
			return nil
		}
	}
	if v.CanAddr() && v.Addr().CanInterface() {
		if _, ok := v.Addr().Interface().(json.Marshaler); ok {
			return nil
		}
		if _, ok := v.Addr().Interface().(encoding.TextMarshaler); ok {
			return nil
		}
	}
	switch v.Kind() {
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return refusal(InvalidJSON, "invalid UTF-8 in Go string")
		}
	case reflect.Interface, reflect.Pointer:
		if !v.IsNil() {
			return validGoStrings(v.Elem())
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			if err := validGoStrings(iter.Key()); err != nil {
				return err
			}
			if err := validGoStrings(iter.Value()); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := validGoStrings(v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Struct:
		typ := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := typ.Field(i)
			if field.PkgPath != "" || strings.Split(field.Tag.Get("json"), ",")[0] == "-" {
				continue
			}
			if err := validGoStrings(v.Field(i)); err != nil {
				return err
			}
		}
	}
	return nil
}
