package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
)

type validatePostResponse struct {
	Valid      bool           `json:"valid"`
	Normalized normalizedPost `json:"normalized"`
	Warnings   []string       `json:"warnings"`
}

type normalizedPost struct {
	Platform    string   `json:"platform"`
	Text        string   `json:"text"`
	ScheduledAt string   `json:"scheduled_at"`
	MediaIDs    []string `json:"media_ids"`
	MaxAttempts int      `json:"max_attempts"`
}

func (s Server) handleValidatePost(w http.ResponseWriter, r *http.Request) {
	var req createPostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json body: %w", err))
		return
	}

	platform := domain.Platform(strings.ToLower(strings.TrimSpace(req.Platform)))
	if platform == "" {
		platform = domain.PlatformX
	}
	if platform != domain.PlatformX {
		writeError(w, http.StatusBadRequest, errors.New("only platform 'x' is supported in this MVP"))
		return
	}

	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}

	scheduledAt, err := time.Parse(time.RFC3339, req.ScheduledAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("scheduled_at must be RFC3339: %w", err))
		return
	}

	if _, err := s.Store.GetMediaByIDs(r.Context(), req.MediaIDs); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	maxAttempts := req.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = s.DefaultMaxRetries
		if maxAttempts <= 0 {
			maxAttempts = 3
		}
	}

	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) > 128 {
		writeError(w, http.StatusBadRequest, errors.New("Idempotency-Key too long (max 128 chars)"))
		return
	}

	warnings := make([]string, 0)
	if scheduledAt.UTC().Before(time.Now().UTC()) {
		warnings = append(warnings, "scheduled_at is in the past; post may publish immediately")
	}

	writeJSON(w, http.StatusOK, validatePostResponse{
		Valid: true,
		Normalized: normalizedPost{
			Platform:    string(platform),
			Text:        text,
			ScheduledAt: scheduledAt.UTC().Format(time.RFC3339),
			MediaIDs:    req.MediaIDs,
			MaxAttempts: maxAttempts,
		},
		Warnings: warnings,
	})
}
