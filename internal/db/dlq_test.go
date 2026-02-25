package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
)

func TestRequeueDeadLetter(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	created, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			Platform:    domain.PlatformX,
			Text:        "needs requeue",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(-1 * time.Minute),
			MaxAttempts: 1,
		},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	claimed, err := store.ClaimDuePosts(ctx, 1)
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected 1 claimed, got %d", len(claimed))
	}

	if err := store.RecordPublishFailure(ctx, created.Post.ID, errors.New("hard fail"), time.Second); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	dlqItems, err := store.ListDeadLetters(ctx, 10)
	if err != nil {
		t.Fatalf("list dead letters: %v", err)
	}
	if len(dlqItems) != 1 {
		t.Fatalf("expected 1 dead letter, got %d", len(dlqItems))
	}

	requeuedPost, err := store.RequeueDeadLetter(ctx, dlqItems[0].ID)
	if err != nil {
		t.Fatalf("requeue dead letter: %v", err)
	}
	if requeuedPost.Status != domain.PostStatusScheduled {
		t.Fatalf("expected post status scheduled, got %s", requeuedPost.Status)
	}
	if requeuedPost.Attempts != 0 {
		t.Fatalf("expected attempts reset to 0, got %d", requeuedPost.Attempts)
	}

	dlqItems, err = store.ListDeadLetters(ctx, 10)
	if err != nil {
		t.Fatalf("list dead letters after requeue: %v", err)
	}
	if len(dlqItems) != 0 {
		t.Fatalf("expected 0 dead letters after requeue, got %d", len(dlqItems))
	}
}
