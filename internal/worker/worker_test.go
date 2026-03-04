package worker

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
	"github.com/antoniolg/postflow/internal/secure"
)

func TestLoadCredentialsWithoutRowReturnsEmpty(t *testing.T) {
	store := openWorkerTestStore(t)
	cipher := newWorkerTestCipher(t)
	account := createWorkerTestAccount(t, store)

	w := Worker{Store: store, Cipher: cipher}
	got, err := w.loadCredentials(t.Context(), account.ID)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if strings.TrimSpace(got.AccessToken) != "" {
		t.Fatalf("expected empty access token, got %q", got.AccessToken)
	}
}

func TestSaveAndLoadCredentialsRoundTrip(t *testing.T) {
	store := openWorkerTestStore(t)
	cipher := newWorkerTestCipher(t)
	account := createWorkerTestAccount(t, store)

	w := Worker{Store: store, Cipher: cipher}
	original := postflow.Credentials{
		AccessToken:  "access_123",
		RefreshToken: "refresh_123",
		TokenType:    "Bearer",
		Extra: map[string]string{
			"scope": "write",
		},
	}
	if err := (workerCredentialsStore{worker: w}).SaveCredentials(t.Context(), account.ID, original); err != nil {
		t.Fatalf("save credentials: %v", err)
	}
	loaded, err := w.loadCredentials(t.Context(), account.ID)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if loaded.AccessToken != original.AccessToken {
		t.Fatalf("unexpected access token: got=%q want=%q", loaded.AccessToken, original.AccessToken)
	}
	if loaded.RefreshToken != original.RefreshToken {
		t.Fatalf("unexpected refresh token: got=%q want=%q", loaded.RefreshToken, original.RefreshToken)
	}
	if loaded.TokenType != original.TokenType {
		t.Fatalf("unexpected token type: got=%q want=%q", loaded.TokenType, original.TokenType)
	}
	if loaded.Extra["scope"] != "write" {
		t.Fatalf("unexpected extra scope: got=%q", loaded.Extra["scope"])
	}
}

func TestRunOncePublishesDuePost(t *testing.T) {
	store := openWorkerTestStore(t)
	cipher := newWorkerTestCipher(t)
	account := createWorkerTestAccount(t, store)
	created, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Platform:    account.Platform,
			Text:        "worker should publish this",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(-1 * time.Minute),
			MaxAttempts: 3,
		},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	w := Worker{
		Store:        store,
		Registry:     postflow.NewProviderRegistry(postflow.NewMockProvider(domain.PlatformX)),
		Cipher:       cipher,
		Interval:     25 * time.Millisecond,
		RetryBackoff: 1 * time.Second,
	}
	w.runOnce(context.Background())

	post, err := store.GetPost(t.Context(), created.Post.ID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if post.Status != domain.PostStatusPublished {
		t.Fatalf("expected status published after runOnce, got %s", post.Status)
	}
	if post.ExternalID == nil || strings.TrimSpace(*post.ExternalID) == "" {
		t.Fatalf("expected external_id to be set after publish")
	}
}

func TestStartStopsWhenContextCancelled(t *testing.T) {
	store := openWorkerTestStore(t)
	cipher := newWorkerTestCipher(t)
	_ = createWorkerTestAccount(t, store)

	w := Worker{
		Store:        store,
		Registry:     postflow.NewProviderRegistry(postflow.NewMockProvider(domain.PlatformX)),
		Cipher:       cipher,
		Interval:     25 * time.Millisecond,
		RetryBackoff: 1 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		w.Start(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("worker Start did not stop after context cancellation")
	}
}

func openWorkerTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "worker.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func createWorkerTestAccount(t *testing.T, store *db.Store) domain.SocialAccount {
	t.Helper()
	account, err := store.UpsertAccount(t.Context(), db.UpsertAccountParams{
		Platform:          domain.PlatformX,
		DisplayName:       "Worker X",
		ExternalAccountID: "worker_" + strings.ReplaceAll(t.Name(), "/", "_"),
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("upsert account: %v", err)
	}
	return account
}

func newWorkerTestCipher(t *testing.T) *secure.Cipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	cipher, err := secure.NewCipher(key, 1)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	return cipher
}
