package publisher

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/antoniolg/publisher/internal/domain"
)

func TestFormatPostTextForPublishConvertsMarkdownToRTF(t *testing.T) {
	got := formatPostTextForPublish("Hola **mundo** _equipo_")
	want := "{\\rtf1\\ansi\\deff0 Hola \\b mundo\\b0  \\i equipo\\i0 }"
	if got != want {
		t.Fatalf("formatted text = %q, want %q", got, want)
	}
}

func TestXProviderPublishSendsRTFText(t *testing.T) {
	var gotText string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2/tweets" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		gotText, _ = payload["text"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"x_post_1"}}`))
	}))
	defer srv.Close()

	provider := NewXProvider(XConfig{
		APIBaseURL:        srv.URL,
		UploadBaseURL:     srv.URL,
		APIKey:            "key",
		APIKeySecret:      "secret",
		AccessToken:       "token",
		AccessTokenSecret: "token_secret",
	})

	externalID, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform: domain.PlatformX,
	}, Credentials{
		AccessToken:       "token",
		AccessTokenSecret: "token_secret",
	}, domain.Post{
		Platform: domain.PlatformX,
		Text:     "Hola **mundo** _equipo_",
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if externalID != "x_post_1" {
		t.Fatalf("unexpected external id %q", externalID)
	}
	if !strings.HasPrefix(gotText, "{\\rtf1\\ansi\\deff0 ") {
		t.Fatalf("expected RTF prefix in payload text, got %q", gotText)
	}
	if !strings.Contains(gotText, "\\b mundo\\b0") {
		t.Fatalf("expected bold token in payload text, got %q", gotText)
	}
	if !strings.Contains(gotText, "\\i equipo\\i0") {
		t.Fatalf("expected italic token in payload text, got %q", gotText)
	}
}
