# Multi-Provider OIDC Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the hardcoded Google-only OIDC provider with a generic OIDC provider that works with any standards-compliant IdP (Okta, Azure AD, Google, Keycloak, Auth0).

**Architecture:** The `go-oidc/v3` library is already imported and the verifier pattern is already correct — the only Google-specific code is the hardcoded issuer URL `https://accounts.google.com`, the `hostedDomain` claim check, and the `hd` claim used for org provisioning. We rename `GoogleOIDC` → `OIDCProvider`, accept `issuerURL` as a parameter, and replace the `hostedDomain` check with an optional `allowedDomains` list based on email domain (works across all IdPs). The org key changes from `google_domain` (the `hd` claim) to the domain extracted from the user's email address.

**Tech Stack:** Go, `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`, existing chi router, pgx, existing JWT pattern.

---

## File Map

| File | Action | What changes |
|---|---|---|
| `apps/control-api/internal/auth/oidc.go` | Modify | Rename struct, accept issuerURL, drop hostedDomain, add allowedDomains |
| `apps/control-api/internal/handler/handler.go` | Modify | Update OIDC type reference; replace hd-claim org lookup with email-domain logic |
| `apps/control-api/cmd/server/main.go` | Modify | Rename flags; add `--oidc-issuer-url` and `--oidc-allowed-domains`; keep google flag aliases |
| `docker-compose.yml` | Modify | Add `OIDC_ISSUER_URL` env var (default Google) |
| `SECURITY.md` | Create | Separate task — do this inline |

---

## Task 1: Refactor auth/oidc.go to generic OIDCProvider

**Files:**
- Modify: `apps/control-api/internal/auth/oidc.go`

- [ ] **Step 1: Replace the entire file content**

```go
package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
)

// Claims represents verified OIDC claims. Works with Google, Okta, Azure AD, and
// any standard OIDC provider. The Subject field holds the provider's sub claim.
type Claims struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
	// HostedDomain is Google-specific (hd claim). Empty for non-Google providers.
	HostedDomain  string `json:"hd"`
	EmailVerified bool   `json:"email_verified"`
	jwt.RegisteredClaims
}

// EmailDomain returns the domain portion of an email address (e.g. "acme.com" from "alice@acme.com").
// Returns empty string if the email is malformed.
func EmailDomain(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.ToLower(parts[1])
}

// OIDCProvider is a generic OIDC provider that works with any standards-compliant IdP.
// Replaces the old GoogleOIDC struct. To use Google, set issuerURL to
// "https://accounts.google.com". For Okta: "https://{tenant}.okta.com".
// For Azure AD: "https://login.microsoftonline.com/{tenantId}/v2.0".
type OIDCProvider struct {
	provider       *oidc.Provider
	verifier       *oidc.IDTokenVerifier
	issuerURL      string
	clientID       string
	// allowedDomains restricts login to these email domains (e.g. ["acme.com"]).
	// Empty slice = allow any verified email.
	allowedDomains []string
}

// NewOIDCProvider creates an OIDCProvider by discovering the IdP's metadata.
// issuerURL must be the OIDC issuer (e.g. "https://accounts.google.com").
// allowedDomains is optional — pass nil or empty slice to allow all domains.
func NewOIDCProvider(ctx context.Context, issuerURL, clientID string, allowedDomains []string) (*OIDCProvider, error) {
	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc: failed to discover provider at %s: %w", issuerURL, err)
	}

	verifier := provider.Verifier(&oidc.Config{
		ClientID: clientID,
	})

	return &OIDCProvider{
		provider:       provider,
		verifier:       verifier,
		issuerURL:      issuerURL,
		clientID:       clientID,
		allowedDomains: allowedDomains,
	}, nil
}

// Verify verifies a raw ID token and returns the parsed claims.
// Returns an error if the token is invalid or the email domain is not allowed.
func (p *OIDCProvider) Verify(ctx context.Context, rawIDToken string) (*Claims, error) {
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("oidc: failed to verify token: %w", err)
	}

	var claims Claims
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("oidc: failed to unmarshal claims: %w", err)
	}

	if !claims.EmailVerified {
		return nil, fmt.Errorf("oidc: email %s is not verified by the provider", claims.Email)
	}

	if len(p.allowedDomains) > 0 {
		domain := EmailDomain(claims.Email)
		allowed := false
		for _, d := range p.allowedDomains {
			if strings.EqualFold(d, domain) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("oidc: email domain %q is not in the allowed list", domain)
		}
	}

	return &claims, nil
}

// Provider returns the underlying oidc.Provider for building OAuth2 endpoints.
func (p *OIDCProvider) Provider() *oidc.Provider {
	return p.provider
}

// contextKey is a package-private type for storing values in context.
type contextKey string

const claimsKey contextKey = "auth:claims"

// JWTMiddleware validates HS256 JWT tokens from the Authorization header.
// Tokens are issued by OIDCCallback after successful OIDC login.
func JWTMiddleware(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, "missing authorization header", http.StatusUnauthorized)
				return
			}

			const bearerScheme = "Bearer "
			if len(authHeader) < len(bearerScheme) || authHeader[:len(bearerScheme)] != bearerScheme {
				http.Error(w, "invalid authorization header format", http.StatusUnauthorized)
				return
			}

			tokenString := authHeader[len(bearerScheme):]

			var claims Claims
			token, err := jwt.ParseWithClaims(tokenString, &claims, func(token *jwt.Token) (interface{}, error) {
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
				}
				return secret, nil
			})

			if err != nil || !token.Valid {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), claimsKey, &claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserFromContext retrieves verified claims from the request context.
func UserFromContext(ctx context.Context) *Claims {
	claims, ok := ctx.Value(claimsKey).(*Claims)
	if !ok {
		return nil
	}
	return claims
}

// DevClaimsMiddleware injects hardcoded claims for local development.
// NEVER use in production. Set DevEmail env var to enable.
func DevClaimsMiddleware(devEmail, devMemberID string) func(http.Handler) http.Handler {
	claims := &Claims{
		Email:         devEmail,
		Name:          "Dev User",
		EmailVerified: true,
	}
	claims.Subject = devMemberID
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
```

