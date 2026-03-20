package db

import (
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestUpsertLocalOwnerBootstrapCreatesAndUpdatesSingleOwner(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	hash1, err := bcrypt.GenerateFromPassword([]byte("first-pass"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password 1: %v", err)
	}
	created, err := store.UpsertLocalOwnerBootstrap(t.Context(), "owner@example.com", string(hash1))
	if err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}
	if created.Email != "owner@example.com" {
		t.Fatalf("unexpected owner email %q", created.Email)
	}

	hash2, err := bcrypt.GenerateFromPassword([]byte("second-pass"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password 2: %v", err)
	}
	updated, err := store.UpsertLocalOwnerBootstrap(t.Context(), "new-owner@example.com", string(hash2))
	if err != nil {
		t.Fatalf("update owner bootstrap: %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("expected singleton owner id to remain stable, got %q vs %q", updated.ID, created.ID)
	}
	if updated.Email != "new-owner@example.com" {
		t.Fatalf("expected updated email, got %q", updated.Email)
	}

	ok, err := store.HasLocalOwner(t.Context())
	if err != nil {
		t.Fatalf("has local owner: %v", err)
	}
	if !ok {
		t.Fatalf("expected local owner to exist")
	}

	authed, err := store.AuthenticateLocalOwner(t.Context(), "new-owner@example.com", "second-pass")
	if err != nil {
		t.Fatalf("authenticate updated owner: %v", err)
	}
	if authed.ID != created.ID {
		t.Fatalf("expected updated auth to resolve same owner id, got %q want %q", authed.ID, created.ID)
	}
}
