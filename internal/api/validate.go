package api

import (
	"context"
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

type validatePostInput struct {
	AccountID   string
	Text        string
	ScheduledAt string
	MediaIDs    []string
	MaxAttempts int
}

func (s Server) validatePost(ctx context.Context, req validatePostInput) (validatePostResponse, error) {
	if strings.TrimSpace(req.AccountID) == "" {
		return validatePostResponse{}, errors.New("account_id is required")
	}

	account, err := s.resolveTargetAccount(ctx, req.AccountID)
	if err != nil {
		return validatePostResponse{}, errors.New("account not found")
	}
	if account.Status != domain.AccountStatusConnected {
		return validatePostResponse{}, errors.New("account is not connected")
	}
	provider, ok := s.providerRegistry().Get(account.Platform)
	if !ok {
		return validatePostResponse{}, errors.New("provider is not configured for account platform")
	}

	text := strings.TrimSpace(req.Text)
	if text == "" {
		return validatePostResponse{}, errors.New("text is required")
	}

	var scheduledAt time.Time
	if strings.TrimSpace(req.ScheduledAt) != "" {
		parsed, err := time.Parse(time.RFC3339, req.ScheduledAt)
		if err != nil {
			return validatePostResponse{}, fmt.Errorf("scheduled_at must be RFC3339: %w", err)
		}
		scheduledAt = parsed
	}

	mediaItems, err := s.Store.GetMediaByIDs(ctx, req.MediaIDs)
	if err != nil {
		return validatePostResponse{}, err
	}

	maxAttempts := req.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = s.DefaultMaxRetries
		if maxAttempts <= 0 {
			maxAttempts = 3
		}
	}

	warnings, err := provider.ValidateDraft(ctx, account, publisher.Draft{Text: text, Media: mediaItems})
	if err != nil {
		return validatePostResponse{}, err
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

	return validatePostResponse{
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
	}, nil
}

func (s Server) handleValidatePost(w http.ResponseWriter, r *http.Request) {
	var req createPostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json body: %w", err))
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) > 128 {
		writeError(w, http.StatusBadRequest, errors.New("Idempotency-Key too long (max 128 chars)"))
		return
	}
	out, err := s.validatePost(r.Context(), validatePostInput{
		AccountID:   req.AccountID,
		Text:        req.Text,
		ScheduledAt: req.ScheduledAt,
		MediaIDs:    req.MediaIDs,
		MaxAttempts: req.MaxAttempts,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
