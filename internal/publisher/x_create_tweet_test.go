package publisher

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateStatusUsesV2TweetsEndpoint(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotContentType string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"123456"}}`))
	}))
	defer srv.Close()

	client, err := NewXClient(XConfig{
		APIBaseURL:        srv.URL,
		UploadBaseURL:     srv.URL,
		APIKey:            "key",
		APIKeySecret:      "secret",
		AccessToken:       "token",
		AccessTokenSecret: "token_secret",
	})
	if err != nil {
		t.Fatalf("NewXClient() error = %v", err)
	}

	id, err := client.createStatus(context.Background(), "hola", []string{"111", "222"})
	if err != nil {
		t.Fatalf("createStatus() error = %v", err)
	}
	if id != "123456" {
		t.Fatalf("id = %q, want %q", id, "123456")
	}
	if gotPath != "/2/tweets" {
		t.Fatalf("path = %q, want %q", gotPath, "/2/tweets")
	}
	if gotAuth == "" {
		t.Fatalf("Authorization header is empty")
	}
	if gotContentType != "application/json" {
		t.Fatalf("content-type = %q, want %q", gotContentType, "application/json")
	}
	if gotBody["text"] != "hola" {
		t.Fatalf("body text = %v, want %q", gotBody["text"], "hola")
	}
	media, ok := gotBody["media"].(map[string]any)
	if !ok {
		t.Fatalf("body media missing or invalid: %#v", gotBody["media"])
	}
	ids, ok := media["media_ids"].([]any)
	if !ok || len(ids) != 2 || ids[0] != "111" || ids[1] != "222" {
		t.Fatalf("media_ids = %#v, want [111 222]", media["media_ids"])
	}
}

func TestCreateStatusSupportsLegacyResponseID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id_str":"legacy-1"}`))
	}))
	defer srv.Close()

	client, err := NewXClient(XConfig{
		APIBaseURL:        srv.URL,
		UploadBaseURL:     srv.URL,
		APIKey:            "key",
		APIKeySecret:      "secret",
		AccessToken:       "token",
		AccessTokenSecret: "token_secret",
	})
	if err != nil {
		t.Fatalf("NewXClient() error = %v", err)
	}

	id, err := client.createStatus(context.Background(), "hola", nil)
	if err != nil {
		t.Fatalf("createStatus() error = %v", err)
	}
	if id != "legacy-1" {
		t.Fatalf("id = %q, want %q", id, "legacy-1")
	}
}
