package posts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
)

type fakeMutationsStore struct {
	cancelID          string
	deleteID          string
	scheduleID        string
	scheduleAt        time.Time
	updateID          string
	updateText        string
	updateScheduledAt time.Time
	post              domain.Post
	err               error
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

func (f *fakeMutationsStore) UpdatePostEditable(_ context.Context, id, text string, scheduledAt time.Time) error {
	f.updateID = id
	f.updateText = text
	f.updateScheduledAt = scheduledAt
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

func TestResolveScheduledAtForEdit(t *testing.T) {
	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	draftAt, err := ResolveScheduledAtForEdit("draft", now.Add(10*time.Minute), nil)
	if err != nil {
		t.Fatalf("draft resolve failed: %v", err)
	}
	if !draftAt.IsZero() {
		t.Fatalf("expected zero scheduled_at for draft")
	}

	publishNowAt, err := ResolveScheduledAtForEdit("publish_now", time.Time{}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("publish_now resolve failed: %v", err)
	}
	if !publishNowAt.Equal(now) {
		t.Fatalf("expected publish_now to default to now, got %s", publishNowAt)
	}

	_, err = ResolveScheduledAtForEdit("schedule", time.Time{}, nil)
	if !errors.Is(err, ErrScheduledAtNeeded) {
		t.Fatalf("expected ErrScheduledAtNeeded, got %v", err)
	}

	explicit := now.Add(2 * time.Hour)
	scheduledAt, err := ResolveScheduledAtForEdit("schedule", explicit, nil)
	if err != nil {
		t.Fatalf("schedule resolve failed: %v", err)
	}
	if !scheduledAt.Equal(explicit.UTC()) {
		t.Fatalf("expected explicit scheduled_at, got %s", scheduledAt)
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
			Text:        "updated",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
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