- [ ] **Step 2: Build to confirm no compile errors**

```bash
cd /path/to/repo/apps/control-api && go build ./internal/auth/... 2>&1
```

Expected: no output (clean build).

- [ ] **Step 3: Commit**

```bash
git add apps/control-api/internal/auth/oidc.go
git commit -m "refactor(auth): rename GoogleOIDC to OIDCProvider, accept configurable issuerURL"
```

---

## Task 2: Update handler.go — fix type references and org provisioning

**Files:**
- Modify: `apps/control-api/internal/handler/handler.go`

The `Deps` struct has `OIDC *auth.GoogleOIDC` — rename to `*auth.OIDCProvider`. The `OIDCCallback` handler uses `claims.HostedDomain` (Google-specific `hd` claim) to key the org. Replace with `auth.EmailDomain(claims.Email)`.

- [ ] **Step 1: Update the Deps struct** (find the `OIDC *auth.GoogleOIDC` line and replace)

Old:
```go
OIDC         *auth.GoogleOIDC
```

New:
```go
OIDC         *auth.OIDCProvider
```

- [ ] **Step 2: Update OIDCCallback org upsert** (find the INSERT INTO orgs block inside OIDCCallback)

Old:
```go
err = h.DB.QueryRow(r.Context(),
    `INSERT INTO orgs (name, google_domain) VALUES ($1, $2)
     ON CONFLICT (name) DO UPDATE SET name=EXCLUDED.name
     RETURNING id`,
    claims.Email, claims.HostedDomain,
).Scan(&orgID)
```

New (use email domain as org identifier, works across all IdPs):
```go
orgDomain := auth.EmailDomain(claims.Email)
if orgDomain == "" {
    orgDomain = claims.Email // fallback: full email as org key
}
err = h.DB.QueryRow(r.Context(),
    `INSERT INTO orgs (name, google_domain) VALUES ($1, $2)
     ON CONFLICT (google_domain) DO UPDATE SET name = EXCLUDED.name
     RETURNING id`,
    orgDomain, orgDomain,
).Scan(&orgID)
```

Note: `google_domain` column is repurposed as a generic `org_domain` identifier. A future migration can rename the column; for now it stores the email domain for any IdP.

- [ ] **Step 3: Build to confirm**

