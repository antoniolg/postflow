package api

import (
	"sync"

	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
	"github.com/antoniolg/postflow/internal/secure"
)

var (
	fallbackCipherOnce sync.Once
	fallbackCipher     *secure.Cipher
)

func (s Server) providerRegistry() *postflow.ProviderRegistry {
	if s.Registry != nil {
		return s.Registry
	}
	return postflow.NewProviderRegistry(
		postflow.NewMockProvider(domain.PlatformX),
		postflow.NewMockProvider(domain.PlatformLinkedIn),
		postflow.NewMockProvider(domain.PlatformFacebook),
		postflow.NewMockProvider(domain.PlatformInstagram),
	)
}

func (s Server) credentialsCipher() *secure.Cipher {
	if s.Cipher != nil {
		return s.Cipher
	}
	fallbackCipherOnce.Do(func() {
		key := make([]byte, 32)
		for i := range key {
			key[i] = byte(i + 1)
		}
		cipher, _ := secure.NewCipher(key, 1)
		fallbackCipher = cipher
	})
	return fallbackCipher
}
