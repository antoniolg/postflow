package postflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestFormatPostTextForPublishKeepsPlainMarkdownText(t *testing.T) {
	got := formatPostTextForPublish("Hola **mundo** _equipo_")
	want := "Hola 𝗺𝘂𝗻𝗱𝗼 𝑒𝑞𝑢𝑖𝑝𝑜"
	if got != want {
		t.Fatalf("formatted text = %q, want %q", got, want)
	}
}

func TestFormatPostTextForPublishUnwrapsRTFToPlainText(t *testing.T) {
	got := formatPostTextForPublish("{\\rtf1\\ansi\\deff0 Prueba}")
	want := "Prueba"
	if got != want {
		t.Fatalf("formatted rtf text = %q, want %q", got, want)
	}
}

func TestXProviderPublishSendsPlainText(t *testing.T) {
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
		APIBaseURL:    srv.URL,
		UploadBaseURL: srv.URL,
	})

	publishResult, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform: domain.PlatformX,
	}, Credentials{
		AccessToken: "bearer-token",
	}, domain.Post{
		Platform: domain.PlatformX,
		Text:     "Hola **mundo** _equipo_",
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if publishResult.ExternalID != "x_post_1" {
		t.Fatalf("unexpected external id %q", publishResult.ExternalID)
	}
	if gotText != "Hola 𝗺𝘂𝗻𝗱𝗼 𝑒𝑞𝑢𝑖𝑝𝑜" {
		t.Fatalf("expected plain payload text, got %q", gotText)
	}
}
