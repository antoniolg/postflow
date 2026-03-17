package postflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestLinkedInValidateDraftRules(t *testing.T) {
	provider := NewLinkedInProvider(LinkedInProviderConfig{})
	if provider.Platform() != domain.PlatformLinkedIn {
		t.Fatalf("expected linkedin platform, got %s", provider.Platform())
	}

	tooManyImages := make([]domain.Media, 0, 10)
	for i := 0; i < 10; i++ {
		tooManyImages = append(tooManyImages, domain.Media{
			ID:           "img_" + string(rune('a'+i)),
			OriginalName: "img.png",
			MimeType:     "image/png",
		})
	}

	testCases := []struct {
		name       string
		draft      Draft
		wantErrSub string
	}{
		{
			name:       "rejects too many images",
			draft:      Draft{Text: "hello", Media: tooManyImages},
			wantErrSub: "up to 9",
		},
		{
			name: "rejects mixed image and video",
			draft: Draft{Text: "hello", Media: []domain.Media{
				{ID: "img_1", OriginalName: "img.jpg", MimeType: "image/jpeg"},
				{ID: "vid_1", OriginalName: "vid.mp4", MimeType: "video/mp4"},
			}},
			wantErrSub: "mixing images and video",
		},
		{
			name: "rejects unsupported media",
			draft: Draft{Text: "hello", Media: []domain.Media{
				{ID: "doc_1", OriginalName: "doc.pdf", MimeType: "application/pdf"},
			}},
			wantErrSub: "requires image or video",
		},
		{
			name: "accepts one video",
			draft: Draft{Text: "hello", Media: []domain.Media{
				{ID: "vid_1", OriginalName: "vid.mp4", MimeType: "video/mp4"},
			}},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := provider.ValidateDraft(context.Background(), domain.SocialAccount{Platform: domain.PlatformLinkedIn}, tc.draft)
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
}

func TestLinkedInPublishCommentMode(t *testing.T) {
	var sawCommentEndpoint bool
	targetURN := "urn:li:ugcPost:7096760097833439232"
	encodedTargetURN := url.PathEscape(targetURN)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != "/v2/socialActions/"+encodedTargetURN+"/comments" {
			http.NotFound(w, r)
			return
		}
		sawCommentEndpoint = true
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "Bearer token-1" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		if got := strings.TrimSpace(r.Header.Get("Linkedin-Version")); got != "" {
			t.Fatalf("did not expect Linkedin-Version on v2 comment endpoint, got %q", got)
		}
		if got := strings.TrimSpace(r.Header.Get("X-Restli-Protocol-Version")); got != "" {
			t.Fatalf("did not expect X-Restli-Protocol-Version on v2 comment endpoint, got %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode comment payload: %v", err)
		}
		if actor, _ := payload["actor"].(string); strings.TrimSpace(actor) != "urn:li:person:member_1" {
			t.Fatalf("unexpected actor %q", actor)
		}
		if object, _ := payload["object"].(string); strings.TrimSpace(object) != targetURN {
			t.Fatalf("unexpected object %q", object)
		}
		if _, hasParentComment := payload["parentComment"]; hasParentComment {
			t.Fatalf("did not expect parentComment for root post comment")
		}
		msg, _ := payload["message"].(map[string]any)
		if text, _ := msg["text"].(string); strings.TrimSpace(text) != "comment text" {
			t.Fatalf("unexpected comment text %q", text)
		}
		_, _ = w.Write([]byte(`{"id":"li_comment_1","commentUrn":"urn:li:comment:(urn:li:ugcPost:7096760097833439232,li_comment_1)","object":"urn:li:ugcPost:7096760097833439232"}`))
	}))
	defer server.Close()

	provider := NewLinkedInProvider(LinkedInProviderConfig{APIBaseURL: server.URL})
	publishResult, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "comment text",
	}, PublishOptions{
		Mode:             PublishModeComment,
		ParentExternalID: encodedTargetURN,
	})
	if err != nil {
		t.Fatalf("publish comment: %v", err)
	}
	if !sawCommentEndpoint {
		t.Fatalf("expected linkedin comment endpoint call")
	}
	if publishResult.ExternalID != "urn:li:comment:(urn:li:ugcPost:7096760097833439232,li_comment_1)" {
		t.Fatalf("unexpected external id %q", publishResult.ExternalID)
	}
}

