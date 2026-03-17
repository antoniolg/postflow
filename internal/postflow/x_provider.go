package postflow

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

type XProvider struct {
	cfg    XConfig
	client *http.Client
}

func NewXProvider(cfg XConfig) *XProvider {
	return &XProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (p *XProvider) Platform() domain.Platform {
	return domain.PlatformX
}

func (p *XProvider) ValidateDraft(_ context.Context, account domain.SocialAccount, draft Draft) ([]string, error) {
	warnings := make([]string, 0)
	maxChars := 280
	if account.XPremium {
		maxChars = 25000
	}
	if len([]rune(strings.TrimSpace(draft.Text))) > maxChars {
		warnings = append(warnings, fmt.Sprintf("text exceeds %d chars for this x account; publish may fail", maxChars))
	}
	if len(draft.Media) > 4 {
		warnings = append(warnings, "x supports up to 4 media items per post")
	}
	return warnings, nil
}

func (p *XProvider) Publish(ctx context.Context, _ domain.SocialAccount, credentials Credentials, post domain.Post, opts PublishOptions) (PublishResult, error) {
	token := strings.TrimSpace(credentials.AccessToken)
	if strings.TrimSpace(credentials.AccessTokenSecret) != "" || strings.EqualFold(strings.TrimSpace(credentials.TokenType), "oauth1") {
		return PublishResult{}, fmt.Errorf("x static/oauth1 accounts are no longer supported; reconnect via oauth")
	}
	if token == "" {
		return PublishResult{}, fmt.Errorf("x account is missing an oauth access token; reconnect via oauth")
	}
	client, err := NewXClient(XConfig{
		APIBaseURL:    p.cfg.APIBaseURL,
		UploadBaseURL: p.cfg.UploadBaseURL,
		AccessToken:   token,
	})
	if err != nil {
		return PublishResult{}, fmt.Errorf("build x client: %w", err)
	}
	externalID, err := client.Publish(ctx, post, opts)
	if err != nil {
		return PublishResult{}, err
	}
	result := PublishResult{ExternalID: strings.TrimSpace(externalID)}
	if opts.Mode == PublishModeRoot {
		username := strings.TrimSpace(credentials.Extra["username"])
		if username == "" && strings.TrimSpace(token) != "" {
			if user, fetchErr := p.fetchCurrentUser(ctx, token); fetchErr == nil {
				username = strings.TrimSpace(user.Username)
			}
		}
		if username != "" && result.ExternalID != "" {
			result.PublishedURL = fmt.Sprintf("https://x.com/%s/status/%s", username, result.ExternalID)
		}
	}
	return result, nil
}

func (p *XProvider) httpClient() *http.Client {
	if p != nil && p.client != nil {
		return p.client
	}
	return &http.Client{Timeout: 60 * time.Second}
}
