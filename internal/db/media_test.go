package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
)

func TestListMediaIncludesUsageCount(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformX)
	mediaA, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "a.png",
		StoragePath:  "/tmp/a.png",
		MimeType:     "image/png",
		SizeBytes:    123,
	})
	if err != nil {
		t.Fatalf("create media A: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	mediaB, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "b.png",
		StoragePath:  "/tmp/b.png",
		MimeType:     "image/png",
		SizeBytes:    456,
	})
	if err != nil {
		t.Fatalf("create media B: %v", err)
	}

	_, err = store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "post with media",
			Status:      domain.PostStatusDraft,
			MaxAttempts: 3,
		},
		MediaIDs: []string{mediaA.ID},
	})
	if err != nil {
		t.Fatalf("create post with media: %v", err)
	}

	items, err := store.ListMedia(ctx, 10)
	if err != nil {
		t.Fatalf("list media: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 media items, got %d", len(items))
	}

	usageByID := map[string]int{}
	for _, item := range items {
		usageByID[item.Media.ID] = item.UsageCount
	}
	if got := usageByID[mediaA.ID]; got != 1 {
		t.Fatalf("expected usage_count=1 for media A, got %d", got)
	}
	if got := usageByID[mediaB.ID]; got != 0 {
		t.Fatalf("expected usage_count=0 for media B, got %d", got)
	}
}

func TestDeleteMediaIfUnused(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	created, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "preview.png",
		StoragePath:  "/tmp/preview.png",
		MimeType:     "image/png",
		SizeBytes:    1234,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	deleted, err := store.DeleteMediaIfUnused(ctx, created.ID)
	if err != nil {
		t.Fatalf("delete media: %v", err)
	}
	if deleted.ID != created.ID {
		t.Fatalf("expected deleted media id %q, got %q", created.ID, deleted.ID)
	}

	items, err := store.ListMedia(ctx, 10)
	if err != nil {
		t.Fatalf("list media: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected media list to be empty after delete, got %d", len(items))
	}
}

func TestDeleteMediaIfUnusedRejectsInUse(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	created, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "preview.png",
		StoragePath:  "/tmp/preview.png",
		MimeType:     "image/png",
		SizeBytes:    1234,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	_, err = store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   createTestAccount(t, store, domain.PlatformX).ID,
			Text:        "post with media",
			Status:      domain.PostStatusDraft,
			MaxAttempts: 3,
		},
		MediaIDs: []string{created.ID},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	_, err = store.DeleteMediaIfUnused(ctx, created.ID)
	if err == nil {
		t.Fatalf("expected delete to fail for in-use media")
	}
	if !errors.Is(err, ErrMediaInUse) {
		t.Fatalf("expected ErrMediaInUse, got %v", err)
	}
}
