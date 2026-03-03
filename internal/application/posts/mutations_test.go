package posts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
)

type fakeMutationsStore struct {
	cancelID           string
	deleteID           string
	scheduleID         string
	scheduleAt         time.Time
	updateID           string
	updateText         string
	updateScheduledAt  time.Time
	updateMediaIDs     []string
	updateReplaceMedia bool
	updateThreadRoot   string
	updateThreadSteps  []db.ThreadStepUpdate
	post               domain.Post
	account            domain.SocialAccount
	mediaByID          map[string]domain.Media
	err                error
}

func (f *fakeMutationsStore) CancelPost(_ context.Context, id string) error {
	f.cancelID = id
	return f.err
}

func (f *fakeMutationsStore) DeletePostEditable(_ context.Context, id string) error {
	f.deleteID = id
	return f.err
}

func (f *fakeMutationsStore) ScheduleDraftPost(_ context.Context, id string, scheduledAt time.Time) error {
	f.scheduleID = id
	f.scheduleAt = scheduledAt
	return f.err
}

func (f *fakeMutationsStore) UpdatePostEditable(_ context.Context, id, text string, scheduledAt time.Time, mediaIDs []string, replaceMedia bool) error {
	f.updateID = id
	f.updateText = text
	f.updateScheduledAt = scheduledAt
	f.updateReplaceMedia = replaceMedia
	f.updateMediaIDs = append([]string(nil), mediaIDs...)
	return f.err
}

func (f *fakeMutationsStore) UpdateThreadEditable(_ context.Context, rootPostID string, steps []db.ThreadStepUpdate) error {
	f.updateThreadRoot = rootPostID
	f.updateThreadSteps = append([]db.ThreadStepUpdate(nil), steps...)
	return f.err
}

func (f *fakeMutationsStore) GetPost(_ context.Context, id string) (domain.Post, error) {
	if f.err != nil {
		return domain.Post{}, f.err
	}
	post := f.post
	post.ID = id
	return post, nil
}

func (f *fakeMutationsStore) GetAccount(_ context.Context, _ string) (domain.SocialAccount, error) {
	if f.err != nil {
		return domain.SocialAccount{}, f.err
	}
	return f.account, nil
}

func (f *fakeMutationsStore) GetMediaByIDs(_ context.Context, ids []string) ([]domain.Media, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]domain.Media, 0, len(ids))
	for _, id := range ids {
		item, ok := f.mediaByID[strings.TrimSpace(id)]
		if !ok {
			return nil, errors.New("media not found: " + strings.TrimSpace(id))
		}
		out = append(out, item)
	}
	return out, nil
}

func TestResolveScheduledAtForEdit(t *testing.T) {
	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	draftAt, err := ResolveScheduledAtForEdit("draft", now.Add(10*time.Minute), time.Time{}, nil)
	if err != nil {
		t.Fatalf("draft resolve failed: %v", err)
	}
	if !draftAt.IsZero() {
		t.Fatalf("expected zero scheduled_at for draft")
	}

	publishNowAt, err := ResolveScheduledAtForEdit("publish_now", time.Time{}, time.Time{}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("publish_now resolve failed: %v", err)
	}
	if !publishNowAt.Equal(now) {
		t.Fatalf("expected publish_now to default to now, got %s", publishNowAt)
	}

	_, err = ResolveScheduledAtForEdit("schedule", time.Time{}, time.Time{}, nil)
	if !errors.Is(err, ErrScheduledAtNeeded) {
		t.Fatalf("expected ErrScheduledAtNeeded, got %v", err)
	}

	explicit := now.Add(2 * time.Hour)
	scheduledAt, err := ResolveScheduledAtForEdit("schedule", explicit, time.Time{}, nil)
	if err != nil {
		t.Fatalf("schedule resolve failed: %v", err)
	}
	if !scheduledAt.Equal(explicit.UTC()) {
		t.Fatalf("expected explicit scheduled_at, got %s", scheduledAt)
	}

	preserved, err := ResolveScheduledAtForEdit("", time.Time{}, explicit, nil)
	if err != nil {
		t.Fatalf("preserve resolve failed: %v", err)
	}
	if !preserved.Equal(explicit.UTC()) {
		t.Fatalf("expected preserved scheduled_at, got %s", preserved)
	}
}

