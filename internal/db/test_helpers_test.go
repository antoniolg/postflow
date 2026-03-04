package db

import (
	"context"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
)

func createTestAccount(t *testing.T, store *Store, platform domain.Platform) domain.SocialAccount {
	t.Helper()
	if platform == "" {
		platform = domain.PlatformX
	}
	account, err := store.UpsertAccount(context.Background(), UpsertAccountParams{
		Platform:          platform,
		DisplayName:       "Test Account",
		ExternalAccountID: "test-" + string(platform),
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("create test account: %v", err)
	}
	return account
}
