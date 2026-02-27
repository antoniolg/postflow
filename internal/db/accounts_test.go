package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
)

func TestDeleteAccountRejectsPendingPosts(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	account := createTestAccount(t, store, domain.PlatformX)
	if _, err := store.CreatePost(context.Background(), CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Platform:    account.Platform,
			Text:        "pending",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(10 * time.Minute),
			MaxAttempts: 3,
		},
	}); err != nil {
		t.Fatalf("create post: %v", err)
	}
	if err := store.DisconnectAccount(context.Background(), account.ID); err != nil {
		t.Fatalf("disconnect account: %v", err)
	}

	err = store.DeleteAccount(context.Background(), account.ID)
	if !errors.Is(err, ErrAccountHasPosts) {
		t.Fatalf("expected ErrAccountHasPosts, got %v", err)
	}
}

func TestDeleteAccountRemovesHistoricalPostsAndAccount(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	account := createTestAccount(t, store, domain.PlatformLinkedIn)
	result, err := store.CreatePost(context.Background(), CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Platform:    account.Platform,
			Text:        "published",
			Status:      domain.PostStatusPublished,
			ScheduledAt: time.Now().UTC().Add(-10 * time.Minute),
			MaxAttempts: 3,
		},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}
	if err := store.DisconnectAccount(context.Background(), account.ID); err != nil {
		t.Fatalf("disconnect account: %v", err)
	}

	if err := store.DeleteAccount(context.Background(), account.ID); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	if _, err := store.GetAccount(context.Background(), account.ID); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("expected account to be deleted, got %v", err)
	}
	if _, err := store.GetPost(context.Background(), result.Post.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected post to be deleted, got %v", err)
	}
}