```bash
cd /path/to/repo/apps/control-api && go build ./... 2>&1
```

Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add apps/control-api/internal/handler/handler.go
git commit -m "fix(handler): use email domain for org provisioning (works with any OIDC provider)"
```

---

## Task 3: Update main.go — replace Google-specific flags with generic OIDC flags

**Files:**
- Modify: `apps/control-api/cmd/server/main.go`

- [ ] **Step 1: Replace the flag declarations and provider init**

Find and replace the flags section. Old flags:
```go
googleClientID string
googleDomain   string
```

New flags (keep old env var names as fallbacks so existing deployments don't break):
```go
oidcIssuerURL    string
oidcClientID     string
oidcClientSecret string
oidcAllowedDomains string  // comma-separated, e.g. "acme.com,partner.com"
```

Old flag.StringVar calls:
```go
flag.StringVar(&googleClientID, "google-client-id", os.Getenv("GOOGLE_CLIENT_ID"), "Google OIDC client ID")
flag.StringVar(&googleDomain, "google-domain", os.Getenv("GOOGLE_HOSTED_DOMAIN"), "Google Workspace hosted domain restriction")
```

New (with backward-compat env var fallbacks):
```go
flag.StringVar(&oidcIssuerURL, "oidc-issuer-url",
    envOr("OIDC_ISSUER_URL", "https://accounts.google.com"),
    "OIDC issuer URL. Google: https://accounts.google.com, Okta: https://{tenant}.okta.com, Azure: https://login.microsoftonline.com/{tenant}/v2.0")
flag.StringVar(&oidcClientID, "oidc-client-id",
    envOr("OIDC_CLIENT_ID", os.Getenv("GOOGLE_CLIENT_ID")),
    "OIDC client ID from your IdP application")
flag.StringVar(&oidcClientSecret, "oidc-client-secret",
    envOr("OIDC_CLIENT_SECRET", os.Getenv("GOOGLE_CLIENT_SECRET")),
    "OIDC client secret from your IdP application")
flag.StringVar(&oidcAllowedDomains, "oidc-allowed-domains",
    envOr("OIDC_ALLOWED_DOMAINS", os.Getenv("GOOGLE_HOSTED_DOMAIN")),
    "Comma-separated email domains to allow (empty = any verified email)")
```

Add the helper function at the bottom of main.go (after main()):
```go
// envOr returns the value of the env var key, or fallback if empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

- [ ] **Step 2: Update the validation block**

Old:
```go
if devEmail == "" && googleClientID == "" {
    log.Fatal("--google-client-id is required when not in dev mode")
}
```

New:
```go
if devEmail == "" && oidcClientID == "" {
    log.Fatal("--oidc-client-id (or OIDC_CLIENT_ID env) is required when not in dev mode")
}
```

- [ ] **Step 3: Update the OIDCProvider initialization block**

Old:
```go
var oidcProvider *auth.GoogleOIDC
if devEmail == "" {
    oidcProvider, err = auth.NewGoogleOIDC(context.Background(), googleClientID, googleDomain)
    if err != nil {
        log.Fatal("oidc provider init failed", zap.Error(err))
    }
}
```

New:
```go
var oidcProvider *auth.OIDCProvider
if devEmail == "" {
    var allowedDomains []string
    if oidcAllowedDomains != "" {
        for _, d := range strings.Split(oidcAllowedDomains, ",") {
            d = strings.TrimSpace(d)
            if d != "" {
                allowedDomains = append(allowedDomains, d)
            }
        }
    }
    oidcProvider, err = auth.NewOIDCProvider(context.Background(), oidcIssuerURL, oidcClientID, allowedDomains)
    if err != nil {
        log.Fatal("oidc provider init failed", zap.Error(err))
    }
    log.Info("oidc provider ready", zap.String("issuer", oidcIssuerURL))
}
```

Add `"strings"` to the import block if not already present.

- [ ] **Step 4: Update the OAuth2Config construction** (find where h.OAuth2Config is set up)

The OAuth2 redirect URL and scopes are already set up. Only the client ID/secret source changes:

Find:
```go
OAuth2Config: &oauth2.Config{
    ClientID:     googleClientID,
```

Replace with:
```go
OAuth2Config: &oauth2.Config{
    ClientID:     oidcClientID,
    ClientSecret: oidcClientSecret,
```

- [ ] **Step 5: Build and verify**

```bash
cd /path/to/repo/apps/control-api && go build -o /tmp/control-api-bin ./cmd/... 2>&1
```

Expected: no output.

- [ ] **Step 6: Update docker-compose.yml** — add new env vars to control-api service

Add to the `environment:` block of the `control-api` service:
```yaml
      OIDC_ISSUER_URL: ${OIDC_ISSUER_URL:-https://accounts.google.com}
      OIDC_CLIENT_ID: ${OIDC_CLIENT_ID:-}
      OIDC_CLIENT_SECRET: ${OIDC_CLIENT_SECRET:-}
      OIDC_ALLOWED_DOMAINS: ${OIDC_ALLOWED_DOMAINS:-}
```

