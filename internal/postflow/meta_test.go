package postflow

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

	"github.com/antoniolg/postflow/internal/domain"
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
	var gotPermalinkLookup bool
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
		case "/v1.0/fb_post_1":
			gotPermalinkLookup = true
			_, _ = w.Write([]byte(`{"permalink_url":"https://facebook.example/posts/fb_post_1"}`))
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
	publishResult, err := provider.Publish(context.Background(), domain.SocialAccount{
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
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if publishResult.ExternalID != "fb_post_1" {
		t.Fatalf("unexpected external id %q", publishResult.ExternalID)
	}
	if publishResult.PublishedURL != "https://facebook.example/posts/fb_post_1" {
		t.Fatalf("unexpected published url %q", publishResult.PublishedURL)
	}
	if !gotPhotoUpload || !gotFeedPost {
		t.Fatalf("expected both photo upload and feed publish calls")
	}
	if !gotPermalinkLookup {
		t.Fatalf("expected permalink lookup after publish")
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
	publishResult, err := provider.Publish(context.Background(), domain.SocialAccount{
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
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if publishResult.ExternalID != "video_1" {
		t.Fatalf("unexpected external id %q", publishResult.ExternalID)
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
	var gotPermalinkLookup bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/ig_1/media":
			body, _ := io.ReadAll(r.Body)
			values, _ := url.ParseQuery(string(body))
			createdWithURL = strings.TrimSpace(values.Get("image_url"))
			_, _ = w.Write([]byte(`{"id":"ig_container_1"}`))
		case "/v1.0/ig_1/media_publish":
			_, _ = w.Write([]byte(`{"id":"ig_post_1"}`))
		case "/v1.0/ig_post_1":
			gotPermalinkLookup = true
			_, _ = w.Write([]byte(`{"permalink":"https://instagram.example/p/ig_post_1/"}`))
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
	publishResult, err := provider.Publish(context.Background(), domain.SocialAccount{
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
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if publishResult.ExternalID != "ig_post_1" {
		t.Fatalf("unexpected external id %q", publishResult.ExternalID)
	}
	if publishResult.PublishedURL != "https://instagram.example/p/ig_post_1/" {
		t.Fatalf("unexpected published url %q", publishResult.PublishedURL)
	}
	if createdWithURL != expectedURL {
		t.Fatalf("expected image_url %q, got %q", expectedURL, createdWithURL)
	}
	if !gotPermalinkLookup {
		t.Fatalf("expected permalink lookup after instagram publish")
	}
}

func TestInstagramPublishVideoUsesMediaURLBuilder(t *testing.T) {
	var createdWithURL string
	var createdMediaType string
	var checkedContainerStatus bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/ig_1/media":
			body, _ := io.ReadAll(r.Body)
			values, _ := url.ParseQuery(string(body))
			createdWithURL = strings.TrimSpace(values.Get("video_url"))
			createdMediaType = strings.TrimSpace(values.Get("media_type"))
			_, _ = w.Write([]byte(`{"id":"ig_container_1"}`))
		case "/v1.0/ig_container_1":
			checkedContainerStatus = true
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
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
	publishResult, err := provider.Publish(context.Background(), domain.SocialAccount{
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
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if publishResult.ExternalID != "ig_post_1" {
		t.Fatalf("unexpected external id %q", publishResult.ExternalID)
	}
	if createdWithURL != expectedURL {
		t.Fatalf("expected video_url %q, got %q", expectedURL, createdWithURL)
	}
	if createdMediaType != "REELS" {
		t.Fatalf("expected media_type REELS, got %q", createdMediaType)
	}
	if !checkedContainerStatus {
		t.Fatalf("expected container status check before publish")
	}
}

func TestInstagramPublishIgnoresInvalidCredentialMediaURLWhenBuilderIsValid(t *testing.T) {
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

	expectedURL := "https://cdn.example.com/med_ig_2.jpg"
	provider := NewInstagramProvider(MetaProviderConfig{
		GraphURL:   server.URL,
		APIVersion: "v1.0",
		MediaURLBuilder: func(media domain.Media) (string, error) {
			return fmt.Sprintf("https://cdn.example.com/%s.jpg", strings.TrimSpace(media.ID)), nil
		},
	})
	_, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformInstagram,
		ExternalAccountID: "ig_1",
	}, Credentials{
		AccessToken: "token-1",
		Extra: map[string]string{
			"image_url": "http://localhost:8080/private.jpg",
		},
	}, domain.Post{
		Text: "hello ig",
		Media: []domain.Media{{
			ID:          "med_ig_2",
			StoragePath: "/tmp/not-public.jpg",
			MimeType:    "image/jpeg",
		}},
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if createdWithURL != expectedURL {
		t.Fatalf("expected image_url %q, got %q", expectedURL, createdWithURL)
	}
}

func TestInstagramPublishRejectsNonPublicMediaURLFromBuilder(t *testing.T) {
	var mediaCreateCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.0/ig_1/media" {
			mediaCreateCalled = true
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	provider := NewInstagramProvider(MetaProviderConfig{
		GraphURL:   server.URL,
		APIVersion: "v1.0",
		MediaURLBuilder: func(media domain.Media) (string, error) {
			return "http://localhost:8080/media/" + strings.TrimSpace(media.ID), nil
		},
	})
	_, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformInstagram,
		ExternalAccountID: "ig_1",
	}, Credentials{
		AccessToken: "token-1",
	}, domain.Post{
		Text: "hello ig",
		Media: []domain.Media{{
			ID:          "med_ig_3",
			StoragePath: "/tmp/not-public.jpg",
			MimeType:    "image/jpeg",
		}},
	}, PublishOptions{})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "not publicly reachable") {
		t.Fatalf("expected non-public media url error, got %v", err)
	}
	if mediaCreateCalled {
		t.Fatalf("did not expect create media request when media url is non-public")
	}
}

func TestInstagramPublishAllowsPNGImage(t *testing.T) {
	provider := NewInstagramProvider(MetaProviderConfig{})
	_, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformInstagram,
		ExternalAccountID: "ig_1",
	}, Credentials{
		AccessToken: "token-1",
	}, domain.Post{
		Text: "hello ig",
		Media: []domain.Media{{
			ID:          "med_ig_png",
			StoragePath: "/tmp/not-public.png",
			MimeType:    "image/png",
		}},
	}, PublishOptions{})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "public image url") {
		t.Fatalf("expected publish flow to proceed past format validation, got %v", err)
	}
}
