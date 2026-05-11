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
// Examples of issuerURL values:
//   - Google: "https://accounts.google.com"
//   - Okta: "https://{tenant}.okta.com/oauth2/default"
//   - Azure AD: "https://login.microsoftonline.com/{tenantId}/v2.0"
//   - GitHub Actions OIDC: "https://token.actions.githubusercontent.com"
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

// NewGoogleOIDC creates an OIDCProvider configured for Google OIDC (backward-compat wrapper).
// This is deprecated; use NewOIDCProvider directly.
func NewGoogleOIDC(ctx context.Context, clientID, hostedDomain string) (*OIDCProvider, error) {
	var allowedDomains []string
	if hostedDomain != "" {
		allowedDomains = []string{hostedDomain}
	}
	return NewOIDCProvider(ctx, "https://accounts.google.com", clientID, allowedDomains)
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

// contextKey is a type for storing values in context.
type contextKey string

const claimsKey contextKey = "auth:claims"

// JWTMiddleware validates HS256 JWT tokens. It reads the token from the
// Authorization: Bearer header first, then falls back to the "openserve_session" cookie.
func JWTMiddleware(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenString := ""

			// Try Authorization: Bearer header
			authHeader := r.Header.Get("Authorization")
			if authHeader != "" {
				const bearerScheme = "Bearer "
				if len(authHeader) >= len(bearerScheme) && authHeader[:len(bearerScheme)] == bearerScheme {
					tokenString = authHeader[len(bearerScheme):]
				}
			}

			// Fall back to session cookie set by OIDCCallback
			if tokenString == "" {
				if cookie, err := r.Cookie("openserve_session"); err == nil && cookie.Value != "" {
					tokenString = cookie.Value
				}
			}

			if tokenString == "" {
				http.Error(w, "unauthorized: no token", http.StatusUnauthorized)
				return
			}

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

// UserFromContext retrieves the claims from the request context.
func UserFromContext(ctx context.Context) *Claims {
	claims, ok := ctx.Value(claimsKey).(*Claims)
	if !ok {
		return nil
	}
	return claims
}

// DevClaimsMiddleware injects hardcoded claims for local development without
// validating any token. Only use when DEV_EMAIL is set; never in production.
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
