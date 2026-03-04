package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestCreatePostWithIdempotencyKeyReturnsExisting(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformX)
	scheduled := time.Now().UTC().Add(10 * time.Minute)

	first, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "hola",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: scheduled,
			MaxAttempts: 3,
		},
		IdempotencyKey: "same-key",
	})
	if err != nil {
		t.Fatalf("create first post: %v", err)
	}
	if !first.Created {
		t.Fatalf("expected first create to be created=true")
	}

	second, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "hola",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: scheduled,
			MaxAttempts: 3,
		},
		IdempotencyKey: "same-key",
	})
	if err != nil {
		t.Fatalf("create second post: %v", err)
	}
	if second.Created {
		t.Fatalf("expected second create to be created=false")
	}
	if second.Post.ID != first.Post.ID {
		t.Fatalf("expected same post ID, got %s != %s", second.Post.ID, first.Post.ID)
	}
}

func TestRecordPublishFailureRetriesThenMovesToDLQ(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformX)
	created, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "retry me",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(-1 * time.Minute),
			MaxAttempts: 2,
		},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	claimed, err := store.ClaimDuePosts(ctx, 10)
	if err != nil {
		t.Fatalf("claim due posts (1): %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected 1 claimed post, got %d", len(claimed))
	}

	if err := store.RecordPublishFailure(ctx, created.Post.ID, errors.New("network boom"), 1*time.Second); err != nil {
		t.Fatalf("record publish failure (1): %v", err)
	}

	firstRetryPost, err := store.GetPost(ctx, created.Post.ID)
	if err != nil {
		t.Fatalf("get post after first failure: %v", err)
	}
	if firstRetryPost.Status != domain.PostStatusScheduled {
		t.Fatalf("expected status scheduled after first failure, got %s", firstRetryPost.Status)
	}
	if firstRetryPost.Attempts != 1 {
		t.Fatalf("expected attempts=1 after first failure, got %d", firstRetryPost.Attempts)
	}
	if firstRetryPost.NextRetryAt == nil {
		t.Fatalf("expected next_retry_at to be set after first failure")
	}

	if _, err := store.db.ExecContext(ctx, `UPDATE posts SET next_retry_at = ? WHERE id = ?`, time.Now().UTC().Add(-1*time.Second).Format(time.RFC3339Nano), created.Post.ID); err != nil {
		t.Fatalf("force retry window: %v", err)
	}

	claimed, err = store.ClaimDuePosts(ctx, 10)
	if err != nil {
		t.Fatalf("claim due posts (2): %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected 1 claimed post on second round, got %d", len(claimed))
	}

	if err := store.RecordPublishFailure(ctx, created.Post.ID, errors.New("still failing"), 1*time.Second); err != nil {
		t.Fatalf("record publish failure (2): %v", err)
	}

	finalPost, err := store.GetPost(ctx, created.Post.ID)
	if err != nil {
		t.Fatalf("get post after second failure: %v", err)
	}
	if finalPost.Status != domain.PostStatusFailed {
		t.Fatalf("expected status failed after max attempts, got %s", finalPost.Status)
	}
	if finalPost.Attempts != 2 {
		t.Fatalf("expected attempts=2 after second failure, got %d", finalPost.Attempts)
	}

	var dlqCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dead_letters WHERE post_id = ?`, created.Post.ID).Scan(&dlqCount); err != nil {
		t.Fatalf("count dead letters: %v", err)
	}
	if dlqCount != 1 {
		t.Fatalf("expected 1 dead letter, got %d", dlqCount)
	}
}

func TestReschedulePublishWithoutAttemptKeepsAttempts(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformInstagram)
	created, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "wait for ig media processing",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(-1 * time.Minute),
			MaxAttempts: 3,
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
		t.Fatalf("expected 1 claimed post, got %d", len(claimed))
	}

	before, err := store.GetPost(ctx, created.Post.ID)
	if err != nil {
		t.Fatalf("get post before defer: %v", err)
	}
	if before.Attempts != 0 {
		t.Fatalf("expected attempts=0 before defer, got %d", before.Attempts)
	}

	if err := store.ReschedulePublishWithoutAttempt(ctx, created.Post.ID, errors.New("instagram publish failed: status=400 body={\"error\":{\"code\":9007,\"error_subcode\":2207027}}"), 2*time.Second); err != nil {
		t.Fatalf("reschedule without attempt: %v", err)
	}

	after, err := store.GetPost(ctx, created.Post.ID)
	if err != nil {
		t.Fatalf("get post after defer: %v", err)
	}
	if after.Status != domain.PostStatusScheduled {
		t.Fatalf("expected status scheduled, got %s", after.Status)
	}
	if after.Attempts != 0 {
		t.Fatalf("expected attempts to stay 0, got %d", after.Attempts)
	}
	if after.NextRetryAt == nil {
		t.Fatalf("expected next_retry_at to be set")
	}

	var dlqCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dead_letters WHERE post_id = ?`, created.Post.ID).Scan(&dlqCount); err != nil {
		t.Fatalf("count dead letters: %v", err)
	}
	if dlqCount != 0 {
		t.Fatalf("expected no dead letters, got %d", dlqCount)
	}
}

