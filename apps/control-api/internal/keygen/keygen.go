package keygen

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/argon2"
)

const (
	// KeyPrefix is prepended to all generated keys for easy detection by secret scanners.
	KeyPrefix = "openserve_live_"
	// PrefixLength is the number of key chars stored as plaintext for fast DB lookup.
	// Must extend past KeyPrefix (15 chars) into the random portion so that the
	// stored prefix is unique per key and the DB lookup lands on exactly one row.
	// Format: "openserve_live_" (15) + 8 random hex chars = 23 chars total.
	PrefixLength = 23
)

// Params are the Argon2id parameters. Fixed to prevent hash migrations.
var Params = struct {
	Time    uint32
	Memory  uint32
	Threads uint8
	KeyLen  uint32
}{Time: 1, Memory: 64 * 1024, Threads: 4, KeyLen: 32}

// Generate creates a new API key. Returns the raw key (shown once) and its Argon2id hash.
// Raw key format: "openserve_live_<32 random hex bytes>"
func Generate() (rawKey, hash string, err error) {
	// Generate 16 random bytes (will be encoded as 32 hex chars)
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		return "", "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	rawKey = KeyPrefix + hex.EncodeToString(keyBytes)

	hash, err = Hash(rawKey)
	if err != nil {
		return "", "", err
	}

	return rawKey, hash, nil
}

// Hash computes the Argon2id hash of rawKey.
// Uses a random 16-byte salt embedded in the output as "$argon2id$v=19$m=65536,t=1,p=4$<base64-salt>$<base64-hash>" format.
func Hash(rawKey string) (string, error) {
	// Generate random 16-byte salt
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	// Compute Argon2id hash
	hash := argon2.IDKey(
		[]byte(rawKey),
		salt,
		Params.Time,
		Params.Memory,
		Params.Threads,
		Params.KeyLen,
	)

	// Encode salt and hash in base64
	saltB64 := base64.RawStdEncoding.EncodeToString(salt)
	hashB64 := base64.RawStdEncoding.EncodeToString(hash)

	// Return in standard Argon2id format
	encoded := fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		Params.Memory,
		Params.Time,
		Params.Threads,
		saltB64,
		hashB64,
	)

	return encoded, nil
}

// Verify checks rawKey against storedHash. Returns nil if they match.
// Constant-time to prevent timing attacks.
func Verify(rawKey, storedHash string) error {
	// Parse the stored hash to extract salt
	var version, m, t, p uint32
	var saltB64, hashB64 string

	_, err := fmt.Sscanf(
		storedHash,
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		&version, &m, &t, &p, &saltB64, &hashB64,
	)
	if err != nil {
		return fmt.Errorf("invalid hash format: %w", err)
	}

	// Decode salt
	salt, err := base64.RawStdEncoding.DecodeString(saltB64)
	if err != nil {
		return fmt.Errorf("failed to decode salt: %w", err)
	}

	// Compute hash of provided key with same salt
	computedHash := argon2.IDKey(
		[]byte(rawKey),
		salt,
		Params.Time,
		Params.Memory,
		Params.Threads,
		Params.KeyLen,
	)

	// Decode stored hash
	storedHashBytes, err := base64.RawStdEncoding.DecodeString(hashB64)
	if err != nil {
		return fmt.Errorf("failed to decode stored hash: %w", err)
	}

	// Constant-time comparison
	if subtle.ConstantTimeCompare(computedHash, storedHashBytes) != 1 {
		return fmt.Errorf("key does not match hash")
	}

	return nil
}

// Prefix returns the first PrefixLength characters of rawKey for DB lookup.
func Prefix(rawKey string) string {
	if len(rawKey) < PrefixLength {
		return rawKey
	}
	return rawKey[:PrefixLength]
}