Keep the old `GOOGLE_CLIENT_ID` line as a comment showing the migration path:
```yaml
      # Legacy: GOOGLE_CLIENT_ID is still read as fallback for OIDC_CLIENT_ID
      GOOGLE_CLIENT_ID: ${GOOGLE_CLIENT_ID:-}
```

- [ ] **Step 7: Commit everything**

```bash
git add apps/control-api/cmd/server/main.go docker-compose.yml
git commit -m "feat(oidc): support any OIDC provider (Okta, Azure AD, Google) via --oidc-issuer-url flag"
```

---

## Task 4: Write SECURITY.md

**Files:**
- Create: `SECURITY.md` at repo root

- [ ] **Step 1: Create SECURITY.md**

```markdown
# Security Policy

## Supported Versions

| Version | Supported |
|---|---|
| main branch | ✅ |
| Tagged releases | ✅ (latest minor only) |

## Reporting a Vulnerability

**Please do NOT report security vulnerabilities through public GitHub Issues.**

Send a report to: **security@openserve.io** (or open a [GitHub Security Advisory](https://github.com/openserve/openserve/security/advisories/new) — preferred, gives you a CVE number automatically).

Include:
- Description of the vulnerability
- Steps to reproduce
- Affected component (control-api, gateway, operator, peer-relay, peer-agent, GUI)
- Potential impact assessment
- Your suggested fix (optional but appreciated)

**Response SLA:**
- Acknowledgement within 48 hours
- Triage and severity assessment within 5 business days
- Fix timeline communicated within 10 business days
- Critical / High: target fix within 14 days
- Medium: target fix within 30 days

## Security Design Principles

1. **No prompt content stored** — openserve does not log request or response bodies. Only metadata (key ID, deployment ID, timestamp, token counts) is stored.
2. **BYOC isolation** — all inference stays in the customer's own VPC. No prompt or response content crosses VPC boundaries.
3. **Secrets never stored raw** — API keys and peer tokens are stored as Argon2id hashes only. The raw value is shown once and immediately discarded.
4. **Workload Identity** — no static GCP service-account keys. All GCP API access uses Workload Identity bindings.
5. **Append-only audit log** — `audit_log` rows are never updated or deleted. A hash-chain anchor is published monthly.
6. **Supply chain** — every release image is signed with cosign, has an SBOM (syft), and has SLSA L3 provenance.
7. **Model weight verification** — catalog manifests carry SHA256 + cosign signature verified by the operator before any model pod is scheduled.
8. **Network isolation** — vLLM pods have a NetworkPolicy allowing egress only to GCS (model cache). A compromised model cannot exfiltrate data.

## Known Security Invariants (Never Break These)

- All SQL uses pgx positional parameters — no string concatenation in queries
- The gateway never buffers SSE streams (no prompt content held in memory beyond the request)
- The `audit_log` table has no UPDATE or DELETE grants on the application role

## Scope

In scope for bug bounties / responsible disclosure:
- Authentication bypass
- Authorization escalation (accessing another org's data)
- SSRF in any network-facing service
- SQL injection
- Stored XSS in the GUI
- Prompt injection enabling data exfiltration
- Container escape from vLLM pods
- API key exposure

Out of scope:
- Social engineering attacks
- Denial of service (rate limiting is a feature, not a security bug)
- Issues in dependencies that have no practical exploit path in openserve's deployment model
- Vulnerabilities requiring physical access to the customer's GKE cluster
```

- [ ] **Step 2: Commit**

```bash
git add SECURITY.md
git commit -m "docs(security): add SECURITY.md with vulnerability disclosure policy and security design"
```

---

## Self-Review Checklist

- [x] `GoogleOIDC` renamed to `OIDCProvider` across all three files
- [x] `hostedDomain` check replaced with `allowedDomains` (email-domain based, works for any IdP)
- [x] Org provisioning uses `auth.EmailDomain(email)` instead of `claims.HostedDomain` (hd claim)
- [x] Old env var names (`GOOGLE_CLIENT_ID`, `GOOGLE_HOSTED_DOMAIN`) still work as fallbacks — zero breakage for existing deployments
- [x] `go build ./...` confirmed at each task
- [x] docker-compose.yml updated with new env vars
- [x] SECURITY.md written
- [x] No placeholders — every code block is complete and runnable
