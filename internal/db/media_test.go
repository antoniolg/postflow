package db

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
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
	mediaC, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "c.png",
		StoragePath:  "/tmp/c.png",
		MimeType:     "image/png",
		SizeBytes:    789,
	})
	if err != nil {
		t.Fatalf("create media C: %v", err)
	}
	mediaD, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "d.png",
		StoragePath:  "/tmp/d.png",
		MimeType:     "image/png",
		SizeBytes:    999,
	})
	if err != nil {
		t.Fatalf("create media D: %v", err)
	}
	mediaE, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "e.png",
		StoragePath:  "/tmp/e.png",
		MimeType:     "image/png",
		SizeBytes:    555,
	})
	if err != nil {
		t.Fatalf("create media E: %v", err)
	}
	mediaF, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "f.png",
		StoragePath:  "/tmp/f.png",
		MimeType:     "image/png",
		SizeBytes:    666,
	})
	if err != nil {
		t.Fatalf("create media F: %v", err)
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
	_, err = store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "published with media",
			Status:      domain.PostStatusPublished,
			MaxAttempts: 3,
		},
		MediaIDs: []string{mediaC.ID},
	})
	if err != nil {
		t.Fatalf("create published post with media: %v", err)
	}
	_, err = store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "scheduled future with media",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().Add(2 * time.Hour),
			MaxAttempts: 3,
		},
		MediaIDs: []string{mediaD.ID},
	})
	if err != nil {
		t.Fatalf("create scheduled future post with media: %v", err)
	}
	_, err = store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "failed with media",
			Status:      domain.PostStatusFailed,
			MaxAttempts: 3,
		},
		MediaIDs: []string{mediaE.ID},
	})
	if err != nil {
		t.Fatalf("create failed post with media: %v", err)
	}
	_, err = store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "canceled with media",
			Status:      domain.PostStatusCanceled,
			MaxAttempts: 3,
		},
		MediaIDs: []string{mediaF.ID},
	})
	if err != nil {
		t.Fatalf("create canceled post with media: %v", err)
	}

	items, err := store.ListMedia(ctx, 10)
	if err != nil {
		t.Fatalf("list media: %v", err)
	}
	if len(items) != 6 {
		t.Fatalf("expected 6 media items, got %d", len(items))
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
	if got := usageByID[mediaC.ID]; got != 0 {
		t.Fatalf("expected usage_count=0 for media C because published-only uses are not future/draft, got %d", got)
	}
	if got := usageByID[mediaD.ID]; got != 1 {
		t.Fatalf("expected usage_count=1 for media D because future scheduled uses count as in-use, got %d", got)
	}
	if got := usageByID[mediaE.ID]; got != 1 {
		t.Fatalf("expected usage_count=1 for media E because failed uses block deletion, got %d", got)
	}
	if got := usageByID[mediaF.ID]; got != 0 {
		t.Fatalf("expected usage_count=0 for media F because canceled uses do not block deletion, got %d", got)
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

func TestDeleteMediaIfUnusedAllowsPublishedOnlyReferences(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	created, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "published-only.png",
		StoragePath:  "/tmp/published-only.png",
		MimeType:     "image/png",
		SizeBytes:    1234,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	_, err = store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   createTestAccount(t, store, domain.PlatformX).ID,
			Text:        "published with media",
			Status:      domain.PostStatusPublished,
			MaxAttempts: 3,
		},
		MediaIDs: []string{created.ID},
	})
	if err != nil {
		t.Fatalf("create published post: %v", err)
	}

	deleted, err := store.DeleteMediaIfUnused(ctx, created.ID)
	if err != nil {
		t.Fatalf("expected published-only media to be deletable, got %v", err)
	}
	if deleted.ID != created.ID {
		t.Fatalf("expected deleted media id %q, got %q", created.ID, deleted.ID)
	}
}

func TestDeleteMediaIfUnusedAllowsCanceledOnlyReferences(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	created, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "canceled-only.png",
		StoragePath:  "/tmp/canceled-only.png",
		MimeType:     "image/png",
		SizeBytes:    1234,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	_, err = store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   createTestAccount(t, store, domain.PlatformX).ID,
			Text:        "canceled with media",
			Status:      domain.PostStatusCanceled,
			MaxAttempts: 3,
		},
		MediaIDs: []string{created.ID},
	})
	if err != nil {
		t.Fatalf("create canceled post: %v", err)
	}

	deleted, err := store.DeleteMediaIfUnused(ctx, created.ID)
	if err != nil {
		t.Fatalf("expected canceled-only media to be deletable, got %v", err)
	}
	if deleted.ID != created.ID {
		t.Fatalf("expected deleted media id %q, got %q", created.ID, deleted.ID)
	}
}

