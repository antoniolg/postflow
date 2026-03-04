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

func TestSaveAndGetAccountCredentialsRoundTrip(t *testing.T) {
	store := openTestStore(t)
	account := createTestAccount(t, store, domain.PlatformX)

	first := EncryptedCredentials{
		Ciphertext: []byte("ciphertext-v1"),
		Nonce:      []byte("nonce-v1"),
		KeyVersion: 1,
		UpdatedAt:  time.Now().UTC().Add(-1 * time.Minute).Round(time.Microsecond),
	}
	if err := store.SaveAccountCredentials(context.Background(), account.ID, first); err != nil {
		t.Fatalf("save credentials v1: %v", err)
	}

	second := EncryptedCredentials{
		Ciphertext: []byte("ciphertext-v2"),
		Nonce:      []byte("nonce-v2"),
		KeyVersion: 2,
		UpdatedAt:  time.Now().UTC().Round(time.Microsecond),
	}
	if err := store.SaveAccountCredentials(context.Background(), account.ID, second); err != nil {
		t.Fatalf("save credentials v2: %v", err)
	}

	got, err := store.GetAccountCredentials(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("get credentials: %v", err)
	}
	if string(got.Ciphertext) != string(second.Ciphertext) {
		t.Fatalf("unexpected ciphertext: got=%q want=%q", string(got.Ciphertext), string(second.Ciphertext))
	}
	if string(got.Nonce) != string(second.Nonce) {
		t.Fatalf("unexpected nonce: got=%q want=%q", string(got.Nonce), string(second.Nonce))
	}
	if got.KeyVersion != second.KeyVersion {
		t.Fatalf("unexpected key version: got=%d want=%d", got.KeyVersion, second.KeyVersion)
	}
}

func TestSaveAccountCredentialsValidation(t *testing.T) {
	store := openTestStore(t)
	account := createTestAccount(t, store, domain.PlatformX)

	cases := []struct {
		name      string
		accountID string
		creds     EncryptedCredentials
		contains  string
	}{
		{
			name:      "missing account id",
			accountID: "",
			creds: EncryptedCredentials{
				Ciphertext: []byte("cipher"),
				Nonce:      []byte("nonce"),
			},
			contains: "account_id is required",
		},
		{
			name:      "missing ciphertext",
			accountID: account.ID,
			creds: EncryptedCredentials{
				Nonce: []byte("nonce"),
			},
			contains: "ciphertext and nonce are required",
		},
		{
			name:      "missing nonce",
			accountID: account.ID,
			creds: EncryptedCredentials{
				Ciphertext: []byte("cipher"),
			},
			contains: "ciphertext and nonce are required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := store.SaveAccountCredentials(context.Background(), tc.accountID, tc.creds)
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.contains)) {
				t.Fatalf("expected error containing %q, got %q", tc.contains, err.Error())
			}
		})
	}
}

func TestConsumeOAuthStateSuccessDeletesState(t *testing.T) {
	store := openTestStore(t)

	created, err := store.CreateOAuthState(context.Background(), domain.OauthState{
		Platform:     domain.PlatformLinkedIn,
		State:        "state_success_" + strings.ReplaceAll(t.Name(), "/", "_"),
		CodeVerifier: "verifier-success",
		ExpiresAt:    time.Now().UTC().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create oauth state: %v", err)
	}

	consumed, err := store.ConsumeOAuthState(context.Background(), created.State)
	if err != nil {
		t.Fatalf("consume oauth state: %v", err)
	}
	if consumed.ID != created.ID || consumed.State != created.State || consumed.Platform != created.Platform {
		t.Fatalf("unexpected consumed state: got=%+v want=%+v", consumed, created)
	}

	_, err = store.ConsumeOAuthState(context.Background(), created.State)
	if !isNoRows(err) {
		t.Fatalf("expected consumed state to be removed, got %v", err)
	}
}

func TestConsumeOAuthStateExpiredDeletesState(t *testing.T) {
	store := openTestStore(t)

	created, err := store.CreateOAuthState(context.Background(), domain.OauthState{
		Platform:     domain.PlatformLinkedIn,
		State:        "state_expired_" + strings.ReplaceAll(t.Name(), "/", "_"),
		CodeVerifier: "verifier-expired",
		ExpiresAt:    time.Now().UTC().Add(-1 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create oauth state: %v", err)
	}

	_, err = store.ConsumeOAuthState(context.Background(), created.State)
	if err == nil {
		t.Fatalf("expected expired oauth state error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "expired") {
		t.Fatalf("expected expired error, got %q", err.Error())
	}

	_, err = store.ConsumeOAuthState(context.Background(), created.State)
	if !isNoRows(err) {
		t.Fatalf("expected expired oauth state to be deleted, got %v", err)
	}
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
