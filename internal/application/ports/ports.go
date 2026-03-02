package ports

import (
	"context"

	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
)

type ProviderRegistry interface {
	Get(platform domain.Platform) (publisher.Provider, bool)
}

type CredentialsStore interface {
	LoadCredentials(ctx context.Context, accountID string) (publisher.Credentials, error)
	SaveCredentials(ctx context.Context, accountID string, credentials publisher.Credentials) error
}
