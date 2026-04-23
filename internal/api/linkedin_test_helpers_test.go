package api

import (
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

func testRegistryWithRealLinkedIn() *postflow.ProviderRegistry {
	return postflow.NewProviderRegistry(
		postflow.NewMockProvider(domain.PlatformX),
		postflow.NewLinkedInProvider(postflow.LinkedInProviderConfig{}),
		postflow.NewMockProvider(domain.PlatformFacebook),
		postflow.NewMockProvider(domain.PlatformInstagram),
	)
}
