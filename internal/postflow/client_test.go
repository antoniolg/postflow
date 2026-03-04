package postflow

import (
	"context"
	"strings"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
)

type fakeOAuthProvider struct {
	platform domain.Platform
}

func (f fakeOAuthProvider) Platform() domain.Platform {
	return f.platform
}

func (f fakeOAuthProvider) ValidateDraft(context.Context, domain.SocialAccount, Draft) ([]string, error) {
	return nil, nil
}

func (f fakeOAuthProvider) Publish(context.Context, domain.SocialAccount, Credentials, domain.Post, PublishOptions) (string, error) {
	return "external-id", nil
}

func (f fakeOAuthProvider) RefreshIfNeeded(context.Context, domain.SocialAccount, Credentials) (Credentials, bool, error) {
	return Credentials{}, false, nil
}

func (f fakeOAuthProvider) StartOAuth(context.Context, OAuthStartInput) (OAuthStartOutput, error) {
	return OAuthStartOutput{AuthURL: "https://example.com/oauth"}, nil
}

func (f fakeOAuthProvider) HandleOAuthCallback(context.Context, OAuthCallbackInput) ([]ConnectedAccount, error) {
	return []ConnectedAccount{{Platform: f.platform}}, nil
}

func TestProviderRegistryGetAndGetOAuth(t *testing.T) {
	registry := NewProviderRegistry(
		nil,
		NewMockProvider(domain.PlatformX),
		fakeOAuthProvider{platform: domain.PlatformLinkedIn},
	)

	if _, ok := registry.Get(domain.PlatformX); !ok {
		t.Fatalf("expected provider for x platform")
	}
	if _, ok := registry.Get(domain.PlatformFacebook); ok {
		t.Fatalf("did not expect provider for facebook")
	}
	if _, ok := registry.GetOAuth(domain.PlatformLinkedIn); !ok {
		t.Fatalf("expected oauth provider for linkedin")
	}
	if _, ok := registry.GetOAuth(domain.PlatformX); ok {
		t.Fatalf("did not expect oauth provider for x mock provider")
	}

	var nilRegistry *ProviderRegistry
	if _, ok := nilRegistry.Get(domain.PlatformX); ok {
		t.Fatalf("expected nil registry get to return false")
	}
	if _, ok := nilRegistry.GetOAuth(domain.PlatformX); ok {
		t.Fatalf("expected nil registry get oauth to return false")
	}
}

func TestMockProviderDefaultsAndOperations(t *testing.T) {
	mockDefault := NewMockProvider("")
	if mockDefault.Platform() != domain.PlatformX {
		t.Fatalf("expected default mock platform to be x, got %s", mockDefault.Platform())
	}

	mockLinkedIn := NewMockProvider(domain.PlatformLinkedIn)
	if mockLinkedIn.Platform() != domain.PlatformLinkedIn {
		t.Fatalf("expected mock platform linkedin, got %s", mockLinkedIn.Platform())
	}

	warnings, err := mockLinkedIn.ValidateDraft(context.Background(), domain.SocialAccount{}, Draft{})
	if err != nil {
		t.Fatalf("validate draft: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %#v", warnings)
	}

	publishedID, err := mockLinkedIn.Publish(context.Background(), domain.SocialAccount{}, Credentials{}, domain.Post{Platform: domain.PlatformLinkedIn}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !strings.HasPrefix(publishedID, "mock_linkedin_") {
		t.Fatalf("expected mock published id prefix, got %q", publishedID)
	}

	original := Credentials{AccessToken: "tok_1", RefreshToken: "ref_1"}
	refreshed, changed, err := mockLinkedIn.RefreshIfNeeded(context.Background(), domain.SocialAccount{}, original)
	if err != nil {
		t.Fatalf("refresh if needed: %v", err)
	}
	if changed {
		t.Fatalf("expected changed=false")
	}
	if refreshed.AccessToken != original.AccessToken || refreshed.RefreshToken != original.RefreshToken {
		t.Fatalf("expected credentials to remain unchanged, got %#v", refreshed)
	}
}
