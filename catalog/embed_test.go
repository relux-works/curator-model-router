package catalog

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestDefaultIsEmbeddedAndIndependent(t *testing.T) {
	a, b := Default(), Default()
	if !json.Valid(a) || !bytes.Equal(a, b) {
		t.Fatal("invalid embedded catalog")
	}
	a[0] = '!'
	if !bytes.Equal(b, Default()) {
		t.Fatal("caller mutated embedded bytes")
	}
}
