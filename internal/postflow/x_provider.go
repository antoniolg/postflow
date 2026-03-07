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

func (p *XProvider) Publish(ctx context.Context, _ domain.SocialAccount, credentials Credentials, post domain.Post, opts PublishOptions) (string, error) {
	token := strings.TrimSpace(credentials.AccessToken)
	tokenSecret := strings.TrimSpace(credentials.AccessTokenSecret)
	if token == "" {
		token = strings.TrimSpace(p.cfg.AccessToken)
		tokenSecret = strings.TrimSpace(p.cfg.AccessTokenSecret)
	}
	client, err := NewXClient(XConfig{
		APIBaseURL:        p.cfg.APIBaseURL,
		UploadBaseURL:     p.cfg.UploadBaseURL,
		APIKey:            p.cfg.APIKey,
		APIKeySecret:      p.cfg.APIKeySecret,
		AccessToken:       token,
		AccessTokenSecret: tokenSecret,
	})
	if err != nil {
		return "", fmt.Errorf("build x client: %w", err)
	}
	return client.Publish(ctx, post, opts)
}

func (p *XProvider) httpClient() *http.Client {
	if p != nil && p.client != nil {
		return p.client
	}
	return &http.Client{Timeout: 60 * time.Second}
}
