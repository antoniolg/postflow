package mentions

import (
	"context"
	"errors"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

type fakeAccounts map[string]domain.SocialAccount

func (f fakeAccounts) GetAccount(_ context.Context, id string) (domain.SocialAccount, error) {
	account, ok := f[id]
	if !ok {
		return domain.SocialAccount{}, errors.New("not found")
	}
	return account, nil
}

type fakeCredentials struct {
	loaded postflow.Credentials
	saved  *postflow.Credentials
}

func (f *fakeCredentials) LoadCredentials(context.Context, string) (postflow.Credentials, error) {
	return f.loaded, nil
}

func (f *fakeCredentials) SaveCredentials(_ context.Context, _ string, credentials postflow.Credentials) error {
	f.saved = &credentials
	return nil
}

type fakeLinkedIn struct {
	postflow.MockProvider
	gotToken string
	gotLimit int
}

func (f *fakeLinkedIn) RefreshIfNeeded(_ context.Context, _ domain.SocialAccount, c postflow.Credentials) (postflow.Credentials, bool, error) {
	c.AccessToken = "fresh"
	return c, true, nil
}

func (f *fakeLinkedIn) ListMentions(_ context.Context, _ domain.SocialAccount, c postflow.Credentials, limit int) ([]postflow.Mention, error) {
	f.gotToken, f.gotLimit = c.AccessToken, limit
	return []postflow.Mention{{Name: "Ana", URN: "urn:li:person:x"}}, nil
}

func TestListRefreshesCredentialsAndClampsLimit(t *testing.T) {
	provider := &fakeLinkedIn{MockProvider: postflow.NewMockProvider(domain.PlatformLinkedIn)}
	credentials := &fakeCredentials{loaded: postflow.Credentials{AccessToken: "stale"}}
	service := Service{
		Store: fakeAccounts{
			"acc_page": {ID: "acc_page", Platform: domain.PlatformLinkedIn, AccountKind: domain.AccountKindOrganization, ExternalAccountID: "42"},
			"acc_me":   {ID: "acc_me", Platform: domain.PlatformLinkedIn, AccountKind: domain.AccountKindPersonal, ExternalAccountID: "me"},
		},
		Registry:    postflow.NewProviderRegistry(provider),
		Credentials: credentials,
	}

	got, err := service.List(context.Background(), " acc_page ", 500)
	if err != nil || len(got) != 1 {
		t.Fatalf("list: %v %+v", err, got)
	}
	if provider.gotToken != "fresh" || provider.gotLimit != MaxLimit {
		t.Fatalf("expected refreshed token and clamped limit, got %q %d", provider.gotToken, provider.gotLimit)
	}
	if credentials.saved == nil || credentials.saved.AccessToken != "fresh" {
		t.Fatalf("expected refreshed credentials to be saved, got %+v", credentials.saved)
	}

	if _, err := service.List(context.Background(), "acc_me", 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("personal account: expected ErrUnsupported, got %v", err)
	}
	if _, err := service.List(context.Background(), "", 0); !errors.Is(err, ErrAccountIDRequired) {
		t.Fatalf("empty id: expected ErrAccountIDRequired, got %v", err)
	}
	credentials.loaded = postflow.Credentials{}
	if _, err := service.List(context.Background(), "acc_page", 0); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("no token: expected ErrNoCredentials, got %v", err)
	}
}
