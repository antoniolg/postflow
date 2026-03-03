package db

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antoniolg/publisher/internal/domain"
)

func TestListAccountsAndUpdateStatus(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	facebook, err := store.UpsertAccount(ctx, UpsertAccountParams{
		Platform:          domain.PlatformFacebook,
		DisplayName:       "B Team",
		ExternalAccountID: "fb_listing_b",
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("create facebook account: %v", err)
	}
	linkedIn, err := store.UpsertAccount(ctx, UpsertAccountParams{
		Platform:          domain.PlatformLinkedIn,
		DisplayName:       "A Team",
		ExternalAccountID: "li_listing_a",
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("create linkedin account: %v", err)
	}

	accounts, err := store.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(accounts))
	}
	if strings.TrimSpace(accounts[0].ID) != strings.TrimSpace(facebook.ID) {
		t.Fatalf("expected first account facebook by platform sort, got %s", accounts[0].ID)
	}
	if strings.TrimSpace(accounts[1].ID) != strings.TrimSpace(linkedIn.ID) {
		t.Fatalf("expected second account linkedin by platform sort, got %s", accounts[1].ID)
	}

	statusErr := "token expired"
	if err := store.UpdateAccountStatus(ctx, linkedIn.ID, domain.AccountStatusError, &statusErr); err != nil {
		t.Fatalf("set account status error: %v", err)
	}
	afterError, err := store.GetAccount(ctx, linkedIn.ID)
	if err != nil {
		t.Fatalf("get account after error update: %v", err)
	}
	if afterError.Status != domain.AccountStatusError {
		t.Fatalf("expected account status error, got %s", afterError.Status)
	}
	if afterError.LastError == nil || strings.TrimSpace(*afterError.LastError) != statusErr {
		t.Fatalf("expected last_error %q, got %+v", statusErr, afterError.LastError)
	}

	if err := store.UpdateAccountStatus(ctx, linkedIn.ID, domain.AccountStatusConnected, nil); err != nil {
		t.Fatalf("clear account status error: %v", err)
	}
	afterClear, err := store.GetAccount(ctx, linkedIn.ID)
	if err != nil {
		t.Fatalf("get account after clear update: %v", err)
	}
	if afterClear.Status != domain.AccountStatusConnected {
		t.Fatalf("expected account status connected, got %s", afterClear.Status)
	}
	if afterClear.LastError != nil {
		t.Fatalf("expected last_error to be cleared, got %q", *afterClear.LastError)
	}
}

func TestGetAnyConnectedAccountForPlatform(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	older, err := store.UpsertAccount(ctx, UpsertAccountParams{
		Platform:          domain.PlatformInstagram,
		DisplayName:       "Old Connected",
		ExternalAccountID: "ig_connected_old",
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("create older connected account: %v", err)
	}
	_, err = store.UpsertAccount(ctx, UpsertAccountParams{
		Platform:          domain.PlatformInstagram,
		DisplayName:       "Disconnected",
		ExternalAccountID: "ig_disconnected",
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusDisconnected,
	})
	if err != nil {
		t.Fatalf("create disconnected account: %v", err)
	}
	_, err = store.UpsertAccount(ctx, UpsertAccountParams{
		Platform:          domain.PlatformInstagram,
		DisplayName:       "New Connected",
		ExternalAccountID: "ig_connected_new",
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("create newer connected account: %v", err)
	}

	got, err := store.GetAnyConnectedAccountForPlatform(ctx, domain.PlatformInstagram)
	if err != nil {
		t.Fatalf("get connected account for platform: %v", err)
	}
	if strings.TrimSpace(got.ID) != strings.TrimSpace(older.ID) {
		t.Fatalf("expected oldest connected account %s, got %s", older.ID, got.ID)
	}

	_, err = store.GetAnyConnectedAccountForPlatform(ctx, domain.PlatformFacebook)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound for missing platform account, got %v", err)
	}
}
