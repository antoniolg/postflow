package publishcycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

type fakeStore struct {
	duePosts            []domain.Post
	postsByID           map[string]domain.Post
	accountByID         map[string]domain.SocialAccount
	recordFailureCalls  int
	rescheduleCalls     int
	markPublishedCalls  int
	markPublishedPostID string
	markPublishedExtID  string
	updateStatusCalls   int
	lastStatus          domain.AccountStatus
}

func (f *fakeStore) ClaimDuePosts(context.Context, int) ([]domain.Post, error) {
	return f.duePosts, nil
}

func (f *fakeStore) GetAccount(_ context.Context, id string) (domain.SocialAccount, error) {
	account, ok := f.accountByID[id]
	if !ok {
		return domain.SocialAccount{}, errors.New("account not found")
	}
	return account, nil
}

func (f *fakeStore) GetPost(_ context.Context, id string) (domain.Post, error) {
	if f.postsByID != nil {
		if post, ok := f.postsByID[id]; ok {
			return post, nil
		}
	}
	for _, post := range f.duePosts {
		if post.ID == id {
			return post, nil
		}
	}
	return domain.Post{}, errors.New("post not found")
}

func (f *fakeStore) RecordPublishFailure(context.Context, string, error, time.Duration) error {
	f.recordFailureCalls++
	return nil
}

func (f *fakeStore) ReschedulePublishWithoutAttempt(context.Context, string, error, time.Duration) error {
	f.rescheduleCalls++
	return nil
}

func (f *fakeStore) MarkPublished(_ context.Context, id, externalID string) error {
	f.markPublishedCalls++
	f.markPublishedPostID = id
	f.markPublishedExtID = externalID
	return nil
}

func (f *fakeStore) UpdateAccountStatus(_ context.Context, _ string, status domain.AccountStatus, _ *string) error {
	f.updateStatusCalls++
	f.lastStatus = status
	return nil
}

type fakeRegistry struct {
	providers map[domain.Platform]postflow.Provider
}

func (f fakeRegistry) Get(platform domain.Platform) (postflow.Provider, bool) {
	p, ok := f.providers[platform]
	return p, ok
}

type fakeCredentialsStore struct {
	load postflow.Credentials
	save int
}

func (f *fakeCredentialsStore) LoadCredentials(context.Context, string) (postflow.Credentials, error) {
	return f.load, nil
}

func (f *fakeCredentialsStore) SaveCredentials(context.Context, string, postflow.Credentials) error {
	f.save++
	return nil
}

type fakeProvider struct {
	platform          domain.Platform
	publishErr        error
	publishExternalID string
	publishCalls      int
	refreshUpdated    postflow.Credentials
	refreshChanged    bool
	refreshErr        error
}

func (f *fakeProvider) Platform() domain.Platform {
	return f.platform
}

func (f *fakeProvider) ValidateDraft(context.Context, domain.SocialAccount, postflow.Draft) ([]string, error) {
	return nil, nil
}

func (f *fakeProvider) Publish(context.Context, domain.SocialAccount, postflow.Credentials, domain.Post, postflow.PublishOptions) (string, error) {
	f.publishCalls++
	if f.publishErr != nil {
		return "", f.publishErr
	}
	return f.publishExternalID, nil
}

func (f *fakeProvider) RefreshIfNeeded(context.Context, domain.SocialAccount, postflow.Credentials) (postflow.Credentials, bool, error) {
	if f.refreshErr != nil {
		return postflow.Credentials{}, false, f.refreshErr
	}
	return f.refreshUpdated, f.refreshChanged, nil
}

func TestRunnerPublishesAndMarksAccountConnected(t *testing.T) {
	store := &fakeStore{
		duePosts: []domain.Post{
			{ID: "pst_1", AccountID: "acc_1", Platform: domain.PlatformX},
		},
		accountByID: map[string]domain.SocialAccount{
			"acc_1": {ID: "acc_1", Platform: domain.PlatformX, Status: domain.AccountStatusConnected},
		},
	}
	provider := &fakeProvider{
		platform:          domain.PlatformX,
		publishExternalID: "ext_1",
	}
	runner := Runner{
		Store:        store,
		Registry:     fakeRegistry{providers: map[domain.Platform]postflow.Provider{domain.PlatformX: provider}},
		Credentials:  &fakeCredentialsStore{},
		RetryBackoff: 30 * time.Second,
		Interval:     1 * time.Second,
	}

	runner.RunOnce(t.Context())

	if store.markPublishedCalls != 1 {
		t.Fatalf("expected one mark published call, got %d", store.markPublishedCalls)
	}
	if store.markPublishedPostID != "pst_1" || store.markPublishedExtID != "ext_1" {
		t.Fatalf("unexpected mark published payload: id=%q external=%q", store.markPublishedPostID, store.markPublishedExtID)
	}
	if store.updateStatusCalls == 0 || store.lastStatus != domain.AccountStatusConnected {
		t.Fatalf("expected account status update to connected")
	}
	if store.recordFailureCalls != 0 {
		t.Fatalf("expected no failure recording, got %d", store.recordFailureCalls)
	}
}

