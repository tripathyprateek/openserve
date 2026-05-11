// Package auth provides API key validation against a PostgreSQL database.
package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"
)

// KeyInfo holds the validated API key metadata.
type KeyInfo struct {
	KeyID              string
	Role               string
	AllowedDeployments []string
	RPM                int32
	TPM                int32
	IPAllowlist        []string
}

// KeyValidator validates raw API keys against stored credentials.
type KeyValidator interface {
	Validate(ctx context.Context, rawKey, deploymentID string) (*KeyInfo, error)
}

// PostgresKeyValidator implements KeyValidator using a PostgreSQL database.
type PostgresKeyValidator struct {
	pool *pgxpool.Pool
}

// NewKeyValidator creates a new PostgreSQL-backed key validator.
// It establishes a connection pool to the provided Postgres URL.
func NewKeyValidator(ctx context.Context, postgresURL string) (KeyValidator, error) {
	pool, err := pgxpool.New(ctx, postgresURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create pgx pool: %w", err)
	}

	// Test the connection.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres connection failed: %w", err)
	}

	return &PostgresKeyValidator{pool: pool}, nil
}

const validateQuery = `
SELECT key_hash, role, allowed_deployments, rpm, tpm, ip_allowlist, expires_at, active
FROM api_keys
WHERE key_prefix = @prefix
  AND active = true
  AND (expires_at IS NULL OR expires_at > NOW())
`

// Validate checks the raw key against the database and returns KeyInfo if valid.
func (v *PostgresKeyValidator) Validate(ctx context.Context, rawKey, deploymentID string) (*KeyInfo, error) {
	// key format: "openserve_live_<32 hex chars>" — 15-char constant prefix + 32 random chars
	// We store the first 23 chars (prefix + 8 random chars) as the DB lookup key so that
	// every key maps to exactly one DB row. Must stay in sync with keygen.PrefixLength.
	const lookupPrefixLen = 23
	if len(rawKey) < lookupPrefixLen {
		return nil, errors.New("invalid key format")
	}

	// Extract the prefix (first 23 chars) for indexed lookup.
	prefix := rawKey[:lookupPrefixLen]

	// Query the database.
	var keyHash string
	var role string
	var allowedDeployments []string
	var rpm, tpm int32
	var ipAllowlist []string
	var expiresAt *time.Time
	var active bool

	err := v.pool.QueryRow(ctx, validateQuery,
		pgx.NamedArgs{
			"prefix": prefix,
		},
	).Scan(&keyHash, &role, &allowedDeployments, &rpm, &tpm, &ipAllowlist, &expiresAt, &active)

	if err != nil {
		return nil, fmt.Errorf("key not found: %w", err)
	}

	// Verify the full key against the stored Argon2id hash.
	// The control-api stores keys in PHC string format:
	//   $argon2id$v=19$m=65536,t=1,p=4$<base64-salt>$<base64-hash>
	// We parse it and re-derive the hash with the embedded salt.
	if err := verifyArgon2idPHC(rawKey, keyHash); err != nil {
		return nil, errors.New("invalid key")
	}

	// Check deployment access (empty slice = allow all).
	if len(allowedDeployments) > 0 {
		allowed := false
		for _, dep := range allowedDeployments {
			if dep == deploymentID {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("deployment %q not allowed for this key", deploymentID)
		}
	}

	return &KeyInfo{
		KeyID:              prefix, // Use prefix as key ID for tracking
		Role:               role,
		AllowedDeployments: allowedDeployments,
		RPM:                rpm,
		TPM:                tpm,
		IPAllowlist:        ipAllowlist,
	}, nil
}

// Pool returns the underlying PostgreSQL connection pool.
func (v *PostgresKeyValidator) Pool() *pgxpool.Pool {
	return v.pool
}

// verifyArgon2idPHC verifies rawKey against a PHC-encoded Argon2id hash of the form:
//
//	$argon2id$v=19$m=65536,t=1,p=4$<base64url-salt>$<base64url-hash>
//
// This matches the format produced by the control-api's keygen.Hash() function.
// The function is intentionally not timing-safe on format errors (only on hash comparison).
func verifyArgon2idPHC(rawKey, storedHash string) error {
	// Split on '$': ["", "argon2id", "v=19", "m=65536,t=1,p=4", "<salt>", "<hash>"]
	parts := strings.Split(storedHash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return errors.New("invalid hash format")
	}

	// Parse parameters from parts[3]: "m=65536,t=1,p=4"
	var memory, timeCost, parallelism uint32
	for _, kv := range strings.Split(parts[3], ",") {
		pair := strings.SplitN(kv, "=", 2)
		if len(pair) != 2 {
			return errors.New("invalid hash params")
		}
		val, err := strconv.ParseUint(pair[1], 10, 32)
		if err != nil {
			return fmt.Errorf("invalid param %s: %w", pair[0], err)
		}
		switch pair[0] {
		case "m":
			memory = uint32(val)
		case "t":
			timeCost = uint32(val)
		case "p":
			parallelism = uint32(val)
		}
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return fmt.Errorf("failed to decode salt: %w", err)
	}

	storedHashBytes, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return fmt.Errorf("failed to decode hash: %w", err)
	}

	computed := argon2.IDKey([]byte(rawKey), salt, timeCost, memory, uint8(parallelism), uint32(len(storedHashBytes)))

	if !constantTimeCompare(computed, storedHashBytes) {
		return errors.New("key mismatch")
	}
	return nil
}

// constantTimeCompare performs constant-time byte slice comparison.
func constantTimeCompare(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var result byte
	for i := range a {
		result |= a[i] ^ b[i]
	}
	return result == 0
}