func TestMutationsServiceValidatePostID(t *testing.T) {
	svc := MutationsService{Store: &fakeMutationsStore{}}

	if err := svc.Cancel(t.Context(), " "); !errors.Is(err, ErrPostIDRequired) {
		t.Fatalf("expected ErrPostIDRequired in cancel, got %v", err)
	}
	if err := svc.DeleteEditable(t.Context(), " "); !errors.Is(err, ErrPostIDRequired) {
		t.Fatalf("expected ErrPostIDRequired in delete, got %v", err)
	}
	if _, err := svc.ScheduleDraft(t.Context(), " ", time.Now()); !errors.Is(err, ErrPostIDRequired) {
		t.Fatalf("expected ErrPostIDRequired in schedule, got %v", err)
	}
	if _, err := svc.UpdateEditable(t.Context(), EditInput{PostID: " ", Text: "hola"}, nil); !errors.Is(err, ErrPostIDRequired) {
		t.Fatalf("expected ErrPostIDRequired in update, got %v", err)
	}
}

func TestMutationsServiceScheduleAndUpdate(t *testing.T) {
	store := &fakeMutationsStore{
		post: domain.Post{
			ID:          "pst_1",
			AccountID:   "acc_1",
			Platform:    domain.PlatformX,
			Text:        "updated",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		},
		account: domain.SocialAccount{
			ID:       "acc_1",
			Platform: domain.PlatformX,
			Status:   domain.AccountStatusConnected,
		},
	}
	svc := MutationsService{Store: store}

	scheduledAt := time.Date(2026, 3, 1, 11, 0, 0, 0, time.UTC)
	post, err := svc.ScheduleDraft(t.Context(), "pst_1", scheduledAt)
	if err != nil {
		t.Fatalf("schedule failed: %v", err)
	}
	if store.scheduleID != "pst_1" {
		t.Fatalf("expected schedule id pst_1, got %q", store.scheduleID)
	}
	if !store.scheduleAt.Equal(scheduledAt) {
		t.Fatalf("expected schedule time %s, got %s", scheduledAt, store.scheduleAt)
	}
	if post.ID != "pst_1" {
		t.Fatalf("expected post id pst_1, got %q", post.ID)
	}

	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	post, err = svc.UpdateEditable(t.Context(), EditInput{
		PostID: "pst_1",
		Text:   "updated text",
		Intent: "publish_now",
	}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if store.updateID != "pst_1" {
		t.Fatalf("expected update id pst_1, got %q", store.updateID)
	}
	if !strings.Contains(store.updateText, "updated text") {
		t.Fatalf("expected update text to be propagated")
	}
	if !store.updateScheduledAt.Equal(now) {
		t.Fatalf("expected publish_now scheduled_at=%s, got %s", now, store.updateScheduledAt)
	}
	if post.ID != "pst_1" {
		t.Fatalf("expected updated post id pst_1, got %q", post.ID)
	}
}

func TestMutationsServiceUpdateEditablePreservesScheduledAtByDefault(t *testing.T) {
	originalScheduledAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store := &fakeMutationsStore{
		post: domain.Post{
			ID:          "pst_1",
			AccountID:   "acc_1",
			Platform:    domain.PlatformX,
			Text:        "scheduled",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: originalScheduledAt,
		},
		account: domain.SocialAccount{
			ID:       "acc_1",
			Platform: domain.PlatformX,
			Status:   domain.AccountStatusConnected,
		},
	}
	svc := MutationsService{Store: store}

	_, err := svc.UpdateEditable(t.Context(), EditInput{
		PostID: "pst_1",
		Text:   "updated text",
	}, nil)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !store.updateScheduledAt.Equal(originalScheduledAt) {
		t.Fatalf("expected scheduled_at to be preserved (%s), got %s", originalScheduledAt, store.updateScheduledAt)
	}
}

func TestMutationsServiceUpdateEditableReplacesMediaWhenRequested(t *testing.T) {
	store := &fakeMutationsStore{
		post: domain.Post{
			ID:        "pst_1",
			AccountID: "acc_li",
			Platform:  domain.PlatformLinkedIn,
			Status:    domain.PostStatusDraft,
			Text:      "old text",
		},
		account: domain.SocialAccount{
			ID:       "acc_li",
			Platform: domain.PlatformLinkedIn,
			Status:   domain.AccountStatusConnected,
		},
		mediaByID: map[string]domain.Media{
			"med_new": {ID: "med_new", MimeType: "image/png"},
		},
	}
	svc := MutationsService{
		Store: store,
		Registry: fakeRegistry{
			providers: map[domain.Platform]publisher.Provider{
				domain.PlatformLinkedIn: publisher.NewLinkedInProvider(publisher.LinkedInProviderConfig{}),
			},
		},
	}

	_, err := svc.UpdateEditable(t.Context(), EditInput{
		PostID:       "pst_1",
		Text:         "new text",
		MediaIDs:     []string{"med_new"},
		ReplaceMedia: true,
	}, nil)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !store.updateReplaceMedia {
		t.Fatalf("expected update to replace media")
	}
	if len(store.updateMediaIDs) != 1 || store.updateMediaIDs[0] != "med_new" {
		t.Fatalf("expected media replacement with med_new, got %#v", store.updateMediaIDs)
	}
}

func TestMutationsServiceUpdateEditableAllowsClearingMediaWhenPlatformAllowsIt(t *testing.T) {
	store := &fakeMutationsStore{
		post: domain.Post{
			ID:        "pst_1",
			AccountID: "acc_li",
			Platform:  domain.PlatformLinkedIn,
			Status:    domain.PostStatusScheduled,
			Text:      "old text",
			Media: []domain.Media{
				{ID: "med_old", MimeType: "image/png"},
			},
		},
		account: domain.SocialAccount{
			ID:       "acc_li",
			Platform: domain.PlatformLinkedIn,
			Status:   domain.AccountStatusConnected,
		},
	}
	svc := MutationsService{
		Store: store,
		Registry: fakeRegistry{
			providers: map[domain.Platform]publisher.Provider{
				domain.PlatformLinkedIn: publisher.NewLinkedInProvider(publisher.LinkedInProviderConfig{}),
			},
		},
	}

	_, err := svc.UpdateEditable(t.Context(), EditInput{
		PostID:       "pst_1",
		Text:         "new text",
		MediaIDs:     []string{},
		ReplaceMedia: true,
	}, nil)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !store.updateReplaceMedia {
		t.Fatalf("expected update to replace media")
	}
	if len(store.updateMediaIDs) != 0 {
		t.Fatalf("expected media to be cleared, got %#v", store.updateMediaIDs)
	}
}

func TestMutationsServiceUpdateEditableRejectsInstagramWithoutMedia(t *testing.T) {
	store := &fakeMutationsStore{
		post: domain.Post{
			ID:        "pst_ig",
			AccountID: "acc_ig",
			Platform:  domain.PlatformInstagram,
			Status:    domain.PostStatusScheduled,
			Text:      "caption",
			Media: []domain.Media{
				{ID: "med_ig", MimeType: "image/png"},
			},
		},
		account: domain.SocialAccount{
			ID:       "acc_ig",
			Platform: domain.PlatformInstagram,
			Status:   domain.AccountStatusConnected,
		},
	}
	svc := MutationsService{
		Store: store,
		Registry: fakeRegistry{
			providers: map[domain.Platform]publisher.Provider{
				domain.PlatformInstagram: publisher.NewInstagramProvider(publisher.MetaProviderConfig{}),
			},
		},
	}

	_, err := svc.UpdateEditable(t.Context(), EditInput{
		PostID:       "pst_ig",
		Text:         "caption updated",
		MediaIDs:     []string{},
		ReplaceMedia: true,
	}, nil)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !IsValidationError(err) {
		t.Fatalf("expected validation error wrapper, got %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "instagram") || !strings.Contains(strings.ToLower(err.Error()), "media") {
		t.Fatalf("expected instagram media validation error, got %v", err)
	}
	if store.updateID != "" {
		t.Fatalf("expected no update call when validation fails")
	}
}

func TestMutationsServiceUpdateValidatesTextAndSchedule(t *testing.T) {
	svc := MutationsService{Store: &fakeMutationsStore{}}

	_, err := svc.UpdateEditable(t.Context(), EditInput{
		PostID: "pst_1",
		Text:   " ",
	}, nil)
	if !errors.Is(err, ErrTextRequired) {
		t.Fatalf("expected ErrTextRequired, got %v", err)
	}

	_, err = svc.UpdateEditable(t.Context(), EditInput{
		PostID: "pst_1",
		Text:   "hola",
		Intent: "schedule",
	}, nil)
	if !errors.Is(err, ErrScheduledAtNeeded) {
		t.Fatalf("expected ErrScheduledAtNeeded, got %v", err)
	}
}

func TestMutationsServiceUpdateRejectsTooManySegments(t *testing.T) {
	store := &fakeMutationsStore{}
	svc := MutationsService{Store: store}

	segments := make([]ThreadSegmentInput, 0, MaxThreadSegments+1)
	for i := 0; i < MaxThreadSegments+1; i++ {
		segments = append(segments, ThreadSegmentInput{Text: "segment"})
	}

	_, err := svc.UpdateEditable(t.Context(), EditInput{
		PostID:   "pst_1",
		Segments: segments,
	}, nil)
	if !errors.Is(err, ErrThreadTooLong) {
		t.Fatalf("expected ErrThreadTooLong, got %v", err)
	}
}
