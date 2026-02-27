package api

import (
	"sync"

	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
	"github.com/antoniolg/publisher/internal/secure"
)

var (
	fallbackCipherOnce sync.Once
	fallbackCipher     *secure.Cipher
)

func (s Server) providerRegistry() *publisher.ProviderRegistry {
	if s.Registry != nil {
		return s.Registry
	}
	return publisher.NewProviderRegistry(
		publisher.NewMockProvider(domain.PlatformX),
		publisher.NewMockProvider(domain.PlatformLinkedIn),
		publisher.NewMockProvider(domain.PlatformFacebook),
		publisher.NewMockProvider(domain.PlatformInstagram),
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
