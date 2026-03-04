package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestPostListingAndMediaLookupBlackBox(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformX)
	mediaA := createDBMedia(t, store, "a.png")
	mediaB := createDBMedia(t, store, "b.png")

	scheduledAt := time.Now().UTC().Add(45 * time.Minute).Round(time.Second)
	rootResult, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:      account.ID,
			Platform:       account.Platform,
			Text:           "thread root",
			Status:         domain.PostStatusScheduled,
			ScheduledAt:    scheduledAt,
			ThreadGroupID:  "thd_listing",
			ThreadPosition: 1,
			MaxAttempts:    3,
		},
		MediaIDs: []string{mediaA.ID},
	})
	if err != nil {
		t.Fatalf("create root post: %v", err)
	}
	rootID := strings.TrimSpace(rootResult.Post.ID)

	if _, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:      account.ID,
			Platform:       account.Platform,
			Text:           "thread child",
			Status:         domain.PostStatusScheduled,
			ScheduledAt:    scheduledAt,
			ThreadGroupID:  "thd_listing",
			ThreadPosition: 2,
			ParentPostID:   &rootID,
			RootPostID:     &rootID,
			MaxAttempts:    3,
		},
		MediaIDs: []string{mediaB.ID},
	}); err != nil {
		t.Fatalf("create child post: %v", err)
	}

	draftResult, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Platform:    account.Platform,
			Text:        "just draft",
			Status:      domain.PostStatusDraft,
			MaxAttempts: 3,
		},
	})
	if err != nil {
		t.Fatalf("create draft post: %v", err)
	}

	threadPosts, err := store.ListThreadPosts(ctx, rootID)
	if err != nil {
		t.Fatalf("list thread posts: %v", err)
	}
	if len(threadPosts) != 2 {
		t.Fatalf("expected 2 thread posts, got %d", len(threadPosts))
	}
	if threadPosts[0].ThreadPosition != 1 || threadPosts[1].ThreadPosition != 2 {
		t.Fatalf("expected ordered thread positions [1,2], got [%d,%d]", threadPosts[0].ThreadPosition, threadPosts[1].ThreadPosition)
	}
	if threadPosts[1].ParentPostID == nil || strings.TrimSpace(*threadPosts[1].ParentPostID) != rootID {
		t.Fatalf("expected child parent id=%s, got %+v", rootID, threadPosts[1].ParentPostID)
	}

	scheduledItems, err := store.ListSchedule(ctx, time.Now().UTC().Add(-5*time.Minute), time.Now().UTC().Add(2*time.Hour))
	if err != nil {
		t.Fatalf("list schedule: %v", err)
	}
	if len(scheduledItems) < 2 {
		t.Fatalf("expected at least 2 scheduled posts, got %d", len(scheduledItems))
	}

	drafts, err := store.ListDrafts(ctx)
	if err != nil {
		t.Fatalf("list drafts: %v", err)
	}
	var foundDraft bool
	for _, post := range drafts {
		if strings.TrimSpace(post.ID) == strings.TrimSpace(draftResult.Post.ID) {
			foundDraft = true
			break
		}
	}
	if !foundDraft {
		t.Fatalf("expected created draft %s in draft list", draftResult.Post.ID)
	}

	mediaItems, err := store.GetMediaByIDs(ctx, []string{mediaB.ID, mediaA.ID})
	if err != nil {
		t.Fatalf("get media by ids: %v", err)
	}
	if len(mediaItems) != 2 {
		t.Fatalf("expected 2 media items, got %d", len(mediaItems))
	}
	if strings.TrimSpace(mediaItems[0].ID) != mediaB.ID || strings.TrimSpace(mediaItems[1].ID) != mediaA.ID {
		t.Fatalf("expected media order [%s,%s], got [%s,%s]", mediaB.ID, mediaA.ID, mediaItems[0].ID, mediaItems[1].ID)
	}

	_, err = store.GetMediaByIDs(ctx, []string{"med_missing"})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Fatalf("expected media not found error, got %v", err)
	}
}

