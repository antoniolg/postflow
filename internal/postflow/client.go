package postflow

import (
	"context"
	"fmt"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

type Draft struct {
	Text  string
	Media []domain.Media
}

type PublishMode string

const (
	PublishModeRoot    PublishMode = "root"
	PublishModeReply   PublishMode = "reply"
	PublishModeComment PublishMode = "comment"
)

type PublishOptions struct {
	Mode             PublishMode
	ParentExternalID string
}

type Credentials struct {
	AccessToken       string            `json:"access_token,omitempty"`
	AccessTokenSecret string            `json:"access_token_secret,omitempty"`
	RefreshToken      string            `json:"refresh_token,omitempty"`
	ExpiresAt         *time.Time        `json:"expires_at,omitempty"`
	Scope             string            `json:"scope,omitempty"`
	TokenType         string            `json:"token_type,omitempty"`
	Extra             map[string]string `json:"extra,omitempty"`
}

type ConnectedAccount struct {
	Platform          domain.Platform
	AccountKind       domain.AccountKind
	DisplayName       string
	ExternalAccountID string
	Credentials       Credentials
}

type OAuthStartInput struct {
	State        string
	CodeVerifier string
	RedirectURL  string
}

type OAuthStartOutput struct {
	AuthURL      string
	CodeVerifier string
}

type OAuthCallbackInput struct {
	Code         string
	State        string
	CodeVerifier string
	RedirectURL  string
}

type Provider interface {
	Platform() domain.Platform
	ValidateDraft(ctx context.Context, account domain.SocialAccount, draft Draft) ([]string, error)
	Publish(ctx context.Context, account domain.SocialAccount, credentials Credentials, post domain.Post, opts PublishOptions) (string, error)
	RefreshIfNeeded(ctx context.Context, account domain.SocialAccount, credentials Credentials) (Credentials, bool, error)
}

type OAuthProvider interface {
	Provider
	StartOAuth(ctx context.Context, in OAuthStartInput) (OAuthStartOutput, error)
	HandleOAuthCallback(ctx context.Context, in OAuthCallbackInput) ([]ConnectedAccount, error)
}

type ProviderRegistry struct {
	providers map[domain.Platform]Provider
}

func NewProviderRegistry(providers ...Provider) *ProviderRegistry {
	registry := &ProviderRegistry{providers: make(map[domain.Platform]Provider, len(providers))}
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		registry.providers[provider.Platform()] = provider
	}
	return registry
}

func (r *ProviderRegistry) Get(platform domain.Platform) (Provider, bool) {
	if r == nil {
		return nil, false
	}
	p, ok := r.providers[platform]
	return p, ok
}

func (r *ProviderRegistry) GetOAuth(platform domain.Platform) (OAuthProvider, bool) {
	provider, ok := r.Get(platform)
	if !ok {
		return nil, false
	}
	oauthProvider, ok := provider.(OAuthProvider)
	return oauthProvider, ok
}

type MockProvider struct {
	platform domain.Platform
}

func NewMockProvider(platform domain.Platform) MockProvider {
	if platform == "" {
		platform = domain.PlatformX
	}
	return MockProvider{platform: platform}
}

func (m MockProvider) Platform() domain.Platform {
	return m.platform
}

func (m MockProvider) ValidateDraft(_ context.Context, _ domain.SocialAccount, _ Draft) ([]string, error) {
	return nil, nil
}

func (m MockProvider) Publish(_ context.Context, _ domain.SocialAccount, _ Credentials, post domain.Post, _ PublishOptions) (string, error) {
	return fmt.Sprintf("mock_%s_%d", post.Platform, time.Now().Unix()), nil
}

func (m MockProvider) RefreshIfNeeded(_ context.Context, _ domain.SocialAccount, credentials Credentials) (Credentials, bool, error) {
	return credentials, false, nil
}
