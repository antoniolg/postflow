package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
)

type validatePostResponse struct {
	Valid      bool           `json:"valid"`
	Normalized normalizedPost `json:"normalized"`
	Warnings   []string       `json:"warnings"`
}

type normalizedPost struct {
	AccountID   string   `json:"account_id"`
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
	if strings.TrimSpace(req.AccountID) == "" {
		writeError(w, http.StatusBadRequest, errors.New("account_id is required"))
		return
	}

	account, err := s.resolveTargetAccount(r.Context(), req.AccountID)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("account not found"))
		return
	}
	if account.Status != domain.AccountStatusConnected {
		writeError(w, http.StatusBadRequest, errors.New("account is not connected"))
		return
	}
	provider, ok := s.providerRegistry().Get(account.Platform)
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("provider is not configured for account platform"))
		return
	}

	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}

	var scheduledAt time.Time
	if strings.TrimSpace(req.ScheduledAt) != "" {
		parsed, err := time.Parse(time.RFC3339, req.ScheduledAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("scheduled_at must be RFC3339: %w", err))
			return
		}
		scheduledAt = parsed
	}

	mediaItems, err := s.Store.GetMediaByIDs(r.Context(), req.MediaIDs)
	if err != nil {
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

	warnings, err := provider.ValidateDraft(r.Context(), account, publisher.Draft{Text: text, Media: mediaItems})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if scheduledAt.IsZero() {
		warnings = append(warnings, "draft mode: no scheduled_at provided")
	}
	if !scheduledAt.IsZero() && scheduledAt.UTC().Before(time.Now().UTC()) {
		warnings = append(warnings, "scheduled_at is in the past; post may publish immediately")
	}

	normalizedScheduledAt := ""
	if !scheduledAt.IsZero() {
		normalizedScheduledAt = scheduledAt.UTC().Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, validatePostResponse{
		Valid: true,
		Normalized: normalizedPost{
			AccountID:   account.ID,
			Platform:    string(account.Platform),
			Text:        text,
			ScheduledAt: normalizedScheduledAt,
			MediaIDs:    req.MediaIDs,
			MaxAttempts: maxAttempts,
		},
		Warnings: warnings,
	})
}
