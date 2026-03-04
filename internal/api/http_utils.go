package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

func sanitizeReturnTo(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return ""
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.RequestURI()
}

func withQueryValue(rawURL, key, value string) string {
	rawURL = sanitizeReturnTo(rawURL)
	if rawURL == "" {
		rawURL = "/"
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "/"
	}
	q := parsed.Query()
	q.Set(key, value)
	parsed.RawQuery = q.Encode()
	return parsed.RequestURI()
}

func parseRange(_ context.Context, fromRaw, toRaw string) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	to := now.Add(30 * 24 * time.Hour)
	if fromRaw != "" {
		parsed, err := time.Parse(time.RFC3339, fromRaw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid from: %w", err)
		}
		from = parsed.UTC()
	}
	if toRaw != "" {
		parsed, err := time.Parse(time.RFC3339, toRaw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid to: %w", err)
		}
		to = parsed.UTC()
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, errors.New("to must be after from")
	}
	return from, to, nil
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return -1
		}
	}, s)
	return s
}

func extractPostIDFromPath(path, action string) (string, error) {
	if !strings.HasPrefix(path, "/posts/") || !strings.HasSuffix(path, "/"+action) {
		return "", errors.New("not found")
	}
	trimmed := strings.TrimPrefix(path, "/posts/")
	id := strings.TrimSuffix(trimmed, "/"+action)
	id = strings.TrimSuffix(id, "/")
	if id == "" {
		return "", errors.New("invalid post id")
	}
	return id, nil
}

func defaultStatusForScheduledAt(t time.Time) domain.PostStatus {
	if t.IsZero() {
		return domain.PostStatusDraft
	}
	return domain.PostStatusScheduled
}

func canEditPostStatus(status domain.PostStatus) bool {
	switch status {
	case domain.PostStatusDraft, domain.PostStatusScheduled, domain.PostStatusFailed, domain.PostStatusCanceled:
		return true
	default:
		return false
	}
}

func canDeletePostStatus(status domain.PostStatus) bool {
	switch status {
	case domain.PostStatusDraft, domain.PostStatusScheduled, domain.PostStatusFailed, domain.PostStatusCanceled:
		return true
	default:
		return false
	}
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]any{"error": err.Error()})
}
