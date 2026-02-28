package posts

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
)

var (
	ErrPostIDRequired    = errors.New("post id is required")
	ErrScheduledAtNeeded = errors.New("scheduled_at is required to schedule")
)

type MutationsStore interface {
	CancelPost(ctx context.Context, id string) error
	DeletePostEditable(ctx context.Context, id string) error
	ScheduleDraftPost(ctx context.Context, id string, scheduledAt time.Time) error
	UpdatePostEditable(ctx context.Context, id, text string, scheduledAt time.Time) error
	GetPost(ctx context.Context, id string) (domain.Post, error)
}

type MutationsService struct {
	Store MutationsStore
}

type EditInput struct {
	PostID      string
	Text        string
	Intent      string
	ScheduledAt time.Time
}

func ResolveScheduledAtForEdit(intent string, scheduledAt time.Time, now func() time.Time) (time.Time, error) {
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
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return domain.Post{}, ErrTextRequired
	}
	scheduledAt, err := ResolveScheduledAtForEdit(in.Intent, in.ScheduledAt, now)
	if err != nil {
		return domain.Post{}, err
	}
	if err := s.Store.UpdatePostEditable(ctx, postID, text, scheduledAt); err != nil {
		return domain.Post{}, err
	}
	return s.Store.GetPost(ctx, postID)
}
