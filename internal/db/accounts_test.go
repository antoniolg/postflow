package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
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

func TestDeleteAccountRejectsHistoricalPosts(t *testing.T) {
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

	err = store.DeleteAccount(context.Background(), account.ID)
	if !errors.Is(err, ErrAccountHasPosts) {
		t.Fatalf("expected ErrAccountHasPosts, got %v", err)
	}
	if _, err := store.GetPost(context.Background(), result.Post.ID); err != nil {
		t.Fatalf("expected historical post to be preserved, got %v", err)
	}
}

func TestUpdateAccountXPremium(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	xAccount := createTestAccount(t, store, domain.PlatformX)
	if err := store.UpdateAccountXPremium(context.Background(), xAccount.ID, true); err != nil {
		t.Fatalf("update x premium true: %v", err)
	}
	updated, err := store.GetAccount(context.Background(), xAccount.ID)
	if err != nil {
		t.Fatalf("get x account: %v", err)
	}
	if !updated.XPremium {
		t.Fatalf("expected x premium to be true")
	}

	if err := store.UpdateAccountXPremium(context.Background(), xAccount.ID, false); err != nil {
		t.Fatalf("update x premium false: %v", err)
	}
	updated, err = store.GetAccount(context.Background(), xAccount.ID)
	if err != nil {
		t.Fatalf("get x account after disable: %v", err)
	}
	if updated.XPremium {
		t.Fatalf("expected x premium to be false")
	}
}

func TestUpdateAccountXPremiumRejectsNonX(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	liAccount := createTestAccount(t, store, domain.PlatformLinkedIn)
	err = store.UpdateAccountXPremium(context.Background(), liAccount.ID, true)
	if !errors.Is(err, ErrAccountNotXPlatform) {
		t.Fatalf("expected ErrAccountNotXPlatform, got %v", err)
	}
}
