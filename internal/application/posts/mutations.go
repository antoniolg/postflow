package posts

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/application/ports"
	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

var (
	ErrPostIDRequired    = errors.New("post id is required")
	ErrScheduledAtNeeded = errors.New("scheduled_at is required to schedule")
)

type MutationsStore interface {
	CancelPost(ctx context.Context, id string) error
	DeletePostEditable(ctx context.Context, id string) error
	ScheduleDraftPost(ctx context.Context, id string, scheduledAt time.Time) error
	UpdatePostEditable(ctx context.Context, id, text string, scheduledAt time.Time, mediaIDs []string, replaceMedia bool) error
	UpdateThreadEditable(ctx context.Context, rootPostID string, steps []db.ThreadStepUpdate) error
	GetPost(ctx context.Context, id string) (domain.Post, error)
	GetAccount(ctx context.Context, id string) (domain.SocialAccount, error)
	GetMediaByIDs(ctx context.Context, ids []string) ([]domain.Media, error)
}

type MutationsService struct {
	Store    MutationsStore
	Registry ports.ProviderRegistry
}

type EditInput struct {
	PostID       string
	Text         string
	Intent       string
	ScheduledAt  time.Time
	MediaIDs     []string
	ReplaceMedia bool
	Segments     []ThreadSegmentInput
}

func ResolveScheduledAtForEdit(intent string, scheduledAt time.Time, currentScheduledAt time.Time, now func() time.Time) (time.Time, error) {
	intent = strings.ToLower(strings.TrimSpace(intent))
	switch intent {
	case "draft":
		return time.Time{}, nil
	case "publish_now":
		if scheduledAt.IsZero() {
			if now == nil {
				now = time.Now
			}
			return now().UTC(), nil
		}
	case "schedule":
		if scheduledAt.IsZero() {
			return time.Time{}, ErrScheduledAtNeeded
		}
	}
	if scheduledAt.IsZero() {
		if intent == "" {
			if currentScheduledAt.IsZero() {
				return time.Time{}, nil
			}
			return currentScheduledAt.UTC(), nil
		}
		return time.Time{}, nil
	}
	return scheduledAt.UTC(), nil
}

func (s MutationsService) Cancel(ctx context.Context, postID string) error {
	postID = strings.TrimSpace(postID)
	if postID == "" {
		return ErrPostIDRequired
	}
	return s.Store.CancelPost(ctx, postID)
}

func (s MutationsService) DeleteEditable(ctx context.Context, postID string) error {
	postID = strings.TrimSpace(postID)
	if postID == "" {
		return ErrPostIDRequired
	}
	return s.Store.DeletePostEditable(ctx, postID)
}

func (s MutationsService) ScheduleDraft(ctx context.Context, postID string, scheduledAt time.Time) (domain.Post, error) {
	postID = strings.TrimSpace(postID)
	if postID == "" {
		return domain.Post{}, ErrPostIDRequired
	}
	if scheduledAt.IsZero() {
		return domain.Post{}, ErrScheduledAtNeeded
	}
	if err := s.Store.ScheduleDraftPost(ctx, postID, scheduledAt.UTC()); err != nil {
		return domain.Post{}, err
	}
	return s.Store.GetPost(ctx, postID)
}

func (s MutationsService) UpdateEditable(ctx context.Context, in EditInput, now func() time.Time) (domain.Post, error) {
	postID := strings.TrimSpace(in.PostID)
	if postID == "" {
		return domain.Post{}, ErrPostIDRequired
	}
	current, err := s.Store.GetPost(ctx, postID)
	if err != nil {
		return domain.Post{}, err
	}
	scheduledAt, err := ResolveScheduledAtForEdit(in.Intent, in.ScheduledAt, current.ScheduledAt, now)
	if err != nil {
		return domain.Post{}, err
	}

	if len(in.Segments) > 0 {
		if len(in.Segments) > MaxThreadSegments {
			return domain.Post{}, ErrThreadTooLong
		}
		steps := make([]db.ThreadStepUpdate, 0, len(in.Segments))
		for _, segment := range in.Segments {
			text := strings.TrimSpace(segment.Text)
			if text == "" {
				return domain.Post{}, ErrTextRequired
			}
			steps = append(steps, db.ThreadStepUpdate{
				Text:        text,
				ScheduledAt: scheduledAt,
				MediaIDs:    normalizeMediaIDs(segment.MediaIDs),
			})
		}
		rootID := strings.TrimSpace(current.ID)
		if current.RootPostID != nil && strings.TrimSpace(*current.RootPostID) != "" {
			rootID = strings.TrimSpace(*current.RootPostID)
		}
		if err := s.Store.UpdateThreadEditable(ctx, rootID, steps); err != nil {
			return domain.Post{}, err
		}
		return s.Store.GetPost(ctx, rootID)
	}

	text := strings.TrimSpace(in.Text)
	if text == "" {
		return domain.Post{}, ErrTextRequired
	}

	mediaIDs := mediaIDsFromPost(current.Media)
	if in.ReplaceMedia {
		mediaIDs = normalizeMediaIDs(in.MediaIDs)
	}
	if err := s.validateEditableDraft(ctx, current, text, mediaIDs, in.ReplaceMedia); err != nil {
		return domain.Post{}, err
	}

	if err := s.Store.UpdatePostEditable(ctx, postID, text, scheduledAt, mediaIDs, in.ReplaceMedia); err != nil {
		return domain.Post{}, err
	}
	return s.Store.GetPost(ctx, postID)
}

func (s MutationsService) validateEditableDraft(ctx context.Context, post domain.Post, text string, mediaIDs []string, replaceMedia bool) error {
	if s.Registry == nil {
		return nil
	}

	account, err := s.Store.GetAccount(ctx, strings.TrimSpace(post.AccountID))
	if err != nil {
		return ValidationError{Err: ErrAccountNotFound}
	}
	provider, ok := s.Registry.Get(account.Platform)
	if !ok {
		return ValidationError{Err: ErrProviderNotConfigured}
	}

	media := post.Media
	if replaceMedia {
		media, err = s.Store.GetMediaByIDs(ctx, mediaIDs)
		if err != nil {
			return ValidationError{Err: err}
		}
	}
	if _, err := provider.ValidateDraft(ctx, account, postflow.Draft{
		Text:  text,
		Media: media,
	}); err != nil {
		return ValidationError{Err: err}
	}
	return nil
}

func mediaIDsFromPost(media []domain.Media) []string {
	if len(media) == 0 {
		return nil
	}
	out := make([]string, 0, len(media))
	for _, item := range media {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	return out
}
