package db

import (
	"path/filepath"
	"testing"
	"time"

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

func TestOAuthRefreshKeepsPreviousAccessTokenUsableUntilExpiry(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	hash, err := bcrypt.GenerateFromPassword([]byte("owner-pass"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	owner, err := store.UpsertLocalOwnerBootstrap(t.Context(), "owner@example.com", string(hash))
	if err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}
	client, err := store.RegisterOAuthClient(t.Context(), []string{"https://chatgpt.example/callback"})
	if err != nil {
		t.Fatalf("register client: %v", err)
	}

	accessToken1, refreshToken1, firstToken, err := store.CreateOAuthToken(t.Context(), CreateOAuthTokenParams{
		ClientID:   client.ClientID,
		OwnerID:    owner.ID,
		Scope:      "mcp",
		AccessTTL:  time.Hour,
		RefreshTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("create oauth token: %v", err)
	}

	accessToken2, refreshToken2, _, err := store.RotateOAuthRefreshToken(t.Context(), refreshToken1, client.ClientID, time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatalf("rotate refresh token: %v", err)
	}
	if accessToken2 == "" || refreshToken2 == "" {
		t.Fatalf("expected rotated tokens to be returned")
	}

	gotFirst, err := store.GetOAuthTokenByAccessToken(t.Context(), accessToken1)
	if err != nil {
		t.Fatalf("expected previous access token to remain usable until expiry: %v", err)
	}
	if gotFirst.ID != firstToken.ID {
		t.Fatalf("expected original access token to resolve original row, got %q want %q", gotFirst.ID, firstToken.ID)
	}

	if _, _, _, err := store.RotateOAuthRefreshToken(t.Context(), refreshToken1, client.ClientID, time.Hour, 24*time.Hour); err == nil {
		t.Fatalf("expected old refresh token to be unusable after rotation")
	}
}
