package postflow

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestFacebookPublishCommentMode(t *testing.T) {
	t.Run("publishes comment without media", func(t *testing.T) {
		var sawComment bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1.0/root_post_1/comments" {
				http.NotFound(w, r)
				return
			}
			sawComment = true
			body, _ := io.ReadAll(r.Body)
			values, _ := url.ParseQuery(string(body))
			if strings.TrimSpace(values.Get("message")) != "fb comment" {
				t.Fatalf("unexpected comment message %q", values.Get("message"))
			}
			_, _ = io.WriteString(w, `{"id":"fb_comment_1"}`)
		}))
		defer server.Close()

		provider := NewFacebookProvider(MetaProviderConfig{GraphURL: server.URL, APIVersion: "v1.0"})
		externalID, err := provider.Publish(context.Background(), domain.SocialAccount{
			Platform:          domain.PlatformFacebook,
			ExternalAccountID: "page_1",
		}, Credentials{
			AccessToken: "token-1",
		}, domain.Post{
			Text: "fb comment",
		}, PublishOptions{
			Mode:             PublishModeComment,
			ParentExternalID: "root_post_1",
		})
		if err != nil {
			t.Fatalf("publish facebook comment: %v", err)
		}
		if !sawComment {
			t.Fatalf("expected facebook comment endpoint call")
		}
		if externalID != "fb_comment_1" {
			t.Fatalf("unexpected external id %q", externalID)
		}
	})

	t.Run("rejects invalid comment mode input", func(t *testing.T) {
		provider := NewFacebookProvider(MetaProviderConfig{})
		account := domain.SocialAccount{
			Platform:          domain.PlatformFacebook,
			ExternalAccountID: "page_1",
		}
		_, err := provider.Publish(context.Background(), account, Credentials{AccessToken: "token-1"}, domain.Post{
			Text: "comment with media",
			Media: []domain.Media{{
				ID:           "med_1",
				OriginalName: "image.png",
				MimeType:     "image/png",
			}},
		}, PublishOptions{
			Mode:             PublishModeComment,
			ParentExternalID: "root_post_1",
		})
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "do not support media") {
			t.Fatalf("expected media validation error in comment mode, got %v", err)
		}

		_, err = provider.Publish(context.Background(), account, Credentials{AccessToken: "token-1"}, domain.Post{
			Text: "comment missing parent",
		}, PublishOptions{Mode: PublishModeComment})
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "parent external id is required") {
			t.Fatalf("expected missing parent external id error, got %v", err)
		}
	})
}

func TestInstagramPublishCommentModeValidation(t *testing.T) {
	provider := NewInstagramProvider(MetaProviderConfig{})
	account := domain.SocialAccount{
		Platform:          domain.PlatformInstagram,
		ExternalAccountID: "ig_1",
	}

	_, err := provider.Publish(context.Background(), account, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "ig comment with media",
		Media: []domain.Media{{
			ID:           "med_1",
			OriginalName: "image.png",
			MimeType:     "image/png",
		}},
	}, PublishOptions{
		Mode:             PublishModeComment,
		ParentExternalID: "root_post_1",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "do not support media") {
		t.Fatalf("expected media validation error in instagram comment mode, got %v", err)
	}

	_, err = provider.Publish(context.Background(), account, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "ig comment missing parent",
	}, PublishOptions{Mode: PublishModeComment})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "parent external id is required") {
		t.Fatalf("expected missing parent external id error, got %v", err)
	}
}

func TestInstagramVideoPublishFailsWhenContainerNotPublishable(t *testing.T) {
	var publishCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/ig_1/media":
			_, _ = io.WriteString(w, `{"id":"ig_container_1"}`)
		case "/v1.0/ig_container_1":
			_, _ = io.WriteString(w, `{"status_code":"ERROR","status":"ERROR"}`)
		case "/v1.0/ig_1/media_publish":
			publishCalled = true
			_, _ = io.WriteString(w, `{"id":"ig_post_1"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := NewInstagramProvider(MetaProviderConfig{
		GraphURL:   server.URL,
		APIVersion: "v1.0",
		MediaURLBuilder: func(media domain.Media) (string, error) {
			return "https://cdn.example.com/" + strings.TrimSpace(media.ID) + ".mp4", nil
		},
	})
	_, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformInstagram,
		ExternalAccountID: "ig_1",
	}, Credentials{
		AccessToken: "token-1",
	}, domain.Post{
		Text: "video post",
		Media: []domain.Media{{
			ID:           "med_ig_video",
			OriginalName: "video.mp4",
			MimeType:     "video/mp4",
		}},
	}, PublishOptions{})
	if err == nil {
		t.Fatalf("expected publish failure when container status is ERROR")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not publishable") {
		t.Fatalf("expected not publishable error, got %v", err)
	}
	if publishCalled {
		t.Fatalf("did not expect media_publish call when container is not publishable")
	}
}
