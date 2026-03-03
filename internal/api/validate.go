package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	postsapp "github.com/antoniolg/publisher/internal/application/posts"
	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
)

type validatePostResponse struct {
	Valid      bool           `json:"valid"`
	Normalized normalizedPost `json:"normalized"`
	Warnings   []string       `json:"warnings"`
}

type normalizedPost struct {
	AccountID   string              `json:"account_id"`
	Platform    string              `json:"platform"`
	Text        string              `json:"text"`
	ScheduledAt string              `json:"scheduled_at"`
	MediaIDs    []string            `json:"media_ids"`
	Segments    []normalizedSegment `json:"segments,omitempty"`
	MaxAttempts int                 `json:"max_attempts"`
}

type normalizedSegment struct {
	Text     string   `json:"text"`
	MediaIDs []string `json:"media_ids,omitempty"`
}

type validatePostInput struct {
	AccountID   string
	Text        string
	ScheduledAt string
	MediaIDs    []string
	Segments    []createPostSegment
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

	segments := normalizeRequestSegments(req.Segments)
	if len(segments) == 0 {
		text := strings.TrimSpace(req.Text)
		if text == "" {
			return validatePostResponse{}, errors.New("text is required")
		}
		segments = []createPostSegment{{
			Text:     text,
			MediaIDs: req.MediaIDs,
		}}
	}
	if len(segments) > postsapp.MaxThreadSegments {
		return validatePostResponse{}, fmt.Errorf("thread has too many segments (max %d)", postsapp.MaxThreadSegments)
	}
	if strings.TrimSpace(segments[0].Text) == "" {
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

	allMediaIDs := make([]string, 0)
	for _, segment := range segments {
		allMediaIDs = append(allMediaIDs, segment.MediaIDs...)
	}
	mediaItems, err := s.Store.GetMediaByIDs(ctx, allMediaIDs)
	if err != nil {
		return validatePostResponse{}, err
	}
	mediaByID := make(map[string]domain.Media, len(mediaItems))
	for _, media := range mediaItems {
		mediaByID[strings.TrimSpace(media.ID)] = media
	}

	maxAttempts := req.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = s.DefaultMaxRetries
		if maxAttempts <= 0 {
			maxAttempts = 3
		}
	}

	warnings := make([]string, 0)
	normalizedSegments := make([]normalizedSegment, 0, len(segments))
	for idx, segment := range segments {
		segmentMedia := make([]domain.Media, 0, len(segment.MediaIDs))
		normalizedMediaIDs := make([]string, 0, len(segment.MediaIDs))
		for _, rawID := range segment.MediaIDs {
			mediaID := strings.TrimSpace(rawID)
			if mediaID == "" {
				continue
			}
			media, ok := mediaByID[mediaID]
			if !ok {
				return validatePostResponse{}, fmt.Errorf("media not found: %s", mediaID)
			}
			segmentMedia = append(segmentMedia, media)
			normalizedMediaIDs = append(normalizedMediaIDs, mediaID)
		}
		if idx == 0 {
			stepWarnings, err := provider.ValidateDraft(ctx, account, publisher.Draft{Text: strings.TrimSpace(segment.Text), Media: segmentMedia})
			if err != nil {
				return validatePostResponse{}, err
			}
			warnings = append(warnings, stepWarnings...)
		} else {
			if err := validateFollowUpSegmentForPlatform(account.Platform, segmentMedia); err != nil {
				return validatePostResponse{}, err
			}
		}
		normalizedSegments = append(normalizedSegments, normalizedSegment{
			Text:     strings.TrimSpace(segment.Text),
			MediaIDs: normalizedMediaIDs,
		})
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
			Text:        strings.TrimSpace(segments[0].Text),
			ScheduledAt: normalizedScheduledAt,
			MediaIDs:    normalizedSegments[0].MediaIDs,
			Segments:    normalizedSegments,
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
		Segments:    req.Segments,
		MaxAttempts: req.MaxAttempts,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func validateFollowUpSegmentForPlatform(platform domain.Platform, media []domain.Media) error {
	switch platform {
	case domain.PlatformX:
		if len(media) > 4 {
			return fmt.Errorf("x thread replies support up to 4 media items")
		}
	case domain.PlatformLinkedIn:
		if len(media) > 0 {
			return fmt.Errorf("linkedin thread comments do not support media in this release")
		}
	case domain.PlatformFacebook:
		if len(media) > 0 {
			return fmt.Errorf("facebook thread comments do not support media in this release")
		}
	case domain.PlatformInstagram:
		if len(media) > 0 {
			return fmt.Errorf("instagram thread comments do not support media in this release")
		}
	default:
		if len(media) > 0 {
			return fmt.Errorf("thread follow-up media is not supported for platform %s", platform)
		}
	}
	return nil
}
