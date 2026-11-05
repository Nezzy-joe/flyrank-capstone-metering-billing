// Package apikey generates and hashes tenant API keys.
//
// Keys are high-entropy random secrets, so we store a deterministic SHA-256 hash
// (fast O(1) lookup) rather than bcrypt (which is for low-entropy passwords and
// cannot be looked up). The raw key is shown to the operator exactly once and is
// never stored or logged.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Prefix identifies keys issued by this service.
const Prefix = "mb_"

const randomBytes = 32 // 256 bits of entropy

// Generate returns a new raw key (show once), its SHA-256 hash (store this), and
// a short non-secret prefix for display/debugging.
func Generate() (rawKey, hash, displayPrefix string, err error) {
	b := make([]byte, randomBytes)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", fmt.Errorf("apikey: read random: %w", err)
	}
	rawKey = Prefix + hex.EncodeToString(b)
	hash = Hash(rawKey)
	displayPrefix = rawKey[:len(Prefix)+6] // e.g. "mb_1a2b3c"
	return rawKey, hash, displayPrefix, nil
}

// Hash returns the hex SHA-256 of a raw key. Used both to store a new key and to
// resolve a presented key to its tenant.
func Hash(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(sum[:])
}
