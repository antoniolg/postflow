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
	PostIDs      []string
	Text         string
	Intent       string
	ScheduledAt  time.Time
	MediaIDs     []string
	ReplaceMedia bool
	Segments     []ThreadSegmentInput
}

type preparedEdit struct {
	postID       string
	resultPostID string
	text         string
	scheduledAt  time.Time
	mediaIDs     []string
	replaceMedia bool
	steps        []db.ThreadStepUpdate
}

func ResolveScheduledAtForEdit(intent string, scheduledAt time.Time, currentScheduledAt time.Time, now func() time.Time) (time.Time, error) {
	intent = strings.ToLower(strings.TrimSpace(intent))
	switch intent {
	case "draft":
		return time.Time{}, nil
	case "publish_now":
		if now == nil {
			now = time.Now
		}
		return now().UTC(), nil
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
	posts, err := s.UpdateEditableMany(ctx, in, now)
	if err != nil {
		return domain.Post{}, err
	}
	if len(posts) == 0 {
		return domain.Post{}, ErrPostIDRequired
	}
	return posts[0], nil
}

func (s MutationsService) UpdateEditableMany(ctx context.Context, in EditInput, now func() time.Time) ([]domain.Post, error) {
	postIDs := normalizeEditablePostIDs(in.PostID, in.PostIDs)
	if len(postIDs) == 0 {
		return nil, ErrPostIDRequired
	}

	prepared := make([]preparedEdit, 0, len(postIDs))
	for _, postID := range postIDs {
		item, err := s.prepareEditableUpdate(ctx, postID, in, now)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, item)
	}

	updated := make([]domain.Post, 0, len(prepared))
	for _, item := range prepared {
		if len(item.steps) > 0 {
			if err := s.Store.UpdateThreadEditable(ctx, item.resultPostID, item.steps); err != nil {
				return nil, err
			}
			post, err := s.Store.GetPost(ctx, item.resultPostID)
			if err != nil {
				return nil, err
			}
			updated = append(updated, post)
			continue
		}

		if err := s.Store.UpdatePostEditable(ctx, item.postID, item.text, item.scheduledAt, item.mediaIDs, item.replaceMedia); err != nil {
			return nil, err
		}
		post, err := s.Store.GetPost(ctx, item.resultPostID)
		if err != nil {
			return nil, err
		}
		updated = append(updated, post)
	}

	return updated, nil
}

func (s MutationsService) prepareEditableUpdate(ctx context.Context, postID string, in EditInput, now func() time.Time) (preparedEdit, error) {
	postID = strings.TrimSpace(postID)
	if postID == "" {
		return preparedEdit{}, ErrPostIDRequired
	}
	current, err := s.Store.GetPost(ctx, postID)
	if err != nil {
		return preparedEdit{}, err
	}
	scheduledAt, err := ResolveScheduledAtForEdit(in.Intent, in.ScheduledAt, current.ScheduledAt, now)
	if err != nil {
		return preparedEdit{}, err
	}

	if len(in.Segments) > 0 {
		if len(in.Segments) > MaxThreadSegments {
			return preparedEdit{}, ErrThreadTooLong
		}
		if err := s.validateEditableThread(ctx, current, in.Segments); err != nil {
			return preparedEdit{}, err
		}
		steps := make([]db.ThreadStepUpdate, 0, len(in.Segments))
		for _, segment := range in.Segments {
			text := strings.TrimSpace(segment.Text)
			if text == "" {
				return preparedEdit{}, ErrTextRequired
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
		return preparedEdit{
			postID:       postID,
			resultPostID: rootID,
			scheduledAt:  scheduledAt,
			steps:        steps,
		}, nil
	}

	text := strings.TrimSpace(in.Text)
	if text == "" {
		return preparedEdit{}, ErrTextRequired
	}

	mediaIDs := mediaIDsFromPost(current.Media)
	if in.ReplaceMedia {
		mediaIDs = normalizeMediaIDs(in.MediaIDs)
	}
	if err := s.validateEditableDraft(ctx, current, text, mediaIDs, in.ReplaceMedia); err != nil {
		return preparedEdit{}, err
	}

	return preparedEdit{
		postID:       postID,
		resultPostID: postID,
		text:         text,
		scheduledAt:  scheduledAt,
		mediaIDs:     mediaIDs,
		replaceMedia: in.ReplaceMedia,
	}, nil
}

func normalizeEditablePostIDs(primary string, many []string) []string {
	seen := make(map[string]struct{}, len(many)+1)
	out := make([]string, 0, len(many)+1)
	add := func(raw string) {
		id := strings.TrimSpace(raw)
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range many {
		add(id)
	}
	add(primary)
	return out
}

func (s MutationsService) validateEditableThread(ctx context.Context, post domain.Post, segments []ThreadSegmentInput) error {
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
	if err := validateThreadSegmentsForAccount(ctx, s.Store, provider, account, segments); err != nil {
		return ValidationError{Err: err}
	}
	return nil
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
