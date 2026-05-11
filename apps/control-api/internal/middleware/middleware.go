package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

type contextKey string

const orgIDKey contextKey = "org_id"

// RequestID returns middleware that sets X-Request-ID on every response.
// Uses the incoming header if present, generates one if not.
func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				// Generate a random request ID
				b := make([]byte, 8)
				if _, err := rand.Read(b); err != nil {
					requestID = "unknown"
				} else {
					requestID = hex.EncodeToString(b)
				}
			}

			w.Header().Set("X-Request-ID", requestID)
			r.Header.Set("X-Request-ID", requestID)

			next.ServeHTTP(w, r)
		})
	}
}

// OrgFromContext retrieves the org ID stored in context by auth middleware.
func OrgFromContext(ctx context.Context) string {
	orgID, ok := ctx.Value(orgIDKey).(string)
	if !ok {
		return ""
	}
	return orgID
}

// WithOrg stores orgID in context.
func WithOrg(ctx context.Context, orgID string) context.Context {
	return context.WithValue(ctx, orgIDKey, orgID)
}

// RealIP extracts the real client IP from X-Forwarded-For or X-Real-IP,
// with validation that the IP is not spoofed from internal ranges.
func RealIP() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var ip string

			// Check X-Forwarded-For first (may contain multiple IPs)
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				// Take the first IP (leftmost)
				ips := strings.Split(xff, ",")
				ip = strings.TrimSpace(ips[0])
			}

			// Fall back to X-Real-IP
			if ip == "" {
				if xri := r.Header.Get("X-Real-IP"); xri != "" {
					ip = xri
				}
			}

			// Fall back to remote address
			if ip == "" {
				ip, _, _ = net.SplitHostPort(r.RemoteAddr)
			}

			// Validate IP is not from internal ranges
			parsedIP := net.ParseIP(ip)
			if parsedIP != nil && !isPrivateIP(parsedIP) {
				r.Header.Set("X-Client-IP", ip)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isPrivateIP checks if an IP address is in a private/internal range.
func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast()
}

// RateLimit returns a middleware that limits requests per IP to n per minute.
// Intended for sensitive endpoints (auth callbacks, API key creation).
func RateLimit(requestsPerMinute int) func(http.Handler) http.Handler {
	type entry struct {
		mu         sync.Mutex
		timestamps []time.Time
	}
	var store sync.Map

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := getRealIP(r)
			val, _ := store.LoadOrStore(ip, &entry{})
			e := val.(*entry)

			e.mu.Lock()
			now := time.Now()
			window := now.Add(-time.Minute)
			// Evict old entries
			filtered := e.timestamps[:0]
			for _, t := range e.timestamps {
				if t.After(window) {
					filtered = append(filtered, t)
				}
			}
			e.timestamps = filtered

			if len(e.timestamps) >= requestsPerMinute {
				e.mu.Unlock()
				w.Header().Set("Retry-After", "60")
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			e.timestamps = append(e.timestamps, now)
			e.mu.Unlock()

			next.ServeHTTP(w, r)
		})
	}
}

// getRealIP extracts the real client IP from X-Forwarded-For or RemoteAddr.
func getRealIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP (leftmost = original client)
		if idx := strings.Index(xff, ","); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

// Logger returns structured request logging middleware using zap.
func Logger(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Create a response writer wrapper to capture status and size
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			log.Info("request started",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("request_id", r.Header.Get("X-Request-ID")),
			)

			next.ServeHTTP(wrapped, r)

			log.Info("request completed",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status_code", wrapped.statusCode),
				zap.Int("response_size", wrapped.size),
				zap.String("request_id", r.Header.Get("X-Request-ID")),
			)
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code and size.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	size       int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	size, err := rw.ResponseWriter.Write(b)
	rw.size += size
	return size, err
}