func TestLinkedInPublishCommentModeUsesRestliHeaderID(t *testing.T) {
	targetURN := "urn:li:ugcPost:7096760097833439232"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != "/v2/socialActions/"+url.PathEscape(targetURN)+"/comments" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("x-restli-id", "7100646796353826816")
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	provider := NewLinkedInProvider(LinkedInProviderConfig{APIBaseURL: server.URL})
	publishResult, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "comment text",
	}, PublishOptions{
		Mode:             PublishModeComment,
		ParentExternalID: targetURN,
	})
	if err != nil {
		t.Fatalf("publish comment with restli header: %v", err)
	}
	if publishResult.ExternalID != "urn:li:comment:(urn:li:ugcPost:7096760097833439232,7100646796353826816)" {
		t.Fatalf("unexpected external id %q", publishResult.ExternalID)
	}
}

func TestLinkedInPublishNestedCommentModeUsesParentComment(t *testing.T) {
	parentCommentURN := "urn:li:comment:(urn:li:ugcPost:7096760097833439232,7100646796353826816)"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != "/v2/socialActions/"+url.PathEscape(parentCommentURN)+"/comments" {
			http.NotFound(w, r)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode nested comment payload: %v", err)
		}
		if object, _ := payload["object"].(string); strings.TrimSpace(object) != "urn:li:ugcPost:7096760097833439232" {
			t.Fatalf("unexpected nested comment object %q", object)
		}
		if parentComment, _ := payload["parentComment"].(string); strings.TrimSpace(parentComment) != parentCommentURN {
			t.Fatalf("unexpected parentComment %q", parentComment)
		}
		_, _ = w.Write([]byte(`{"commentUrn":"urn:li:comment:(urn:li:ugcPost:7096760097833439232,7100646796353826817)","object":"urn:li:ugcPost:7096760097833439232"}`))
	}))
	defer server.Close()

	provider := NewLinkedInProvider(LinkedInProviderConfig{APIBaseURL: server.URL})
	publishResult, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "nested comment text",
	}, PublishOptions{
		Mode:             PublishModeComment,
		ParentExternalID: parentCommentURN,
	})
	if err != nil {
		t.Fatalf("publish nested comment: %v", err)
	}
	if publishResult.ExternalID != "urn:li:comment:(urn:li:ugcPost:7096760097833439232,7100646796353826817)" {
		t.Fatalf("unexpected nested comment external id %q", publishResult.ExternalID)
	}
}

func TestLinkedInPublishCommentModeAcceptsShareURNTargets(t *testing.T) {
	targetURN := "urn:li:share:7435691467278331904"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != "/v2/socialActions/"+url.PathEscape(targetURN)+"/comments" {
			http.NotFound(w, r)
			return
		}
		if got := strings.TrimSpace(r.Header.Get("Linkedin-Version")); got != "" {
			t.Fatalf("did not expect Linkedin-Version on v2 comment endpoint, got %q", got)
		}
		if got := strings.TrimSpace(r.Header.Get("X-Restli-Protocol-Version")); got != "" {
			t.Fatalf("did not expect X-Restli-Protocol-Version on v2 comment endpoint, got %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode share comment payload: %v", err)
		}
		if object, _ := payload["object"].(string); strings.TrimSpace(object) != targetURN {
			t.Fatalf("unexpected share comment object %q", object)
		}
		w.Header().Set("x-restli-id", "7435691469999999999")
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	provider := NewLinkedInProvider(LinkedInProviderConfig{APIBaseURL: server.URL})
	publishResult, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "share comment text",
	}, PublishOptions{
		Mode:             PublishModeComment,
		ParentExternalID: targetURN,
	})
	if err != nil {
		t.Fatalf("publish comment on share urn: %v", err)
	}
	if publishResult.ExternalID != "urn:li:comment:(urn:li:share:7435691467278331904,7435691469999999999)" {
		t.Fatalf("unexpected external id %q", publishResult.ExternalID)
	}
}

func TestLinkedInPublishCommentModeUsesOrganizationActor(t *testing.T) {
	targetURN := "urn:li:ugcPost:7096760097833439232"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != "/v2/socialActions/"+url.PathEscape(targetURN)+"/comments" {
			http.NotFound(w, r)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode organization comment payload: %v", err)
		}
		if actor, _ := payload["actor"].(string); strings.TrimSpace(actor) != "urn:li:organization:org_1" {
			t.Fatalf("unexpected actor %q", actor)
		}
		_, _ = w.Write([]byte(`{"id":"li_comment_org_1","object":"urn:li:ugcPost:7096760097833439232"}`))
	}))
	defer server.Close()

	provider := NewLinkedInProvider(LinkedInProviderConfig{APIBaseURL: server.URL})
	publishResult, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		AccountKind:       domain.AccountKindOrganization,
		ExternalAccountID: "org_1",
	}, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "organization comment",
	}, PublishOptions{
		Mode:             PublishModeComment,
		ParentExternalID: targetURN,
	})
	if err != nil {
		t.Fatalf("publish organization comment: %v", err)
	}
	if publishResult.ExternalID != "urn:li:comment:(urn:li:ugcPost:7096760097833439232,li_comment_org_1)" {
		t.Fatalf("unexpected external id %q", publishResult.ExternalID)
	}
}

