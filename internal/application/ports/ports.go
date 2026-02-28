package ports

import (
	"context"

	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
)

// ProviderRegistry resolves a publisher provider for a platform.
type ProviderRegistry interface {
	Get(platform domain.Platform) (publisher.Provider, bool)
}

// CredentialsStore persists provider credentials used during publish cycles.
type CredentialsStore interface {
	LoadCredentials(ctx context.Context, accountID string) (publisher.Credentials, error)
	SaveCredentials(ctx context.Context, accountID string, credentials publisher.Credentials) error
}
