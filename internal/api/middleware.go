package api

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateLimitEntry struct {
	count   int
	resetAt time.Time
}

type inMemoryRateLimiter struct {
	mu      sync.Mutex
	entries map[string]rateLimitEntry
	limit   int
	window  time.Duration
}

func newInMemoryRateLimiter(limit int, window time.Duration) *inMemoryRateLimiter {
	return &inMemoryRateLimiter{
		entries: make(map[string]rateLimitEntry),
		limit:   limit,
		window:  window,
	}
}

func (s Server) withMiddlewares(next http.Handler) http.Handler {
	h := next
	h = s.authMiddleware(h)
	h = s.rateLimitMiddleware(h)
	return h
}

func (s Server) authMiddleware(next http.Handler) http.Handler {
	requireToken := strings.TrimSpace(s.APIToken) != ""
	basicEnabled := strings.TrimSpace(s.UIBasicUser) != "" || strings.TrimSpace(s.UIBasicPass) != ""
	if !requireToken && !basicEnabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if requireToken && tokenMatches(r, s.APIToken) {
			next.ServeHTTP(w, r)
			return
		}
		if basicEnabled && basicMatches(r, s.UIBasicUser, s.UIBasicPass) {
			next.ServeHTTP(w, r)
			return
		}
		if basicEnabled {
			w.Header().Set("WWW-Authenticate", `Basic realm="publisher"`)
		}
		writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
	})
}

func (s Server) rateLimitMiddleware(next http.Handler) http.Handler {
	if s.RateLimitRPM <= 0 {
		return next
	}
	limiter := newInMemoryRateLimiter(s.RateLimitRPM, time.Minute)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		key := rateLimitKey(r)
		allowed, retryAfter := limiter.allow(key, time.Now().UTC())
		if !allowed {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, fmt.Errorf("rate limit exceeded"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *inMemoryRateLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[key]
	if !ok || now.After(entry.resetAt) {
		l.entries[key] = rateLimitEntry{count: 1, resetAt: now.Add(l.window)}
		if len(l.entries) > 10000 {
			for k, v := range l.entries {
				if now.After(v.resetAt) {
					delete(l.entries, k)
				}
			}
		}
		return true, 0
	}
	if entry.count >= l.limit {
		return false, time.Until(entry.resetAt)
	}
	entry.count++
	l.entries[key] = entry
	return true, 0
}

func rateLimitKey(r *http.Request) string {
	if apiKey := strings.TrimSpace(r.Header.Get("X-API-Key")); apiKey != "" {
		return "key:" + apiKey
	}
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return "bearer:" + strings.TrimSpace(auth[7:])
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		return "ip:" + strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return "ip:" + host
	}
	if r.RemoteAddr != "" {
		return "ip:" + r.RemoteAddr
	}
	return "unknown"
}

func tokenMatches(r *http.Request, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	if key := strings.TrimSpace(r.Header.Get("X-API-Key")); key == expected {
		return true
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		token := strings.TrimSpace(auth[7:])
		return token == expected
	}
	return false
}

func basicMatches(r *http.Request, user, pass string) bool {
	u, p, ok := r.BasicAuth()
	if !ok {
		return false
	}
	return u == user && p == pass
}
