// Package mentions reads the mentions in an account's recent posts, so the
// URNs that a network accepts can be reused when PostFlow publishes mentions.
package mentions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/antoniolg/postflow/internal/application/ports"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

const (
	DefaultLimit = 20
	MaxLimit     = postflow.LinkedInPostsMaxCount
)

var (
	ErrAccountIDRequired = errors.New("account id is required")
	ErrUnsupported       = postflow.ErrMentionsUnsupportedAccount
	ErrNoCredentials     = errors.New("account has no saved credentials")
)

type AccountStore interface {
	GetAccount(ctx context.Context, id string) (domain.SocialAccount, error)
}

type Service struct {
	Store       AccountStore
	Registry    ports.ProviderRegistry
	Credentials ports.CredentialsStore
}

func ClampLimit(limit int) int {
	if limit <= 0 {
		return DefaultLimit
	}
	if limit > MaxLimit {
		return MaxLimit
	}
	return limit
}

// List returns the mentions in the last `limit` posts of the account. Only
// LinkedIn organization accounts support it.
func (s Service) List(ctx context.Context, accountID string, limit int) ([]postflow.Mention, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, ErrAccountIDRequired
	}
	account, err := s.Store.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	provider, ok := s.Registry.Get(account.Platform)
	if !ok {
		return nil, ErrUnsupported
	}
	lister, ok := provider.(postflow.MentionLister)
	if !ok || account.Platform != domain.PlatformLinkedIn ||
		domain.NormalizeAccountKind(account.Platform, account.AccountKind) != domain.AccountKindOrganization {
		return nil, ErrUnsupported
	}
	credentials, err := s.Credentials.LoadCredentials(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(credentials.AccessToken) == "" {
		return nil, ErrNoCredentials
	}
	updated, changed, err := provider.RefreshIfNeeded(ctx, account, credentials)
	if err != nil {
		return nil, fmt.Errorf("refresh credentials: %w", err)
	}
	if changed {
		if err := s.Credentials.SaveCredentials(ctx, account.ID, updated); err != nil {
			return nil, err
		}
	}
	return lister.ListMentions(ctx, account, updated, ClampLimit(limit))
}
