package api

import (
	"testing"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
)

func createTestAccount(t *testing.T, store *db.Store) domain.SocialAccount {
	t.Helper()
	account, err := store.UpsertAccount(t.Context(), db.UpsertAccountParams{
		Platform:          domain.PlatformX,
		DisplayName:       "X Test",
		ExternalAccountID: "x_test",
		AuthMethod:        domain.AuthMethodOAuth,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("create test account: %v", err)
	}
	return account
}

func testAccountID(t *testing.T, store *db.Store) string {
	t.Helper()
	return createTestAccount(t, store).ID
}