func TestLinkedInRefreshOAuthAndCallbackFlows(t *testing.T) {
	t.Run("refreshes expiring token", func(t *testing.T) {
		var refreshCalls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/oauth/v2/accessToken" {
				http.NotFound(w, r)
				return
			}
			refreshCalls++
			_, _ = w.Write([]byte(`{"access_token":"new-li-token","refresh_token":"new-refresh","expires_in":3600,"scope":"w_member_social","token_type":"Bearer"}`))
		}))
		defer server.Close()

		provider := NewLinkedInProvider(LinkedInProviderConfig{
			AuthBaseURL:  server.URL,
			ClientID:     "client-id",
			ClientSecret: "client-secret",
		})
		expiresSoon := time.Now().UTC().Add(1 * time.Minute)
		updated, changed, err := provider.RefreshIfNeeded(context.Background(), domain.SocialAccount{}, Credentials{
			AccessToken:  "old-li-token",
			RefreshToken: "old-refresh",
			ExpiresAt:    &expiresSoon,
		})
		if err != nil {
			t.Fatalf("refresh if needed: %v", err)
		}
		if !changed {
			t.Fatalf("expected credentials to be refreshed")
		}
		if updated.AccessToken != "new-li-token" || updated.RefreshToken != "new-refresh" {
			t.Fatalf("unexpected refreshed credentials: %+v", updated)
		}
		if refreshCalls != 1 {
			t.Fatalf("expected one refresh call, got %d", refreshCalls)
		}
	})

	t.Run("start oauth validates config and builds authorization URL", func(t *testing.T) {
		provider := NewLinkedInProvider(LinkedInProviderConfig{})
		if _, err := provider.StartOAuth(context.Background(), OAuthStartInput{State: "s", RedirectURL: "https://app/callback"}); err == nil {
			t.Fatalf("expected oauth start to fail without credentials")
		}

		provider = NewLinkedInProvider(LinkedInProviderConfig{
			ClientID:     "client-id",
			ClientSecret: "client-secret",
			AuthBaseURL:  "https://auth.example.com",
		})
		out, err := provider.StartOAuth(context.Background(), OAuthStartInput{
			State:       "state-123",
			RedirectURL: "https://app.example.com/callback",
		})
		if err != nil {
			t.Fatalf("start oauth: %v", err)
		}
		parsed, err := url.Parse(out.AuthURL)
		if err != nil {
			t.Fatalf("parse auth url: %v", err)
		}
		if parsed.Host != "auth.example.com" || parsed.Path != "/oauth/v2/authorization" {
			t.Fatalf("unexpected auth url %q", out.AuthURL)
		}
		query := parsed.Query()
		if query.Get("client_id") != "client-id" || query.Get("state") != "state-123" {
			t.Fatalf("unexpected query in auth url: %s", parsed.RawQuery)
		}
		scope := strings.Fields(query.Get("scope"))
		expectedScopes := []string{"openid", "profile", "w_member_social", "rw_organization_admin", "w_organization_social"}
		for _, expected := range expectedScopes {
			if !slices.Contains(scope, expected) {
				t.Fatalf("expected oauth scope %q in auth url, got %q", expected, query.Get("scope"))
			}
		}
	})

	t.Run("callback falls back to /me when /userinfo fails", func(t *testing.T) {
		var meCalls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/oauth/v2/accessToken":
				_, _ = w.Write([]byte(`{"access_token":"li-access","refresh_token":"li-refresh","expires_in":3600,"scope":"w_member_social","token_type":"Bearer"}`))
			case "/v2/userinfo":
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"invalid token"}`))
			case "/v2/me":
				meCalls++
				_, _ = w.Write([]byte(`{"id":"member_42","localizedFirstName":"Ada","localizedLastName":"Lovelace"}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		provider := NewLinkedInProvider(LinkedInProviderConfig{
			ClientID:     "client-id",
			ClientSecret: "client-secret",
			AuthBaseURL:  server.URL,
			APIBaseURL:   server.URL,
		})
		accounts, err := provider.HandleOAuthCallback(context.Background(), OAuthCallbackInput{
			Code:        "oauth-code",
			RedirectURL: "https://app.example.com/callback",
		})
		if err != nil {
			t.Fatalf("handle oauth callback: %v", err)
		}
		if len(accounts) != 1 {
			t.Fatalf("expected one connected account, got %d", len(accounts))
		}
		if meCalls != 1 {
			t.Fatalf("expected one /v2/me fallback call, got %d", meCalls)
		}
		got := accounts[0]
		if got.Platform != domain.PlatformLinkedIn {
			t.Fatalf("expected linkedin platform, got %s", got.Platform)
		}
		if got.ExternalAccountID != "member_42" || got.DisplayName != "Ada Lovelace" {
			t.Fatalf("unexpected account details: %+v", got)
		}
		if strings.TrimSpace(got.Credentials.AccessToken) != "li-access" {
			t.Fatalf("unexpected oauth credentials: %+v", got.Credentials)
		}
	})

	t.Run("callback uses userinfo when available", func(t *testing.T) {
		var meCalls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/oauth/v2/accessToken":
				_, _ = io.WriteString(w, `{"access_token":"li-access","expires_in":3600,"scope":"openid,profile,w_member_social,rw_organization_admin,w_organization_social"}`)
			case "/v2/userinfo":
				_, _ = io.WriteString(w, `{"sub":"member_7","name":"Grace Hopper"}`)
			case "/rest/organizationAcls":
				_, _ = io.WriteString(w, `{"elements":[{"role":"ADMINISTRATOR","organizationTarget":"urn:li:organization:987"}],"paging":{"start":0,"count":100}}`)
			case "/rest/organizationsLookup":
				_, _ = io.WriteString(w, `{"results":{"987":{"localizedName":"Acme Co"}}}`)
			case "/v2/me":
				meCalls++
				_, _ = io.WriteString(w, `{"id":"member_legacy"}`)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		provider := NewLinkedInProvider(LinkedInProviderConfig{
			ClientID:     "client-id",
			ClientSecret: "client-secret",
			AuthBaseURL:  server.URL,
			APIBaseURL:   server.URL,
		})
		accounts, err := provider.HandleOAuthCallback(context.Background(), OAuthCallbackInput{
			Code:        "oauth-code",
			RedirectURL: "https://app.example.com/callback",
		})
		if err != nil {
			t.Fatalf("handle oauth callback with userinfo: %v", err)
		}
		if len(accounts) != 2 {
			t.Fatalf("expected personal and organization accounts, got %d", len(accounts))
		}
		if meCalls != 0 {
			t.Fatalf("expected no /v2/me calls when userinfo works, got %d", meCalls)
		}
		if accounts[0].AccountKind != domain.AccountKindPersonal || accounts[0].ExternalAccountID != "member_7" || accounts[0].DisplayName != "Grace Hopper" {
			t.Fatalf("unexpected personal account details: %+v", accounts[0])
		}
		if accounts[1].AccountKind != domain.AccountKindOrganization || accounts[1].ExternalAccountID != "987" || accounts[1].DisplayName != "Acme Co" {
			t.Fatalf("unexpected organization account details: %+v", accounts[1])
		}
	})
}

