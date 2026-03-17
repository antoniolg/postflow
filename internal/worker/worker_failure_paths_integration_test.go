package worker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/application/ports"
	publishcycle "github.com/antoniolg/postflow/internal/application/publishcycle"
	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

func TestWorkerFailurePathMissingProviderMovesPostToFailed(t *testing.T) {
	store := openWorkerTestStore(t)
	account := createWorkerTestAccountForPlatform(t, store, domain.PlatformLinkedIn)
	post := createWorkerDuePost(t, store, account.ID, "missing provider", 1)

	creds := workerCredentialsStore{worker: Worker{Store: store, Cipher: newWorkerTestCipher(t)}}
	runPublishCycleOnce(t, store, postflow.NewProviderRegistry(), creds, 5*time.Second, 1*time.Second)

	got, err := store.GetPost(t.Context(), post.ID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if got.Status != domain.PostStatusFailed {
		t.Fatalf("expected failed post status, got %s", got.Status)
	}
	if got.Attempts != 1 {
		t.Fatalf("expected attempts=1, got %d", got.Attempts)
	}
	if got.Error == nil || !strings.Contains(strings.ToLower(*got.Error), "provider not configured") {
		t.Fatalf("expected provider missing error, got %+v", got.Error)
	}

	accountAfter, err := store.GetAccount(t.Context(), account.ID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if accountAfter.Status != domain.AccountStatusConnected {
		t.Fatalf("expected account status to remain connected, got %s", accountAfter.Status)
	}

	dlq, err := store.ListDeadLetters(t.Context(), 10)
	if err != nil {
		t.Fatalf("list dead letters: %v", err)
	}
	if len(dlq) != 1 || dlq[0].PostID != post.ID {
		t.Fatalf("expected one dead letter for post %s, got %+v", post.ID, dlq)
	}
}

func TestWorkerFailurePathAccountLookupFailureRecordsFailure(t *testing.T) {
	store := openWorkerTestStore(t)
	account := createWorkerTestAccountForPlatform(t, store, domain.PlatformX)
	post := createWorkerDuePost(t, store, account.ID, "account lookup fails", 1)

	creds := workerCredentialsStore{worker: Worker{Store: store, Cipher: newWorkerTestCipher(t)}}
	failingStore := getAccountErrorStore{
		base:       store,
		accountErr: errors.New("account lookup failed: temporary db outage"),
	}
	runPublishCycleOnce(t, failingStore, postflow.NewProviderRegistry(postflow.NewMockProvider(domain.PlatformX)), creds, 5*time.Second, 1*time.Second)

	got, err := store.GetPost(t.Context(), post.ID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if got.Status != domain.PostStatusFailed {
		t.Fatalf("expected failed post status, got %s", got.Status)
	}
	if got.Attempts != 1 {
		t.Fatalf("expected attempts=1, got %d", got.Attempts)
	}
	if got.Error == nil || !strings.Contains(strings.ToLower(*got.Error), "account lookup failed") {
		t.Fatalf("expected account lookup error in post, got %+v", got.Error)
	}

	accountAfter, err := store.GetAccount(t.Context(), account.ID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if accountAfter.Status != domain.AccountStatusConnected {
		t.Fatalf("expected account status to remain connected, got %s", accountAfter.Status)
	}
}

func TestWorkerFailurePathRefreshFailureMarksAccountError(t *testing.T) {
	store := openWorkerTestStore(t)
	account := createWorkerTestAccountForPlatform(t, store, domain.PlatformX)
	post := createWorkerDuePost(t, store, account.ID, "refresh fails", 1)

	provider := &workerScenarioProvider{
		platform: domain.PlatformX,
		refreshFn: func(context.Context, domain.SocialAccount, postflow.Credentials) (postflow.Credentials, bool, error) {
			return postflow.Credentials{}, false, errors.New("401 unauthorized: refresh token revoked")
		},
	}
	creds := workerCredentialsStore{worker: Worker{Store: store, Cipher: newWorkerTestCipher(t)}}
	runPublishCycleOnce(t, store, postflow.NewProviderRegistry(provider), creds, 5*time.Second, 1*time.Second)

	got, err := store.GetPost(t.Context(), post.ID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if got.Status != domain.PostStatusFailed {
		t.Fatalf("expected failed post status, got %s", got.Status)
	}
	if got.Error == nil || !strings.Contains(strings.ToLower(*got.Error), "refresh token revoked") {
		t.Fatalf("expected refresh failure in post error, got %+v", got.Error)
	}

	accountAfter, err := store.GetAccount(t.Context(), account.ID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if accountAfter.Status != domain.AccountStatusError {
		t.Fatalf("expected account status error, got %s", accountAfter.Status)
	}
	if accountAfter.LastError == nil || !strings.Contains(strings.ToLower(*accountAfter.LastError), "refresh token revoked") {
		t.Fatalf("expected account last_error to include refresh failure, got %+v", accountAfter.LastError)
	}
}

func TestWorkerFailurePathCredentialsLoadFailureMovesPostToFailed(t *testing.T) {
	store := openWorkerTestStore(t)
	account := createWorkerTestAccountForPlatform(t, store, domain.PlatformX)
	post := createWorkerDuePost(t, store, account.ID, "load credentials fails", 1)

	creds := forcedCredentialsStore{
		loadErr: errors.New("decrypt credentials failed"),
	}
	runPublishCycleOnce(t, store, postflow.NewProviderRegistry(postflow.NewMockProvider(domain.PlatformX)), creds, 5*time.Second, 1*time.Second)

	got, err := store.GetPost(t.Context(), post.ID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if got.Status != domain.PostStatusFailed {
		t.Fatalf("expected failed post status, got %s", got.Status)
	}
	if got.Attempts != 1 {
		t.Fatalf("expected attempts=1, got %d", got.Attempts)
	}
	if got.Error == nil || !strings.Contains(strings.ToLower(*got.Error), "decrypt credentials failed") {
		t.Fatalf("expected load failure in post error, got %+v", got.Error)
	}

	accountAfter, err := store.GetAccount(t.Context(), account.ID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if accountAfter.Status != domain.AccountStatusConnected {
		t.Fatalf("expected account status to remain connected, got %s", accountAfter.Status)
	}
}

func TestWorkerFailurePathCredentialsSaveFailureMarksAccountError(t *testing.T) {
	store := openWorkerTestStore(t)
	account := createWorkerTestAccountForPlatform(t, store, domain.PlatformX)
	post := createWorkerDuePost(t, store, account.ID, "save credentials fails", 1)

	provider := &workerScenarioProvider{
		platform: domain.PlatformX,
		refreshFn: func(_ context.Context, _ domain.SocialAccount, _ postflow.Credentials) (postflow.Credentials, bool, error) {
			return postflow.Credentials{
				AccessToken: "updated-token",
				TokenType:   "Bearer",
			}, true, nil
		},
	}
	creds := forcedCredentialsStore{
		delegate: workerCredentialsStore{worker: Worker{Store: store, Cipher: newWorkerTestCipher(t)}},
		saveErr:  errors.New("save credentials failed"),
	}
	runPublishCycleOnce(t, store, postflow.NewProviderRegistry(provider), creds, 5*time.Second, 1*time.Second)

	got, err := store.GetPost(t.Context(), post.ID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if got.Status != domain.PostStatusFailed {
		t.Fatalf("expected failed post status, got %s", got.Status)
	}
	if got.Attempts != 1 {
		t.Fatalf("expected attempts=1, got %d", got.Attempts)
	}
	if got.Error == nil || !strings.Contains(strings.ToLower(*got.Error), "save credentials failed") {
		t.Fatalf("expected save failure in post error, got %+v", got.Error)
	}

	accountAfter, err := store.GetAccount(t.Context(), account.ID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if accountAfter.Status != domain.AccountStatusError {
		t.Fatalf("expected account status error, got %s", accountAfter.Status)
	}
	if accountAfter.LastError == nil || !strings.Contains(strings.ToLower(*accountAfter.LastError), "save credentials failed") {
		t.Fatalf("expected account last_error to include save failure, got %+v", accountAfter.LastError)
	}
}

func TestWorkerFailurePathTransientMediaDeferralReschedules(t *testing.T) {
	store := openWorkerTestStore(t)
	account := createWorkerTestAccountForPlatform(t, store, domain.PlatformInstagram)
	post := createWorkerDuePost(t, store, account.ID, "instagram media processing", 3)

	provider := &workerScenarioProvider{
		platform: domain.PlatformInstagram,
		publishFn: func(context.Context, domain.SocialAccount, postflow.Credentials, domain.Post, postflow.PublishOptions) (postflow.PublishResult, error) {
			return postflow.PublishResult{}, errors.New("instagram error 2207027: media id is not available")
		},
	}
	creds := workerCredentialsStore{worker: Worker{Store: store, Cipher: newWorkerTestCipher(t)}}
	runPublishCycleOnce(t, store, postflow.NewProviderRegistry(provider), creds, 7*time.Second, 1*time.Second)

	got, err := store.GetPost(t.Context(), post.ID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if got.Status != domain.PostStatusScheduled {
		t.Fatalf("expected scheduled post status after transient defer, got %s", got.Status)
	}
	if got.Attempts != 0 {
		t.Fatalf("expected attempts to remain 0, got %d", got.Attempts)
	}
	if got.NextRetryAt == nil {
		t.Fatalf("expected next_retry_at to be set after transient defer")
	}
	if got.Error == nil || !strings.Contains(strings.ToLower(*got.Error), "2207027") {
		t.Fatalf("expected transient error to be persisted, got %+v", got.Error)
	}

	dlq, err := store.ListDeadLetters(t.Context(), 10)
	if err != nil {
		t.Fatalf("list dead letters: %v", err)
	}
	if len(dlq) != 0 {
		t.Fatalf("expected no dead letters on transient defer, got %d", len(dlq))
	}
}

type getAccountErrorStore struct {
	base       *db.Store
	accountErr error
}

func (s getAccountErrorStore) ClaimDuePosts(ctx context.Context, limit int) ([]domain.Post, error) {
	return s.base.ClaimDuePosts(ctx, limit)
}

func (s getAccountErrorStore) GetAccount(context.Context, string) (domain.SocialAccount, error) {
	return domain.SocialAccount{}, s.accountErr
}

func (s getAccountErrorStore) GetPost(ctx context.Context, id string) (domain.Post, error) {
	return s.base.GetPost(ctx, id)
}

func (s getAccountErrorStore) RecordPublishFailure(ctx context.Context, id string, postErr error, retryBackoff time.Duration) error {
	return s.base.RecordPublishFailure(ctx, id, postErr, retryBackoff)
}

func (s getAccountErrorStore) ReschedulePublishWithoutAttempt(ctx context.Context, id string, postErr error, retryDelay time.Duration) error {
	return s.base.ReschedulePublishWithoutAttempt(ctx, id, postErr, retryDelay)
}

func (s getAccountErrorStore) MarkPublished(ctx context.Context, id, externalID, publishedURL string) error {
	return s.base.MarkPublished(ctx, id, externalID, publishedURL)
}

func (s getAccountErrorStore) UpdateAccountStatus(ctx context.Context, id string, status domain.AccountStatus, lastErr *string) error {
	return s.base.UpdateAccountStatus(ctx, id, status, lastErr)
}

type forcedCredentialsStore struct {
	delegate ports.CredentialsStore
	loadErr  error
	saveErr  error
}

func (s forcedCredentialsStore) LoadCredentials(ctx context.Context, accountID string) (postflow.Credentials, error) {
	if s.loadErr != nil {
		return postflow.Credentials{}, s.loadErr
	}
	if s.delegate == nil {
		return postflow.Credentials{}, nil
	}
	return s.delegate.LoadCredentials(ctx, accountID)
}

func (s forcedCredentialsStore) SaveCredentials(ctx context.Context, accountID string, credentials postflow.Credentials) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	if s.delegate == nil {
		return nil
	}
	return s.delegate.SaveCredentials(ctx, accountID, credentials)
}

type workerScenarioProvider struct {
	platform  domain.Platform
	publishFn func(context.Context, domain.SocialAccount, postflow.Credentials, domain.Post, postflow.PublishOptions) (postflow.PublishResult, error)
	refreshFn func(context.Context, domain.SocialAccount, postflow.Credentials) (postflow.Credentials, bool, error)
}

func (p *workerScenarioProvider) Platform() domain.Platform {
	return p.platform
}

func (p *workerScenarioProvider) ValidateDraft(context.Context, domain.SocialAccount, postflow.Draft) ([]string, error) {
	return nil, nil
}

func (p *workerScenarioProvider) Publish(ctx context.Context, account domain.SocialAccount, credentials postflow.Credentials, post domain.Post, opts postflow.PublishOptions) (postflow.PublishResult, error) {
	if p.publishFn != nil {
		return p.publishFn(ctx, account, credentials, post, opts)
	}
	return postflow.PublishResult{ExternalID: "ext_" + post.ID}, nil
}

func (p *workerScenarioProvider) RefreshIfNeeded(ctx context.Context, account domain.SocialAccount, credentials postflow.Credentials) (postflow.Credentials, bool, error) {
	if p.refreshFn != nil {
		return p.refreshFn(ctx, account, credentials)
	}
	return credentials, false, nil
}

func createWorkerTestAccountForPlatform(t *testing.T, store *db.Store, platform domain.Platform) domain.SocialAccount {
	t.Helper()
	account, err := store.UpsertAccount(t.Context(), db.UpsertAccountParams{
		Platform:          platform,
		DisplayName:       "Worker " + string(platform),
		ExternalAccountID: "worker_" + string(platform) + "_" + strings.ReplaceAll(t.Name(), "/", "_"),
		AuthMethod:        domain.AuthMethodOAuth,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("upsert account: %v", err)
	}
	return account
}

func createWorkerDuePost(t *testing.T, store *db.Store, accountID, text string, maxAttempts int) domain.Post {
	t.Helper()
	created, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   accountID,
			Text:        text,
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(-1 * time.Minute),
			MaxAttempts: maxAttempts,
		},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}
	return created.Post
}

func runPublishCycleOnce(t *testing.T, store publishcycle.Store, registry ports.ProviderRegistry, credentials ports.CredentialsStore, interval, retryBackoff time.Duration) {
	t.Helper()
	runner := publishcycle.Runner{
		Store:        store,
		Registry:     registry,
		Credentials:  credentials,
		RetryBackoff: retryBackoff,
		Interval:     interval,
	}
	runner.RunOnce(t.Context())
}
