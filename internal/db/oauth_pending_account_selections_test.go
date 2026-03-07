package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestOAuthPendingAccountSelectionLifecycle(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	created, err := store.CreateOAuthPendingAccountSelection(t.Context(), OAuthPendingAccountSelection{
		Platform:   domain.PlatformLinkedIn,
		Ciphertext: []byte("ciphertext"),
		Nonce:      []byte("nonce"),
		KeyVersion: 1,
		ExpiresAt:  time.Now().UTC().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create pending selection: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("expected generated selection id")
	}

	loaded, err := store.GetOAuthPendingAccountSelection(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("load pending selection: %v", err)
	}
	if loaded.ID != created.ID || loaded.Platform != domain.PlatformLinkedIn {
		t.Fatalf("unexpected loaded selection: %+v", loaded)
	}

	if err := store.DeleteOAuthPendingAccountSelection(t.Context(), created.ID); err != nil {
		t.Fatalf("delete pending selection: %v", err)
	}
	if _, err := store.GetOAuthPendingAccountSelection(t.Context(), created.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows after delete, got %v", err)
	}
}

func TestOAuthPendingAccountSelectionExpiredDeletesRecord(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	created, err := store.CreateOAuthPendingAccountSelection(t.Context(), OAuthPendingAccountSelection{
		Platform:   domain.PlatformLinkedIn,
		Ciphertext: []byte("ciphertext"),
		Nonce:      []byte("nonce"),
		KeyVersion: 1,
		ExpiresAt:  time.Now().UTC().Add(-1 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create expired pending selection: %v", err)
	}

	_, err = store.GetOAuthPendingAccountSelection(t.Context(), created.ID)
	if !errors.Is(err, ErrOAuthPendingAccountSelectionExpired) {
		t.Fatalf("expected expired selection error, got %v", err)
	}
	if _, err := store.GetOAuthPendingAccountSelection(t.Context(), created.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected expired selection to be deleted, got %v", err)
	}
}