func TestRecoverStalePublishingPosts(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformInstagram)

	stale, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "stale publishing",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(-1 * time.Minute),
			MaxAttempts: 3,
		},
	})
	if err != nil {
		t.Fatalf("create stale post: %v", err)
	}
	fresh, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "fresh publishing",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(-1 * time.Minute),
			MaxAttempts: 3,
		},
	})
	if err != nil {
		t.Fatalf("create fresh post: %v", err)
	}

	claimed, err := store.ClaimDuePosts(ctx, 10)
	if err != nil {
		t.Fatalf("claim due posts: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("expected 2 claimed posts, got %d", len(claimed))
	}

	old := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `UPDATE posts SET updated_at = ? WHERE id = ?`, old, stale.Post.ID); err != nil {
		t.Fatalf("set stale updated_at: %v", err)
	}

	recovered, err := store.RecoverStalePublishingPosts(ctx, 10*time.Minute)
	if err != nil {
		t.Fatalf("recover stale publishing posts: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("expected 1 recovered post, got %d", recovered)
	}

	stalePost, err := store.GetPost(ctx, stale.Post.ID)
	if err != nil {
		t.Fatalf("get stale post after recovery: %v", err)
	}
	if stalePost.Status != domain.PostStatusFailed {
		t.Fatalf("expected stale post to be failed, got %s", stalePost.Status)
	}

	freshPost, err := store.GetPost(ctx, fresh.Post.ID)
	if err != nil {
		t.Fatalf("get fresh post after recovery: %v", err)
	}
	if freshPost.Status != domain.PostStatusPublishing {
		t.Fatalf("expected fresh post to remain publishing, got %s", freshPost.Status)
	}

	var dlqCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dead_letters WHERE post_id = ?`, stale.Post.ID).Scan(&dlqCount); err != nil {
		t.Fatalf("count dead letters for stale post: %v", err)
	}
	if dlqCount != 1 {
		t.Fatalf("expected 1 dead letter for stale post, got %d", dlqCount)
	}

	recoveredAgain, err := store.RecoverStalePublishingPosts(ctx, 10*time.Minute)
	if err != nil {
		t.Fatalf("recover stale publishing posts second run: %v", err)
	}
	if recoveredAgain != 0 {
		t.Fatalf("expected no recovered posts on second run, got %d", recoveredAgain)
	}
}

func TestCreateDraftDefaultsToDraftStatus(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	created, err := store.CreatePost(context.Background(), CreatePostParams{
		Post: domain.Post{
			AccountID:   createTestAccount(t, store, domain.PlatformX).ID,
			Text:        "draft",
			MaxAttempts: 3,
		},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}
	if created.Post.Status != domain.PostStatusDraft {
		t.Fatalf("expected status draft, got %s", created.Post.Status)
	}
	if !created.Post.ScheduledAt.IsZero() {
		t.Fatalf("expected zero scheduled_at for draft")
	}
}

func TestSetAndGetUITimezone(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	empty, err := store.GetUITimezone(ctx)
	if err != nil {
		t.Fatalf("get empty timezone: %v", err)
	}
	if empty != "" {
		t.Fatalf("expected empty timezone, got %q", empty)
	}

	if err := store.SetUITimezone(ctx, "Europe/Madrid"); err != nil {
		t.Fatalf("set timezone: %v", err)
	}
	got, err := store.GetUITimezone(ctx)
	if err != nil {
		t.Fatalf("get timezone: %v", err)
	}
	if got != "Europe/Madrid" {
		t.Fatalf("expected Europe/Madrid, got %q", got)
	}

	if err := store.SetUITimezone(ctx, "America/New_York"); err != nil {
		t.Fatalf("update timezone: %v", err)
	}
	updated, err := store.GetUITimezone(ctx)
	if err != nil {
		t.Fatalf("get updated timezone: %v", err)
	}
	if updated != "America/New_York" {
		t.Fatalf("expected America/New_York, got %q", updated)
	}
}

func TestDeletePostEditableDeletesPendingPost(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformX)
	media, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "img.png",
		StoragePath:  "/tmp/img.png",
		MimeType:     "image/png",
		SizeBytes:    10,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}
	created, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "delete me",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(5 * time.Minute),
			MaxAttempts: 3,
		},
		MediaIDs: []string{media.ID},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	if err := store.DeletePostEditable(ctx, created.Post.ID); err != nil {
		t.Fatalf("delete post: %v", err)
	}
	if _, err := store.GetPost(ctx, created.Post.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows after delete, got %v", err)
	}
	var links int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_media WHERE post_id = ?`, created.Post.ID).Scan(&links); err != nil {
		t.Fatalf("count post_media: %v", err)
	}
	if links != 0 {
		t.Fatalf("expected 0 post_media links after delete, got %d", links)
	}
}

