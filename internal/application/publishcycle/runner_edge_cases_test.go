package publishcycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
)

func TestRunnerRecordsFailureWhenParentLookupFails(t *testing.T) {
	store := &fakeStore{
		duePosts: []domain.Post{
			{
				ID:           "pst_child_missing_parent",
				AccountID:    "acc_parent_lookup",
				Platform:     domain.PlatformX,
				ParentPostID: ptr("pst_parent_missing"),
			},
		},
		accountByID: map[string]domain.SocialAccount{
			"acc_parent_lookup": {ID: "acc_parent_lookup", Platform: domain.PlatformX, Status: domain.AccountStatusConnected},
		},
	}
	provider := &fakeProvider{platform: domain.PlatformX, publishExternalID: "ext_unused"}
	runner := Runner{
		Store:       store,
		Registry:    fakeRegistry{providers: map[domain.Platform]publisher.Provider{domain.PlatformX: provider}},
		Credentials: &fakeCredentialsStore{},
	}

	runner.RunOnce(t.Context())

	if store.recordFailureCalls != 1 {
		t.Fatalf("expected one failure record for missing parent, got %d", store.recordFailureCalls)
	}
	if provider.publishCalls != 0 {
		t.Fatalf("expected no publish attempt when parent lookup fails, got %d", provider.publishCalls)
	}
	if store.markPublishedCalls != 0 {
		t.Fatalf("expected no mark published call, got %d", store.markPublishedCalls)
	}
}

func TestRunnerRecordsFailureWhenParentExternalIDMissing(t *testing.T) {
	parent := domain.Post{ID: "pst_parent_no_external"}
	store := &fakeStore{
		duePosts: []domain.Post{
			{
				ID:           "pst_child_no_parent_external",
				AccountID:    "acc_parent_external",
				Platform:     domain.PlatformLinkedIn,
				ParentPostID: ptr(parent.ID),
			},
		},
		postsByID: map[string]domain.Post{
			parent.ID: parent,
		},
		accountByID: map[string]domain.SocialAccount{
			"acc_parent_external": {ID: "acc_parent_external", Platform: domain.PlatformLinkedIn, Status: domain.AccountStatusConnected},
		},
	}
	provider := &fakeProvider{platform: domain.PlatformLinkedIn, publishExternalID: "ext_unused"}
	runner := Runner{
		Store:       store,
		Registry:    fakeRegistry{providers: map[domain.Platform]publisher.Provider{domain.PlatformLinkedIn: provider}},
		Credentials: &fakeCredentialsStore{},
	}

	runner.RunOnce(t.Context())

	if store.recordFailureCalls != 1 {
		t.Fatalf("expected one failure record when parent external id missing, got %d", store.recordFailureCalls)
	}
	if provider.publishCalls != 0 {
		t.Fatalf("expected no publish attempt when parent external id is missing, got %d", provider.publishCalls)
	}
	if store.markPublishedCalls != 0 {
		t.Fatalf("expected no mark published call, got %d", store.markPublishedCalls)
	}
}

func TestRunnerAuthFailureAfterRetryMarksAccountError(t *testing.T) {
	store := &fakeStore{
		duePosts: []domain.Post{
			{ID: "pst_auth_failure", AccountID: "acc_auth_failure", Platform: domain.PlatformX},
		},
		accountByID: map[string]domain.SocialAccount{
			"acc_auth_failure": {ID: "acc_auth_failure", Platform: domain.PlatformX, Status: domain.AccountStatusConnected},
		},
	}
	provider := &alwaysAuthFailureProvider{platform: domain.PlatformX}
	runner := Runner{
		Store:       store,
		Registry:    fakeRegistry{providers: map[domain.Platform]publisher.Provider{domain.PlatformX: provider}},
		Credentials: &fakeCredentialsStore{},
	}

	runner.RunOnce(t.Context())

	if provider.publishCalls != 2 {
		t.Fatalf("expected publish to retry once after auth failure, got %d calls", provider.publishCalls)
	}
	if store.recordFailureCalls != 1 {
		t.Fatalf("expected one recorded failure after retry, got %d", store.recordFailureCalls)
	}
	if store.updateStatusCalls == 0 || store.lastStatus != domain.AccountStatusError {
		t.Fatalf("expected account status update to error after auth failure, got calls=%d status=%s", store.updateStatusCalls, store.lastStatus)
	}
	if store.markPublishedCalls != 0 {
		t.Fatalf("expected no mark published call after auth failure, got %d", store.markPublishedCalls)
	}
}

func TestUnsupportedPlatformErrorAndPtrHelpers(t *testing.T) {
	err := errUnsupportedPlatform(domain.PlatformInstagram)
	if err == nil {
		t.Fatalf("expected unsupported platform error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "instagram") {
		t.Fatalf("expected error message to include platform, got %q", err.Error())
	}

	if got := ptr("   "); got != nil {
		t.Fatalf("expected nil ptr for blank input, got %v", *got)
	}
	got := ptr("  hello  ")
	if got == nil || *got != "hello" {
		t.Fatalf("expected trimmed pointer value \"hello\", got %+v", got)
	}
}

type alwaysAuthFailureProvider struct {
	platform     domain.Platform
	publishCalls int
}

func (p *alwaysAuthFailureProvider) Platform() domain.Platform {
	return p.platform
}

func (p *alwaysAuthFailureProvider) ValidateDraft(context.Context, domain.SocialAccount, publisher.Draft) ([]string, error) {
	return nil, nil
}

func (p *alwaysAuthFailureProvider) Publish(context.Context, domain.SocialAccount, publisher.Credentials, domain.Post, publisher.PublishOptions) (string, error) {
	p.publishCalls++
	return "", errors.New("401 unauthorized")
}

func (p *alwaysAuthFailureProvider) RefreshIfNeeded(context.Context, domain.SocialAccount, publisher.Credentials) (publisher.Credentials, bool, error) {
	return publisher.Credentials{}, false, nil
}