func TestRunnerRecordsFailureWhenProviderMissing(t *testing.T) {
	store := &fakeStore{
		duePosts: []domain.Post{
			{ID: "pst_2", AccountID: "acc_2", Platform: domain.PlatformInstagram},
		},
		accountByID: map[string]domain.SocialAccount{
			"acc_2": {ID: "acc_2", Platform: domain.PlatformInstagram, Status: domain.AccountStatusConnected},
		},
	}
	runner := Runner{
		Store:        store,
		Registry:     fakeRegistry{providers: map[domain.Platform]postflow.Provider{}},
		Credentials:  &fakeCredentialsStore{},
		RetryBackoff: 30 * time.Second,
		Interval:     1 * time.Second,
	}

	runner.RunOnce(t.Context())

	if store.recordFailureCalls != 1 {
		t.Fatalf("expected one failure record, got %d", store.recordFailureCalls)
	}
	if store.markPublishedCalls != 0 {
		t.Fatalf("expected no mark published calls, got %d", store.markPublishedCalls)
	}
}

func TestRunnerDefersTransientMediaProcessingErrors(t *testing.T) {
	store := &fakeStore{
		duePosts: []domain.Post{
			{ID: "pst_3", AccountID: "acc_3", Platform: domain.PlatformInstagram},
		},
		accountByID: map[string]domain.SocialAccount{
			"acc_3": {ID: "acc_3", Platform: domain.PlatformInstagram, Status: domain.AccountStatusConnected},
		},
	}
	provider := &fakeProvider{
		platform:   domain.PlatformInstagram,
		publishErr: errors.New("instagram error 2207027: media id is not available"),
	}
	runner := Runner{
		Store:        store,
		Registry:     fakeRegistry{providers: map[domain.Platform]postflow.Provider{domain.PlatformInstagram: provider}},
		Credentials:  &fakeCredentialsStore{},
		RetryBackoff: 30 * time.Second,
		Interval:     5 * time.Second,
	}

	runner.RunOnce(t.Context())

	if store.rescheduleCalls != 1 {
		t.Fatalf("expected one reschedule call, got %d", store.rescheduleCalls)
	}
	if store.recordFailureCalls != 0 {
		t.Fatalf("expected no failure record for transient processing, got %d", store.recordFailureCalls)
	}
	if store.markPublishedCalls != 0 {
		t.Fatalf("expected no mark published calls, got %d", store.markPublishedCalls)
	}
}

func TestRunnerAuthFailureRefreshesAndRetries(t *testing.T) {
	store := &fakeStore{
		duePosts: []domain.Post{
			{ID: "pst_4", AccountID: "acc_4", Platform: domain.PlatformX},
		},
		accountByID: map[string]domain.SocialAccount{
			"acc_4": {ID: "acc_4", Platform: domain.PlatformX, Status: domain.AccountStatusConnected},
		},
	}
	firstFailThenSuccess := &authRetryProvider{
		fakeProvider: fakeProvider{
			platform: domain.PlatformX,
		},
	}
	credsStore := &fakeCredentialsStore{}
	runner := Runner{
		Store:        store,
		Registry:     fakeRegistry{providers: map[domain.Platform]postflow.Provider{domain.PlatformX: firstFailThenSuccess}},
		Credentials:  credsStore,
		RetryBackoff: 30 * time.Second,
		Interval:     5 * time.Second,
	}

	runner.RunOnce(t.Context())

	if firstFailThenSuccess.publishCalls != 2 {
		t.Fatalf("expected publish retry after auth failure, got %d publish calls", firstFailThenSuccess.publishCalls)
	}
	if store.markPublishedCalls != 1 {
		t.Fatalf("expected post to be marked published after retry, got %d", store.markPublishedCalls)
	}
	if store.recordFailureCalls != 0 {
		t.Fatalf("expected no failure record after successful retry, got %d", store.recordFailureCalls)
	}
}

type authRetryProvider struct {
	fakeProvider
}

func (p *authRetryProvider) Publish(context.Context, domain.SocialAccount, postflow.Credentials, domain.Post, postflow.PublishOptions) (string, error) {
	p.publishCalls++
	if p.publishCalls == 1 {
		return "", errors.New("401 unauthorized")
	}
	return "ext_after_retry", nil
}
