package mcpserver

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nvpatel2002/hostelworld-mcp/internal/ratelimit"
)

// Middleware wraps the MCP HTTP handler with request-ID, structured logging,
// real-IP extraction, and per-IP rate limiting. See DESIGN.md §11.2 and §14.
type Middleware struct {
	logger         *slog.Logger
	limiter        *ratelimit.PerKey
	realIPHeader   string // e.g. "CF-Connecting-IP"; empty disables.
	maxRequestBody int64  // bytes; 0 = default (1 MiB)
}

func NewMiddleware(logger *slog.Logger, limiter *ratelimit.PerKey, realIPHeader string) *Middleware {
	return &Middleware{
		logger:         logger,
		limiter:        limiter,
		realIPHeader:   realIPHeader,
		maxRequestBody: 1 << 20,
	}
}

func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS — required for browser-based MCP clients and mcp-inspector.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Mcp-Session-Id, Mcp-Protocol-Version")
		// Expose-Headers lets the browser JS actually READ these response headers.
		// Without this, the inspector can't see the Mcp-Session-Id the server
		// returns from initialize, so it can't attach it to subsequent requests.
		w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		start := time.Now()
		reqID := uuid.NewString()
		ip := m.clientIP(r)

		w.Header().Set("X-Request-Id", reqID)
		r.Body = http.MaxBytesReader(w, r.Body, m.maxRequestBody)

		if !m.limiter.Allow(ip) {
			retry := m.limiter.RetryAfterSeconds(ip)
			w.Header().Set("Retry-After", itoa(retry))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"error": map[string]any{
					"code":    -32000,
					"message": "rate limit exceeded",
					"data": map[string]any{
						"code":                "rate_limited",
						"retry_after_seconds": retry,
					},
				},
			})
			m.logger.Warn("rate_limited",
				"request_id", reqID, "ip", ip, "path", r.URL.Path,
			)
			return
		}

		m.logger.Info("request",
			"request_id", reqID, "ip", ip, "method", r.Method, "path", r.URL.Path,
		)
		next.ServeHTTP(w, r)
		m.logger.Info("response",
			"request_id", reqID, "duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

func (m *Middleware) clientIP(r *http.Request) string {
	if m.realIPHeader != "" {
		if v := r.Header.Get(m.realIPHeader); v != "" {
			return ipKey(v)
		}
	}
	// Fall back to RemoteAddr.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ipKey(host)
}

// ipKey reduces an IP string to a stable rate-limit key. For IPv6 we key on
// the /64 prefix so an attacker can't trivially rotate within a single /64.
func ipKey(s string) string {
	s = strings.TrimSpace(s)
	ip := net.ParseIP(s)
	if ip == nil {
		return s
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	// IPv6: keep first 4 groups (64 bits).
	parts := strings.Split(ip.String(), ":")
	if len(parts) >= 4 {
		return strings.Join(parts[:4], ":") + "::/64"
	}
	return ip.String()
}
