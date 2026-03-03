package parity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/api"
	"github.com/antoniolg/publisher/internal/cli"
	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
)

type parityEnv struct {
	t        *testing.T
	store    *db.Store
	baseURL  string
	token    string
	account  domain.SocialAccount
	session  string
	nextReq  int
	tempFile string
}

func newParityEnv(t *testing.T) *parityEnv {
	t.Helper()

	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	account, err := store.UpsertAccount(t.Context(), db.UpsertAccountParams{
		Platform:          domain.PlatformX,
		DisplayName:       "Parity X",
		ExternalAccountID: "parity_" + strings.ReplaceAll(t.Name(), "/", "_"),
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("upsert account: %v", err)
	}

	token := "tok_parity"
	srv := api.Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		APIToken:          token,
		Registry: publisher.NewProviderRegistry(
			publisher.NewXProvider(publisher.XConfig{}),
			publisher.NewLinkedInProvider(publisher.LinkedInProviderConfig{}),
			publisher.NewFacebookProvider(publisher.MetaProviderConfig{}),
			publisher.NewInstagramProvider(publisher.MetaProviderConfig{}),
		),
	}
	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)

	env := &parityEnv{
		t:       t,
		store:   store,
		baseURL: httpServer.URL,
		token:   token,
		account: account,
		nextReq: 100,
	}
	env.session = env.mcpInitialize()
	env.tempFile = filepath.Join(tempDir, "sample.bin")
	if err := os.WriteFile(env.tempFile, []byte("parity-media-content"), 0o644); err != nil {
		t.Fatalf("write sample media file: %v", err)
	}
	return env
}

func (e *parityEnv) runCLI(args ...string) []byte {
	e.t.Helper()
	code, stdout, stderr := e.runCLIResult(args...)
	if code != 0 {
		e.t.Fatalf("cli exited %d\nargs=%v\nstderr=%s\nstdout=%s", code, args, stderr, stdout)
	}
	return bytes.TrimSpace([]byte(stdout))
}

func (e *parityEnv) runCLIResult(args ...string) (code int, stdout string, stderr string) {
	e.t.Helper()
	full := append([]string{"--base-url", e.baseURL, "--api-token", e.token, "--json"}, args...)
	var out bytes.Buffer
	var errOut bytes.Buffer
	code = cli.Run(context.Background(), full, &out, &errOut)
	return code, out.String(), errOut.String()
}

func (e *parityEnv) seedFailedDeadLetter(text string) string {
	e.t.Helper()
	created, err := e.store.CreatePost(e.t.Context(), db.CreatePostParams{Post: domain.Post{
		AccountID:   e.account.ID,
		Platform:    e.account.Platform,
		Text:        text,
		Status:      domain.PostStatusScheduled,
		ScheduledAt: time.Now().UTC().Add(-1 * time.Minute),
		MaxAttempts: 1,
	}})
	if err != nil {
		e.t.Fatalf("seed failed create post: %v", err)
	}
	if err := e.store.RecordPublishFailure(e.t.Context(), created.Post.ID, fmt.Errorf("synthetic failure: %s", strings.TrimSpace(text)), time.Second); err != nil {
		e.t.Fatalf("seed failed record failure: %v", err)
	}
	items, err := e.store.ListDeadLetters(e.t.Context(), 20)
	if err != nil {
		e.t.Fatalf("seed failed list dead letters: %v", err)
	}
	for _, item := range items {
		if item.PostID == created.Post.ID {
			return item.ID
		}
	}
	e.t.Fatalf("seed failed dead letter not found for post=%s", created.Post.ID)
	return ""
}

func assertPostText(t *testing.T, store *db.Store, postID, expectedText string) {
	t.Helper()
	post, err := store.GetPost(t.Context(), postID)
	if err != nil {
		t.Fatalf("get post %s: %v", postID, err)
	}
	if strings.TrimSpace(post.Text) != strings.TrimSpace(expectedText) {
		t.Fatalf("unexpected post text for %s: got=%q want=%q", postID, post.Text, expectedText)
	}
}

func mustJSON(t *testing.T, raw []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode json failed: %v\nraw=%s", err, string(raw))
	}
}
