package publishcycle

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/application/ports"
	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
)

type Store interface {
	ClaimDuePosts(ctx context.Context, limit int) ([]domain.Post, error)
	GetAccount(ctx context.Context, id string) (domain.SocialAccount, error)
	RecordPublishFailure(ctx context.Context, id string, postErr error, retryBackoff time.Duration) error
	ReschedulePublishWithoutAttempt(ctx context.Context, id string, postErr error, retryDelay time.Duration) error
	MarkPublished(ctx context.Context, id, externalID string) error
	UpdateAccountStatus(ctx context.Context, id string, status domain.AccountStatus, lastErr *string) error
}

type Runner struct {
	Store        Store
	Registry     ports.ProviderRegistry
	Credentials  ports.CredentialsStore
	RetryBackoff time.Duration
	Interval     time.Duration
	Logger       *slog.Logger
}

func (r Runner) RunOnce(ctx context.Context) {
	logger := r.Logger
	if logger == nil {
		logger = slog.Default()
	}

	posts, err := r.Store.ClaimDuePosts(ctx, 25)
	if err != nil {
		logger.Error("worker claim due posts failed", "error", err)
		return
	}
	if len(posts) > 0 {
		logger.Info("worker claimed posts", "count", len(posts))
	}

	for _, post := range posts {
		account, err := r.Store.GetAccount(ctx, post.AccountID)
		if err != nil {
			_ = r.Store.RecordPublishFailure(ctx, post.ID, err, r.RetryBackoff)
			logger.Error("worker account lookup failed", "post_id", post.ID, "account_id", post.AccountID, "error", err)
			continue
		}

		provider, ok := r.Registry.Get(account.Platform)
		if !ok {
			err := errUnsupportedPlatform(account.Platform)
			_ = r.Store.RecordPublishFailure(ctx, post.ID, err, r.RetryBackoff)
			logger.Error("worker provider not found", "post_id", post.ID, "platform", account.Platform)
			continue
		}

		credentials, err := r.Credentials.LoadCredentials(ctx, account.ID)
		if err != nil {
			_ = r.Store.RecordPublishFailure(ctx, post.ID, err, r.RetryBackoff)
			logger.Error("worker credentials load failed", "post_id", post.ID, "account_id", account.ID, "error", err)
			continue
		}

		credentials, err = r.refreshIfNeeded(ctx, provider, account, credentials, false)
		if err != nil {
			_ = r.Store.RecordPublishFailure(ctx, post.ID, err, r.RetryBackoff)
			_ = r.Store.UpdateAccountStatus(ctx, account.ID, domain.AccountStatusError, ptr(err.Error()))
			logger.Error("worker proactive refresh failed", "post_id", post.ID, "account_id", account.ID, "error", err)
			continue
		}

		externalID, err := provider.Publish(ctx, account, credentials, post)
		if err != nil && isAuthFailure(err) {
			credentials, err = r.refreshIfNeeded(ctx, provider, account, credentials, true)
			if err == nil {
				externalID, err = provider.Publish(ctx, account, credentials, post)
			}
		}
		if err != nil {
			if isTransientMediaProcessingError(err) {
				_ = r.Store.ReschedulePublishWithoutAttempt(ctx, post.ID, err, r.Interval)
				logger.Warn("worker publish deferred while media is processing", "post_id", post.ID, "account_id", account.ID, "platform", post.Platform, "error", err)
				continue
			}
			_ = r.Store.RecordPublishFailure(ctx, post.ID, err, r.RetryBackoff)
			if isAuthFailure(err) {
				_ = r.Store.UpdateAccountStatus(ctx, account.ID, domain.AccountStatusError, ptr(err.Error()))
			}
			logger.Error("worker publish failed", "post_id", post.ID, "account_id", account.ID, "platform", post.Platform, "attempt", post.Attempts+1, "error", err)
			continue
		}

		if err := r.Store.MarkPublished(ctx, post.ID, externalID); err != nil {
			logger.Error("worker mark published failed", "post_id", post.ID, "external_id", externalID, "error", err)
			continue
		}
		_ = r.Store.UpdateAccountStatus(ctx, account.ID, domain.AccountStatusConnected, nil)
		logger.Info("worker published post", "post_id", post.ID, "platform", post.Platform, "external_id", externalID)
	}
}

func (r Runner) refreshIfNeeded(ctx context.Context, provider publisher.Provider, account domain.SocialAccount, credentials publisher.Credentials, force bool) (publisher.Credentials, error) {
	if force {
		now := time.Now().UTC().Add(-1 * time.Minute)
		credentials.ExpiresAt = &now
	}
	updated, changed, err := provider.RefreshIfNeeded(ctx, account, credentials)
	if err != nil {
		return publisher.Credentials{}, err
	}
	if changed {
		if err := r.Credentials.SaveCredentials(ctx, account.ID, updated); err != nil {
			return publisher.Credentials{}, err
		}
	}
	return updated, nil
}

func isAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "401") || strings.Contains(msg, "invalid_token") || strings.Contains(msg, "unauthorized")
}

func isTransientMediaProcessingError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if !strings.Contains(msg, "instagram") {
		return false
	}
	if strings.Contains(msg, "2207027") || strings.Contains(msg, "media id is not available") {
		return true
	}
	return strings.Contains(msg, "not ready") && strings.Contains(msg, "publish")
}

func errUnsupportedPlatform(platform domain.Platform) error {
	return &unsupportedPlatformError{platform: platform}
}

type unsupportedPlatformError struct {
	platform domain.Platform
}

func (e *unsupportedPlatformError) Error() string {
	return "provider not configured for platform " + string(e.platform)
}

func ptr(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
