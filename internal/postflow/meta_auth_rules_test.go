package postflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestMetaValidateDraftRules(t *testing.T) {
	facebook := NewFacebookProvider(MetaProviderConfig{})
	instagram := NewInstagramProvider(MetaProviderConfig{})
	if facebook.Platform() != domain.PlatformFacebook {
		t.Fatalf("expected facebook platform, got %s", facebook.Platform())
	}
	if instagram.Platform() != domain.PlatformInstagram {
		t.Fatalf("expected instagram platform, got %s", instagram.Platform())
	}

	t.Run("facebook rejects invalid media combinations", func(t *testing.T) {
		tooMany := make([]domain.Media, 0, 11)
		for i := 0; i < 11; i++ {
			tooMany = append(tooMany, domain.Media{
				ID:           "img_" + strings.Repeat("x", i+1),
				OriginalName: "img.png",
				MimeType:     "image/png",
			})
		}
		cases := []struct {
			name       string
			draft      Draft
			wantErrSub string
		}{
			{
				name:       "too many attachments",
				draft:      Draft{Text: "fb", Media: tooMany},
				wantErrSub: "up to 10",
			},
			{
				name: "mix image and video",
				draft: Draft{Text: "fb", Media: []domain.Media{
					{OriginalName: "img.png", MimeType: "image/png"},
					{OriginalName: "vid.mp4", MimeType: "video/mp4"},
				}},
				wantErrSub: "mixing images and video",
			},
			{
				name: "unsupported media",
				draft: Draft{Text: "fb", Media: []domain.Media{
					{OriginalName: "doc.pdf", MimeType: "application/pdf"},
				}},
				wantErrSub: "requires image or video",
			},
			{
				name: "single video accepted",
				draft: Draft{Text: "fb", Media: []domain.Media{
					{OriginalName: "vid.mp4", MimeType: "video/mp4"},
				}},
			},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				_, err := facebook.ValidateDraft(context.Background(), domain.SocialAccount{}, tc.draft)
				if strings.TrimSpace(tc.wantErrSub) == "" {
					if err != nil {
						t.Fatalf("expected validation success, got %v", err)
					}
					return
				}
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErrSub)) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErrSub, err)
				}
			})
		}
	})

	t.Run("instagram requires exactly one image or video", func(t *testing.T) {
		cases := []struct {
			name       string
			draft      Draft
			wantErrSub string
		}{
			{
				name:       "no media",
				draft:      Draft{Text: "ig"},
				wantErrSub: "requires one media",
			},
			{
				name: "more than one media",
				draft: Draft{Text: "ig", Media: []domain.Media{
					{OriginalName: "a.png", MimeType: "image/png"},
					{OriginalName: "b.png", MimeType: "image/png"},
				}},
				wantErrSub: "single image or video",
			},
			{
				name: "unsupported media",
				draft: Draft{Text: "ig", Media: []domain.Media{
					{OriginalName: "doc.pdf", MimeType: "application/pdf"},
				}},
				wantErrSub: "requires image or video",
			},
			{
				name: "one image accepted",
				draft: Draft{Text: "ig", Media: []domain.Media{
					{OriginalName: "a.png", MimeType: "image/png"},
				}},
			},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				_, err := instagram.ValidateDraft(context.Background(), domain.SocialAccount{}, tc.draft)
				if strings.TrimSpace(tc.wantErrSub) == "" {
					if err != nil {
						t.Fatalf("expected validation success, got %v", err)
					}
					return
				}
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErrSub)) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErrSub, err)
				}
			})
		}
	})
}

