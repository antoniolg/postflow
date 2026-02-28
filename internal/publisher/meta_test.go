package publisher

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
)

func TestFacebookRefreshIfNeededRefreshesExpiringToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/oauth/access_token" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"new-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer server.Close()

	provider := NewFacebookProvider(MetaProviderConfig{
		AppID:      "app-id",
		AppSecret:  "app-secret",
		GraphURL:   server.URL,
		APIVersion: "v1.0",
	})
	expires := time.Now().UTC().Add(1 * time.Minute)
	updated, changed, err := provider.RefreshIfNeeded(context.Background(), domain.SocialAccount{}, Credentials{
		AccessToken: "old-token",
		ExpiresAt:   &expires,
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !changed {
		t.Fatalf("expected credentials to change")
	}
	if updated.AccessToken != "new-token" {
		t.Fatalf("unexpected access token %q", updated.AccessToken)
	}
	if updated.ExpiresAt == nil {
		t.Fatalf("expected refreshed expiration")
	}
}

func TestFacebookPublishWithImageAttachment(t *testing.T) {
	var gotPhotoUpload bool
	var gotFeedPost bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/page_1/photos":
			gotPhotoUpload = true
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				t.Fatalf("parse multipart: %v", err)
			}
			if token := strings.TrimSpace(r.FormValue("access_token")); token != "token-1" {
				t.Fatalf("unexpected photo upload token %q", token)
			}
			_, _ = w.Write([]byte(`{"id":"photo_1"}`))
		case "/v1.0/page_1/feed":
			gotFeedPost = true
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			if got := strings.TrimSpace(r.FormValue("attached_media[0]")); !strings.Contains(got, "photo_1") {
				t.Fatalf("expected attachment to reference photo_1, got %q", got)
			}
			_, _ = w.Write([]byte(`{"id":"fb_post_1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	mediaPath := filepath.Join(t.TempDir(), "image.jpg")
	if err := os.WriteFile(mediaPath, []byte("fake-image"), 0o644); err != nil {
		t.Fatalf("write media: %v", err)
	}
	provider := NewFacebookProvider(MetaProviderConfig{
		GraphURL:   server.URL,
		APIVersion: "v1.0",
	})
	externalID, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformFacebook,
		ExternalAccountID: "page_1",
	}, Credentials{
		AccessToken: "token-1",
	}, domain.Post{
		Text: "hello fb",
		Media: []domain.Media{{
			ID:           "med_1",
			Kind:         "image",
			OriginalName: "image.jpg",
			StoragePath:  mediaPath,
			MimeType:     "image/jpeg",
		}},
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if externalID != "fb_post_1" {
		t.Fatalf("unexpected external id %q", externalID)
	}
	if !gotPhotoUpload || !gotFeedPost {
		t.Fatalf("expected both photo upload and feed publish calls")
	}
}

func TestFacebookPublishWithVideoAttachment(t *testing.T) {
	var gotVideoUpload bool
	var gotFeedPost bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/page_1/videos":
			gotVideoUpload = true
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				t.Fatalf("parse multipart: %v", err)
			}
			if token := strings.TrimSpace(r.FormValue("access_token")); token != "token-1" {
				t.Fatalf("unexpected video upload token %q", token)
			}
			_, _ = w.Write([]byte(`{"id":"video_1"}`))
		case "/v1.0/page_1/feed":
			gotFeedPost = true
			_, _ = w.Write([]byte(`{"id":"fb_post_1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	mediaPath := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(mediaPath, []byte("fake-video"), 0o644); err != nil {
		t.Fatalf("write media: %v", err)
	}
	provider := NewFacebookProvider(MetaProviderConfig{
		GraphURL:   server.URL,
		APIVersion: "v1.0",
	})
	externalID, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformFacebook,
		ExternalAccountID: "page_1",
	}, Credentials{
		AccessToken: "token-1",
	}, domain.Post{
		Text: "hello fb video",
		Media: []domain.Media{{
			ID:           "med_v_1",
			Kind:         "video",
			OriginalName: "video.mp4",
			StoragePath:  mediaPath,
			MimeType:     "video/mp4",
		}},
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if externalID != "video_1" {
		t.Fatalf("unexpected external id %q", externalID)
	}
	if !gotVideoUpload {
		t.Fatalf("expected video upload call")
	}
	if gotFeedPost {
		t.Fatalf("did not expect feed post call for video publish")
	}
}

func TestInstagramPublishUsesMediaURLBuilder(t *testing.T) {
	var createdWithURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/ig_1/media":
			body, _ := io.ReadAll(r.Body)
			values, _ := url.ParseQuery(string(body))
			createdWithURL = strings.TrimSpace(values.Get("image_url"))
			_, _ = w.Write([]byte(`{"id":"ig_container_1"}`))
		case "/v1.0/ig_1/media_publish":
			_, _ = w.Write([]byte(`{"id":"ig_post_1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	expectedURL := "https://cdn.example.com/med_ig_1.jpg"
	provider := NewInstagramProvider(MetaProviderConfig{
		GraphURL:   server.URL,
		APIVersion: "v1.0",
		MediaURLBuilder: func(media domain.Media) (string, error) {
			return fmt.Sprintf("https://cdn.example.com/%s.jpg", strings.TrimSpace(media.ID)), nil
		},
	})
	externalID, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformInstagram,
		ExternalAccountID: "ig_1",
	}, Credentials{
		AccessToken: "token-1",
	}, domain.Post{
		Text: "hello ig",
		Media: []domain.Media{{
			ID:          "med_ig_1",
			StoragePath: "/tmp/not-public.jpg",
			MimeType:    "image/jpeg",
		}},
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if externalID != "ig_post_1" {
		t.Fatalf("unexpected external id %q", externalID)
	}
	if createdWithURL != expectedURL {
		t.Fatalf("expected image_url %q, got %q", expectedURL, createdWithURL)
	}
}

func TestInstagramPublishVideoUsesMediaURLBuilder(t *testing.T) {
	var createdWithURL string
	var createdMediaType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/ig_1/media":
			body, _ := io.ReadAll(r.Body)
			values, _ := url.ParseQuery(string(body))
			createdWithURL = strings.TrimSpace(values.Get("video_url"))
			createdMediaType = strings.TrimSpace(values.Get("media_type"))
			_, _ = w.Write([]byte(`{"id":"ig_container_1"}`))
		case "/v1.0/ig_1/media_publish":
			_, _ = w.Write([]byte(`{"id":"ig_post_1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	expectedURL := "https://cdn.example.com/med_ig_v_1.mp4"
	provider := NewInstagramProvider(MetaProviderConfig{
		GraphURL:   server.URL,
		APIVersion: "v1.0",
		MediaURLBuilder: func(media domain.Media) (string, error) {
			return fmt.Sprintf("https://cdn.example.com/%s.mp4", strings.TrimSpace(media.ID)), nil
		},
	})
	externalID, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformInstagram,
		ExternalAccountID: "ig_1",
	}, Credentials{
		AccessToken: "token-1",
	}, domain.Post{
		Text: "hello ig video",
		Media: []domain.Media{{
			ID:          "med_ig_v_1",
			StoragePath: "/tmp/not-public.mp4",
			MimeType:    "video/mp4",
		}},
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if externalID != "ig_post_1" {
		t.Fatalf("unexpected external id %q", externalID)
	}
	if createdWithURL != expectedURL {
		t.Fatalf("expected video_url %q, got %q", expectedURL, createdWithURL)
	}
	if createdMediaType != "REELS" {
		t.Fatalf("expected media_type REELS, got %q", createdMediaType)
	}
}
