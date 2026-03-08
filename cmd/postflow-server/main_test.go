package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/antoniolg/postflow/internal/config"
	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/secure"
)

func TestBootstrapXAccountSkipsWhenDisabled(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	if err := store.SetBootstrapXAccountDisabled(context.Background(), true); err != nil {
		t.Fatalf("disable bootstrap x account: %v", err)
	}

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	cipher, err := secure.NewCipher(key, 1)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}

	cfg := config.Config{
		X: config.XConfig{
			AccessToken:       "token",
			AccessTokenSecret: "secret",
		},
	}
	if err := bootstrapXAccount(context.Background(), store, cipher, cfg); err != nil {
		t.Fatalf("bootstrap x account: %v", err)
	}

	_, err = store.GetAccountByPlatformExternalID(context.Background(), domain.PlatformX, domain.AccountKindDefault, "x-default")
	if err != nil && err != sql.ErrNoRows {
		t.Fatalf("get bootstrap x account: %v", err)
	}
	if err == nil {
		t.Fatalf("expected bootstrap x account to stay absent when disabled")
	}
}