func TestMetaOAuthStartAndCallbackFlows(t *testing.T) {
	t.Run("facebook and instagram start oauth include expected scopes", func(t *testing.T) {
		facebook := NewFacebookProvider(MetaProviderConfig{})
		if _, err := facebook.StartOAuth(context.Background(), OAuthStartInput{State: "s", RedirectURL: "https://app/callback"}); err == nil {
			t.Fatalf("expected facebook oauth start error without app credentials")
		}

		instagram := NewInstagramProvider(MetaProviderConfig{})
		if _, err := instagram.StartOAuth(context.Background(), OAuthStartInput{State: "s", RedirectURL: "https://app/callback"}); err == nil {
			t.Fatalf("expected instagram oauth start error without app credentials")
		}

		cfg := MetaProviderConfig{
			AppID:      "app-id",
			AppSecret:  "app-secret",
			DialogURL:  "https://dialog.example.com",
			APIVersion: "v1.0",
		}
		facebook = NewFacebookProvider(cfg)
		instagram = NewInstagramProvider(cfg)

		fbOut, err := facebook.StartOAuth(context.Background(), OAuthStartInput{
			State:       "fb-state",
			RedirectURL: "https://app.example.com/callback",
		})
		if err != nil {
			t.Fatalf("facebook start oauth: %v", err)
		}
		igOut, err := instagram.StartOAuth(context.Background(), OAuthStartInput{
			State:       "ig-state",
			RedirectURL: "https://app.example.com/callback",
		})
		if err != nil {
			t.Fatalf("instagram start oauth: %v", err)
		}

		fbURL, _ := url.Parse(fbOut.AuthURL)
		if fbURL.Path != "/v1.0/dialog/oauth" {
			t.Fatalf("unexpected facebook oauth path %q", fbURL.Path)
		}
		if !strings.Contains(fbURL.Query().Get("scope"), "pages_manage_posts") {
			t.Fatalf("expected facebook scope to include pages_manage_posts, got %q", fbURL.Query().Get("scope"))
		}

		igURL, _ := url.Parse(igOut.AuthURL)
		if igURL.Path != "/v1.0/dialog/oauth" {
			t.Fatalf("unexpected instagram oauth path %q", igURL.Path)
		}
		if !strings.Contains(igURL.Query().Get("scope"), "instagram_content_publish") {
			t.Fatalf("expected instagram scope to include instagram_content_publish, got %q", igURL.Query().Get("scope"))
		}
	})

	t.Run("facebook callback returns publishable pages", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1.0/oauth/access_token":
				_, _ = w.Write([]byte(`{"access_token":"user-token","token_type":"Bearer","expires_in":3600}`))
			case "/v1.0/me/accounts":
				_, _ = w.Write([]byte(`{"data":[{"id":"page_1","name":"Team Page","access_token":"page-token-1"},{"id":"page_2","name":"No Token"}]}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		provider := NewFacebookProvider(MetaProviderConfig{
			AppID:      "app-id",
			AppSecret:  "app-secret",
			GraphURL:   server.URL,
			APIVersion: "v1.0",
		})
		accounts, err := provider.HandleOAuthCallback(context.Background(), OAuthCallbackInput{
			Code:        "oauth-code",
			RedirectURL: "https://app.example.com/callback",
		})
		if err != nil {
			t.Fatalf("facebook oauth callback: %v", err)
		}
		if len(accounts) != 1 {
			t.Fatalf("expected 1 publishable facebook page, got %d", len(accounts))
		}
		got := accounts[0]
		if got.Platform != domain.PlatformFacebook || got.ExternalAccountID != "page_1" || got.DisplayName != "Team Page" {
			t.Fatalf("unexpected facebook connected account: %+v", got)
		}
		if strings.TrimSpace(got.Credentials.Extra["page_id"]) != "page_1" {
			t.Fatalf("expected page_id credential extra, got %+v", got.Credentials.Extra)
		}
	})

	t.Run("instagram callback returns business accounts and refresh wrapper works", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1.0/oauth/access_token":
				_, _ = w.Write([]byte(`{"access_token":"user-token","token_type":"Bearer","expires_in":3600}`))
			case "/v1.0/me/accounts":
				_, _ = w.Write([]byte(`{"data":[{"id":"page_1","name":"Team Page","access_token":"page-token-1","instagram_business_account":{"id":"ig_1","username":"ig-team"}},{"id":"page_2","name":"No IG","access_token":"page-token-2"}]}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		provider := NewInstagramProvider(MetaProviderConfig{
			AppID:      "app-id",
			AppSecret:  "app-secret",
			GraphURL:   server.URL,
			APIVersion: "v1.0",
		})
		accounts, err := provider.HandleOAuthCallback(context.Background(), OAuthCallbackInput{
			Code:        "oauth-code",
			RedirectURL: "https://app.example.com/callback",
		})
		if err != nil {
			t.Fatalf("instagram oauth callback: %v", err)
		}
		if len(accounts) != 1 {
			t.Fatalf("expected 1 publishable instagram business account, got %d", len(accounts))
		}
		got := accounts[0]
		if got.Platform != domain.PlatformInstagram || got.ExternalAccountID != "ig_1" || got.DisplayName != "ig-team" {
			t.Fatalf("unexpected instagram connected account: %+v", got)
		}
		if strings.TrimSpace(got.Credentials.Extra["ig_user_id"]) != "ig_1" || strings.TrimSpace(got.Credentials.Extra["page_id"]) != "page_1" {
			t.Fatalf("unexpected instagram credential extras: %+v", got.Credentials.Extra)
		}

		// Wrapper path in Instagram RefreshIfNeeded delegates to Facebook behavior.
		future := time.Now().UTC().Add(30 * time.Minute)
		updated, changed, err := provider.RefreshIfNeeded(context.Background(), domain.SocialAccount{}, Credentials{
			AccessToken: "token",
			ExpiresAt:   &future,
		})
		if err != nil {
			t.Fatalf("instagram refresh if needed: %v", err)
		}
		if changed {
			t.Fatalf("expected no refresh for non-expiring token")
		}
		if strings.TrimSpace(updated.AccessToken) != "token" {
			t.Fatalf("expected access token unchanged, got %q", updated.AccessToken)
		}
	})
}