func TestUpdateThreadEditableRebuildsChainAndCanShrinkThread(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformLinkedIn)
	media1 := createDBMedia(t, store, "one.png")
	media2 := createDBMedia(t, store, "two.png")
	media3 := createDBMedia(t, store, "three.png")

	baseScheduled := time.Now().UTC().Add(1 * time.Hour).Round(time.Second)
	rootResult, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:      account.ID,
			Platform:       account.Platform,
			Text:           "original root",
			Status:         domain.PostStatusScheduled,
			ScheduledAt:    baseScheduled,
			ThreadGroupID:  "thd_update",
			ThreadPosition: 1,
			MaxAttempts:    3,
		},
		MediaIDs: []string{media1.ID},
	})
	if err != nil {
		t.Fatalf("create original root: %v", err)
	}
	rootID := strings.TrimSpace(rootResult.Post.ID)

	childResult, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:      account.ID,
			Platform:       account.Platform,
			Text:           "original child",
			Status:         domain.PostStatusScheduled,
			ScheduledAt:    baseScheduled,
			ThreadGroupID:  "thd_update",
			ThreadPosition: 2,
			ParentPostID:   &rootID,
			RootPostID:     &rootID,
			MaxAttempts:    3,
		},
		MediaIDs: []string{media2.ID},
	})
	if err != nil {
		t.Fatalf("create original child: %v", err)
	}
	originalChildID := strings.TrimSpace(childResult.Post.ID)

	if _, err := store.db.ExecContext(ctx, `
		UPDATE posts SET attempts = 2, error = ?, next_retry_at = ? WHERE id IN (?, ?)
	`, "transient failure", time.Now().UTC().Add(10*time.Minute).Format(time.RFC3339Nano), rootID, originalChildID); err != nil {
		t.Fatalf("prepare dirty thread state: %v", err)
	}

	steps := []ThreadStepUpdate{
		{
			Text:        "edited root",
			ScheduledAt: time.Time{},
			MediaIDs:    []string{media1.ID},
		},
		{
			Text:        "edited child",
			ScheduledAt: baseScheduled.Add(10 * time.Minute),
			MediaIDs:    []string{media2.ID},
		},
		{
			Text:        "new third step",
			ScheduledAt: baseScheduled.Add(20 * time.Minute),
			MediaIDs:    []string{media3.ID},
		},
	}
	if err := store.UpdateThreadEditable(ctx, rootID, steps); err != nil {
		t.Fatalf("update thread editable: %v", err)
	}

	updated, err := store.ListThreadPosts(ctx, rootID)
	if err != nil {
		t.Fatalf("list updated thread: %v", err)
	}
	if len(updated) != 3 {
		t.Fatalf("expected 3 thread steps after growth, got %d", len(updated))
	}

	firstID := strings.TrimSpace(updated[0].ID)
	secondID := strings.TrimSpace(updated[1].ID)
	thirdID := strings.TrimSpace(updated[2].ID)
	if firstID != rootID {
		t.Fatalf("expected root id to remain %s, got %s", rootID, firstID)
	}
	if updated[0].Status != domain.PostStatusDraft {
		t.Fatalf("expected root status=draft, got %s", updated[0].Status)
	}
	if updated[1].Status != domain.PostStatusScheduled || updated[2].Status != domain.PostStatusScheduled {
		t.Fatalf("expected child statuses scheduled, got [%s,%s]", updated[1].Status, updated[2].Status)
	}
	if updated[1].ParentPostID == nil || strings.TrimSpace(*updated[1].ParentPostID) != firstID {
		t.Fatalf("expected second parent=%s, got %+v", firstID, updated[1].ParentPostID)
	}
	if updated[2].ParentPostID == nil || strings.TrimSpace(*updated[2].ParentPostID) != secondID {
		t.Fatalf("expected third parent=%s, got %+v", secondID, updated[2].ParentPostID)
	}
	for _, post := range updated {
		if post.RootPostID == nil || strings.TrimSpace(*post.RootPostID) != firstID {
			t.Fatalf("expected root_post_id=%s for post %s, got %+v", firstID, post.ID, post.RootPostID)
		}
		if post.Attempts != 0 {
			t.Fatalf("expected attempts reset to 0 for post %s, got %d", post.ID, post.Attempts)
		}
		if post.Error != nil {
			t.Fatalf("expected error cleared for post %s, got %q", post.ID, *post.Error)
		}
		if post.NextRetryAt != nil {
			t.Fatalf("expected next_retry_at cleared for post %s", post.ID)
		}
		if len(post.Media) != 1 {
			t.Fatalf("expected one media on post %s, got %d", post.ID, len(post.Media))
		}
	}

	if err := store.UpdateThreadEditable(ctx, rootID, steps[:1]); err != nil {
		t.Fatalf("shrink thread editable: %v", err)
	}
	shrunk, err := store.ListThreadPosts(ctx, rootID)
	if err != nil {
		t.Fatalf("list shrunk thread: %v", err)
	}
	if len(shrunk) != 1 {
		t.Fatalf("expected 1 thread step after shrink, got %d", len(shrunk))
	}
	for _, removedID := range []string{secondID, thirdID} {
		_, err := store.GetPost(ctx, removedID)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected removed post %s to be deleted, got err=%v", removedID, err)
		}
	}
}

func TestMarkPublishedSetsPublishedFieldsAndClearsRetryState(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	account := createTestAccount(t, store, domain.PlatformFacebook)
	created, err := store.CreatePost(ctx, CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Platform:    account.Platform,
			Text:        "pending publish",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(-5 * time.Minute),
			MaxAttempts: 3,
		},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	if _, err := store.db.ExecContext(ctx, `
		UPDATE posts
		SET status = ?, error = ?, next_retry_at = ?, attempts = 2
		WHERE id = ?
	`, domain.PostStatusPublishing, "timeout", time.Now().UTC().Add(3*time.Minute).Format(time.RFC3339Nano), created.Post.ID); err != nil {
		t.Fatalf("prepare publish state: %v", err)
	}

	if err := store.MarkPublished(ctx, created.Post.ID, "ext_123"); err != nil {
		t.Fatalf("mark published: %v", err)
	}
	post, err := store.GetPost(ctx, created.Post.ID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if post.Status != domain.PostStatusPublished {
		t.Fatalf("expected status published, got %s", post.Status)
	}
	if post.PublishedAt == nil {
		t.Fatalf("expected published_at to be set")
	}
	if post.ExternalID == nil || strings.TrimSpace(*post.ExternalID) != "ext_123" {
		t.Fatalf("expected external id ext_123, got %+v", post.ExternalID)
	}
	if post.Error != nil {
		t.Fatalf("expected error cleared, got %q", *post.Error)
	}
	if post.NextRetryAt != nil {
		t.Fatalf("expected next_retry_at cleared")
	}
}

func createDBMedia(t *testing.T, store *Store, originalName string) domain.Media {
	t.Helper()
	item, err := store.CreateMedia(context.Background(), domain.Media{
		Kind:         "image",
		OriginalName: strings.TrimSpace(originalName),
		StoragePath:  "/tmp/" + strings.TrimSpace(originalName),
		MimeType:     "image/png",
		SizeBytes:    512,
	})
	if err != nil {
		t.Fatalf("create media %s: %v", originalName, err)
	}
	return item
}