func TestDeleteMediaIfUnusedRejectsFutureScheduledReferences(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	created, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "future-scheduled.png",
		StoragePath:  "/tmp/future-scheduled.png",
		MimeType:     "image/png",
		SizeBytes:    1234,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	_, err = store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   createTestAccount(t, store, domain.PlatformX).ID,
			Text:        "future scheduled with media",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().Add(3 * time.Hour),
			MaxAttempts: 3,
		},
		MediaIDs: []string{created.ID},
	})
	if err != nil {
		t.Fatalf("create scheduled post: %v", err)
	}

	_, err = store.DeleteMediaIfUnused(ctx, created.ID)
	if err == nil {
		t.Fatalf("expected delete to fail for future scheduled media")
	}
	if !errors.Is(err, ErrMediaInUse) {
		t.Fatalf("expected ErrMediaInUse, got %v", err)
	}
}

func TestDeleteMediaUnusedByPendingPosts(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformX)
	makeMedia := func(name string) domain.Media {
		media, err := store.CreateMedia(ctx, domain.Media{
			Kind:         "image",
			OriginalName: name,
			StoragePath:  "/tmp/" + name,
			MimeType:     "image/png",
			SizeBytes:    100,
		})
		if err != nil {
			t.Fatalf("create media %s: %v", name, err)
		}
		return media
	}

	orphan := makeMedia("orphan.png")
	publishedOnly := makeMedia("published-only.png")
	canceledOnly := makeMedia("canceled-only.png")
	draftOnly := makeMedia("draft-only.png")
	scheduledFutureOnly := makeMedia("scheduled-future-only.png")
	scheduledPastOnly := makeMedia("scheduled-past-only.png")
	failedOnly := makeMedia("failed-only.png")
	mixedPublishedAndDraft := makeMedia("mixed.png")

	if _, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "published post",
			Status:      domain.PostStatusPublished,
			MaxAttempts: 3,
		},
		MediaIDs: []string{publishedOnly.ID, mixedPublishedAndDraft.ID},
	}); err != nil {
		t.Fatalf("create published post: %v", err)
	}
	if _, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "canceled post",
			Status:      domain.PostStatusCanceled,
			MaxAttempts: 3,
		},
		MediaIDs: []string{canceledOnly.ID},
	}); err != nil {
		t.Fatalf("create canceled post: %v", err)
	}
	if _, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "draft post",
			Status:      domain.PostStatusDraft,
			MaxAttempts: 3,
		},
		MediaIDs: []string{draftOnly.ID, mixedPublishedAndDraft.ID},
	}); err != nil {
		t.Fatalf("create draft post: %v", err)
	}
	if _, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "scheduled future post",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().Add(4 * time.Hour),
			MaxAttempts: 3,
		},
		MediaIDs: []string{scheduledFutureOnly.ID},
	}); err != nil {
		t.Fatalf("create scheduled future post: %v", err)
	}
	if _, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "scheduled past post",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().Add(-4 * time.Hour),
			MaxAttempts: 3,
		},
		MediaIDs: []string{scheduledPastOnly.ID},
	}); err != nil {
		t.Fatalf("create scheduled past post: %v", err)
	}
	if _, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "failed post",
			Status:      domain.PostStatusFailed,
			MaxAttempts: 3,
		},
		MediaIDs: []string{failedOnly.ID},
	}); err != nil {
		t.Fatalf("create failed post: %v", err)
	}

	deleted, err := store.DeleteMediaUnusedByPendingPosts(ctx)
	if err != nil {
		t.Fatalf("purge media: %v", err)
	}
	if len(deleted) != 3 {
		t.Fatalf("expected 3 media files to be purged, got %d", len(deleted))
	}
	deletedIDs := []string{deleted[0].ID, deleted[1].ID, deleted[2].ID}
	slices.Sort(deletedIDs)
	expectedDeleted := []string{orphan.ID, publishedOnly.ID, canceledOnly.ID}
	slices.Sort(expectedDeleted)
	if !slices.Equal(deletedIDs, expectedDeleted) {
		t.Fatalf("unexpected purged ids: got=%v want=%v", deletedIDs, expectedDeleted)
	}

	items, err := store.ListMedia(ctx, 20)
	if err != nil {
		t.Fatalf("list media after purge: %v", err)
	}
	remaining := make(map[string]struct{}, len(items))
	for _, item := range items {
		remaining[item.Media.ID] = struct{}{}
	}
	for _, kept := range []string{draftOnly.ID, scheduledFutureOnly.ID, scheduledPastOnly.ID, failedOnly.ID, mixedPublishedAndDraft.ID} {
		if _, ok := remaining[kept]; !ok {
			t.Fatalf("expected media %s to remain after purge", kept)
		}
	}
	for _, removed := range []string{orphan.ID, publishedOnly.ID, canceledOnly.ID} {
		if _, ok := remaining[removed]; ok {
			t.Fatalf("expected media %s to be removed by purge", removed)
		}
	}

	var dangling int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_media WHERE media_id IN (?, ?, ?)`, orphan.ID, publishedOnly.ID, canceledOnly.ID).Scan(&dangling); err != nil {
		t.Fatalf("count post_media after purge: %v", err)
	}
	if dangling != 0 {
		t.Fatalf("expected no dangling post_media rows for purged media, got %d", dangling)
	}
}
