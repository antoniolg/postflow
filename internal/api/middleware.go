package api

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
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
	h = s.requestLoggingMiddleware(h)
	return h
}

func (s Server) requestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now().UTC()
		requestID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if requestID == "" {
			requestID = generateRequestID()
		}
		w.Header().Set("X-Request-Id", requestID)
		rec := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rec, r)

		slog.Info("http request",
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.statusCode,
			"duration_ms", time.Since(startedAt).Milliseconds(),
			"client", requestClientLabel(r),
		)
	})
}

func (s Server) authMiddleware(next http.Handler) http.Handler {
	requireToken := strings.TrimSpace(s.APIToken) != ""
	basicEnabled := strings.TrimSpace(s.UIBasicUser) != "" || strings.TrimSpace(s.UIBasicPass) != ""
	localAuthEnabled := s.LocalAuthEnabled
	if !requireToken && !basicEnabled && !localAuthEnabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if isPublicAssetPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if isOAuthCallbackPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if isLocalAuthPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if isMCPPath(r.URL.Path) && mcpAllowsAnonymousRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		if mediaID, ok := parseMediaContentPath(r.URL.Path); ok {
			if s.signedMediaAccessAllowed(r, mediaID) {
				next.ServeHTTP(w, r)
				return
			}
			if localAuthEnabled {
				if _, _, err := s.currentOwnerFromSession(r); err == nil {
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		if requireToken && tokenMatches(r, s.APIToken) {
			next.ServeHTTP(w, r)
			return
		}
		if isMCPPath(r.URL.Path) && s.oauthAccessTokenMatches(r) {
			next.ServeHTTP(w, r)
			return
		}
		if basicEnabled && basicMatches(r, s.UIBasicUser, s.UIBasicPass) {
			next.ServeHTTP(w, r)
			return
		}
		if localAuthEnabled && requestCanUseLocalSession(r) {
			if _, _, err := s.currentOwnerFromSession(r); err == nil {
				next.ServeHTTP(w, r)
				return
			}
			target := "/login"
			if r.Method == http.MethodGet {
				if returnTo := sanitizeReturnTo(r.URL.RequestURI()); returnTo != "" {
					target += "?return_to=" + url.QueryEscape(returnTo)
				}
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		if isMCPPath(r.URL.Path) {
			w.Header().Set("WWW-Authenticate", oauthWWWAuthenticateHeader(r, s.publicBaseURL(r)))
		} else if basicEnabled {
			w.Header().Set("WWW-Authenticate", `Basic realm="postflow"`)
		}
		writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
	})
}

func isPublicAssetPath(path string) bool {
	trimmed := strings.TrimSpace(path)
	return strings.HasPrefix(trimmed, "/assets/")
}

func isLocalAuthPublicPath(path string) bool {
	trimmed := strings.TrimSpace(path)
	switch trimmed {
	case "/login", "/logout", "/authorize", "/token", "/oauth/register", "/.well-known/oauth-authorization-server", "/.well-known/openid-configuration", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp":
		return true
	default:
		return false
	}
}

func isMCPPath(path string) bool {
	trimmed := strings.TrimSpace(path)
	return trimmed == "/mcp" || strings.HasPrefix(trimmed, "/mcp/")
}

func mcpAllowsAnonymousRequest(r *http.Request) bool {
	if r == nil || r.Method != http.MethodPost || !isMCPPath(r.URL.Path) {
		return false
	}

	const maxBodyBytes = 1 << 20
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) == 0 || len(body) > maxBodyBytes {
		return false
	}

	var single struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &single); err == nil {
		return isAnonymousMCPMethod(single.Method)
	}

	var batch []struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &batch); err != nil {
		return false
	}
	if len(batch) == 0 {
		return false
	}
	for _, item := range batch {
		if !isAnonymousMCPMethod(item.Method) {
			return false
		}
	}
	return true
}

func isAnonymousMCPMethod(method string) bool {
	switch strings.TrimSpace(method) {
	case "initialize", "notifications/initialized", "ping", "tools/list":
		return true
	default:
		return false
	}
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

func requestClientLabel(r *http.Request) string {
	if apiKey := strings.TrimSpace(r.Header.Get("X-API-Key")); apiKey != "" {
		return "key:" + shortFingerprint(apiKey)
	}
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		token := strings.TrimSpace(auth[7:])
		if token != "" {
			return "bearer:" + shortFingerprint(token)
		}
	}
	return rateLimitKey(r)
}

func shortFingerprint(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])[:12]
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

func parseMediaContentPath(path string) (mediaID string, ok bool) {
	trimmed := strings.TrimSpace(path)
	if !strings.HasPrefix(trimmed, "/media/") || !strings.HasSuffix(trimmed, "/content") {
		return "", false
	}
	mediaID = strings.TrimSuffix(strings.TrimPrefix(trimmed, "/media/"), "/content")
	mediaID = strings.TrimSpace(mediaID)
	if mediaID == "" || strings.Contains(mediaID, "/") {
		return "", false
	}
	return mediaID, true
}

func isOAuthCallbackPath(path string) bool {
	trimmed := strings.TrimSpace(path)
	if !strings.HasPrefix(trimmed, "/oauth/") {
		return false
	}
	return strings.HasSuffix(trimmed, "/callback")
}

func (s Server) signedMediaAccessAllowed(r *http.Request, mediaID string) bool {
	expRaw := strings.TrimSpace(r.URL.Query().Get("exp"))
	sig := strings.TrimSpace(r.URL.Query().Get("sig"))
	if expRaw == "" || sig == "" {
		return false
	}
	expUnix, err := strconv.ParseInt(expRaw, 10, 64)
	if err != nil || expUnix <= 0 {
		return false
	}
	nowUnix := time.Now().UTC().Unix()
	if expUnix < nowUnix {
		return false
	}
	if expUnix > nowUnix+int64(60*60) {
		return false
	}
	payload := fmt.Sprintf("%s:%d", strings.TrimSpace(mediaID), expUnix)
	return s.credentialsCipher().VerifyString(payload, sig)
}

func basicMatches(r *http.Request, user, pass string) bool {
	u, p, ok := r.BasicAuth()
	if !ok {
		return false
	}
	return u == user && p == pass
}

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func generateRequestID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req_%d", time.Now().UnixNano())
	}
	return "req_" + hex.EncodeToString(b[:])
}