func TestDeletePostEditableRejectsPublishedPost(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformX)
	created, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "already live",
			Status:      domain.PostStatusPublished,
			ScheduledAt: time.Now().UTC().Add(-2 * time.Minute),
			MaxAttempts: 3,
		},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	err = store.DeletePostEditable(ctx, created.Post.ID)
	if !errors.Is(err, ErrPostNotDeletable) {
		t.Fatalf("expected ErrPostNotDeletable, got %v", err)
	}
}

func TestUpdatePostEditableReplacesMedia(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformLinkedIn)
	mediaA, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "a.png",
		StoragePath:  "/tmp/a.png",
		MimeType:     "image/png",
		SizeBytes:    10,
	})
	if err != nil {
		t.Fatalf("create media A: %v", err)
	}
	mediaB, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "b.png",
		StoragePath:  "/tmp/b.png",
		MimeType:     "image/png",
		SizeBytes:    11,
	})
	if err != nil {
		t.Fatalf("create media B: %v", err)
	}
	created, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "initial",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(30 * time.Minute),
			MaxAttempts: 3,
		},
		MediaIDs: []string{mediaA.ID},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	if err := store.UpdatePostEditable(ctx, created.Post.ID, "edited", time.Time{}, []string{mediaB.ID}, true); err != nil {
		t.Fatalf("update editable: %v", err)
	}

	updated, err := store.GetPost(ctx, created.Post.ID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if updated.Status != domain.PostStatusDraft {
		t.Fatalf("expected draft status after clearing schedule, got %s", updated.Status)
	}
	if updated.Text != "edited" {
		t.Fatalf("expected updated text, got %q", updated.Text)
	}
	if len(updated.Media) != 1 {
		t.Fatalf("expected one media item after replacement, got %d", len(updated.Media))
	}
	if updated.Media[0].ID != mediaB.ID {
		t.Fatalf("expected media %q after replacement, got %q", mediaB.ID, updated.Media[0].ID)
	}
}

func TestUpdatePostEditableClearsMedia(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformLinkedIn)
	media, err := store.CreateMedia(ctx, domain.Media{
		Kind:         "image",
		OriginalName: "clear.png",
		StoragePath:  "/tmp/clear.png",
		MimeType:     "image/png",
		SizeBytes:    12,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}
	created, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "has media",
			Status:      domain.PostStatusDraft,
			MaxAttempts: 3,
		},
		MediaIDs: []string{media.ID},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	if err := store.UpdatePostEditable(ctx, created.Post.ID, "no media now", time.Time{}, nil, true); err != nil {
		t.Fatalf("update editable: %v", err)
	}

	updated, err := store.GetPost(ctx, created.Post.ID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if len(updated.Media) != 0 {
		t.Fatalf("expected media to be cleared, got %d items", len(updated.Media))
	}
}

func TestScheduleDraftPostMarksDraftAsScheduled(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformX)
	created, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "draft to schedule",
			Status:      domain.PostStatusDraft,
			MaxAttempts: 3,
		},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	target := time.Now().UTC().Add(45 * time.Minute).Truncate(time.Second)
	if err := store.ScheduleDraftPost(ctx, created.Post.ID, target); err != nil {
		t.Fatalf("schedule draft: %v", err)
	}

	scheduled, err := store.GetPost(ctx, created.Post.ID)
	if err != nil {
		t.Fatalf("get scheduled post: %v", err)
	}
	if scheduled.Status != domain.PostStatusScheduled {
		t.Fatalf("expected scheduled status, got %s", scheduled.Status)
	}
	if scheduled.ScheduledAt.IsZero() {
		t.Fatalf("expected scheduled_at to be set")
	}
}

func TestCancelPostMarksPostAsCanceled(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformX)
	created, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "scheduled to cancel",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(30 * time.Minute),
			MaxAttempts: 3,
		},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	if err := store.CancelPost(ctx, created.Post.ID); err != nil {
		t.Fatalf("cancel post: %v", err)
	}

	canceled, err := store.GetPost(ctx, created.Post.ID)
	if err != nil {
		t.Fatalf("get canceled post: %v", err)
	}
	if canceled.Status != domain.PostStatusCanceled {
		t.Fatalf("expected canceled status, got %s", canceled.Status)
	}
}

func TestDeleteThreadEditableDeletesThreadRoot(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformX)
	created, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "thread root",
			Status:      domain.PostStatusDraft,
			MaxAttempts: 3,
		},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	if err := store.DeleteThreadEditable(ctx, created.Post.ID); err != nil {
		t.Fatalf("delete thread editable: %v", err)
	}
	if _, err := store.GetPost(ctx, created.Post.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows after delete, got %v", err)
	}
}