func TestLinkedInPublishCommentModeValidationErrors(t *testing.T) {
	provider := NewLinkedInProvider(LinkedInProviderConfig{APIBaseURL: "https://api.example.com"})
	account := domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}

	_, err := provider.Publish(context.Background(), account, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "comment with media",
		Media: []domain.Media{{
			ID:           "med_1",
			OriginalName: "img.png",
			MimeType:     "image/png",
		}},
	}, PublishOptions{
		Mode:             PublishModeComment,
		ParentExternalID: "root_post_1",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "do not support media") {
		t.Fatalf("expected comment mode media validation error, got %v", err)
	}

	_, err = provider.Publish(context.Background(), account, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "comment missing parent",
	}, PublishOptions{
		Mode: PublishModeComment,
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "parent external id is required") {
		t.Fatalf("expected missing parent error, got %v", err)
	}
}

func TestLinkedInOAuthCallbackFailsWhenProfileEndpointsFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/v2/accessToken":
			_, _ = io.WriteString(w, `{"access_token":"li-access","expires_in":3600}`)
		case "/v2/userinfo":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"message":"upstream error"}`)
		case "/v2/me":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"profile lookup error"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := NewLinkedInProvider(LinkedInProviderConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		AuthBaseURL:  server.URL,
		APIBaseURL:   server.URL,
	})
	_, err := provider.HandleOAuthCallback(context.Background(), OAuthCallbackInput{
		Code:        "oauth-code",
		RedirectURL: "https://app.example.com/callback",
	})
	if err == nil {
		t.Fatalf("expected oauth callback to fail when both profile endpoints fail")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "profile fetch failed") || !strings.Contains(msg, "userinfo_error") || !strings.Contains(msg, "me_error") {
		t.Fatalf("expected composed profile failure details, got %q", err.Error())
	}
}
