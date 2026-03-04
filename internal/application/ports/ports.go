package ports

import (
	"context"

	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

type ProviderRegistry interface {
	Get(platform domain.Platform) (postflow.Provider, bool)
}

type CredentialsStore interface {
	LoadCredentials(ctx context.Context, accountID string) (postflow.Credentials, error)
	SaveCredentials(ctx context.Context, accountID string, credentials postflow.Credentials) error
}
