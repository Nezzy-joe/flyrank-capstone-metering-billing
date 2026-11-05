package apikey

import (
	"strings"
	"testing"
)

func TestGenerate_And_Hash(t *testing.T) {
	raw, hash, prefix, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(raw, Prefix) {
		t.Fatalf("raw key %q lacks prefix %q", raw, Prefix)
	}
	if !strings.HasPrefix(prefix, Prefix) {
		t.Fatalf("display prefix %q lacks key prefix", prefix)
	}
	if len(hash) != 64 { // hex SHA-256
		t.Fatalf("hash length = %d, want 64", len(hash))
	}
	if Hash(raw) != hash {
		t.Fatalf("Hash(raw) != returned hash")
	}
	// Deterministic: same input, same hash.
	if Hash(raw) != Hash(raw) {
		t.Fatalf("Hash is not deterministic")
	}
}

func TestGenerate_Unique(t *testing.T) {
	raw1, hash1, _, _ := Generate()
	raw2, hash2, _, _ := Generate()
	if raw1 == raw2 {
		t.Fatal("two generated raw keys collided")
	}
	if hash1 == hash2 {
		t.Fatal("two generated hashes collided")
	}
}
