package publishcycle

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/application/ports"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

type Store interface {
	ClaimDuePosts(ctx context.Context, limit int) ([]domain.Post, error)
	GetAccount(ctx context.Context, id string) (domain.SocialAccount, error)
	GetPost(ctx context.Context, id string) (domain.Post, error)
	RecordPublishFailure(ctx context.Context, id string, postErr error, retryBackoff time.Duration) error
	ReschedulePublishWithoutAttempt(ctx context.Context, id string, postErr error, retryDelay time.Duration) error
	MarkPublished(ctx context.Context, id, externalID, publishedURL string) error
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
		rootPostID := strings.TrimSpace(post.ID)
		if post.RootPostID != nil && strings.TrimSpace(*post.RootPostID) != "" {
			rootPostID = strings.TrimSpace(*post.RootPostID)
		}
		threadPosition := post.ThreadPosition
		if threadPosition <= 0 {
			threadPosition = 1
		}
		threadGroupID := strings.TrimSpace(post.ThreadGroupID)
		account, err := r.Store.GetAccount(ctx, post.AccountID)
		if err != nil {
			_ = r.Store.RecordPublishFailure(ctx, post.ID, err, r.RetryBackoff)
			logger.Error("worker account lookup failed", "post_id", post.ID, "root_post_id", rootPostID, "thread_group_id", threadGroupID, "thread_position", threadPosition, "account_id", post.AccountID, "error", err)
			continue
		}

		provider, ok := r.Registry.Get(account.Platform)
		if !ok {
			err := errUnsupportedPlatform(account.Platform)
			_ = r.Store.RecordPublishFailure(ctx, post.ID, err, r.RetryBackoff)
			logger.Error("worker provider not found", "post_id", post.ID, "root_post_id", rootPostID, "thread_group_id", threadGroupID, "thread_position", threadPosition, "platform", account.Platform)
			continue
		}

		credentials, err := r.Credentials.LoadCredentials(ctx, account.ID)
		if err != nil {
			_ = r.Store.RecordPublishFailure(ctx, post.ID, err, r.RetryBackoff)
			logger.Error("worker credentials load failed", "post_id", post.ID, "root_post_id", rootPostID, "thread_group_id", threadGroupID, "thread_position", threadPosition, "account_id", account.ID, "error", err)
			continue
		}

		credentials, err = r.refreshIfNeeded(ctx, provider, account, credentials, false)
		if err != nil {
			_ = r.Store.RecordPublishFailure(ctx, post.ID, err, r.RetryBackoff)
			_ = r.Store.UpdateAccountStatus(ctx, account.ID, domain.AccountStatusError, ptr(err.Error()))
			logger.Error("worker proactive refresh failed", "post_id", post.ID, "root_post_id", rootPostID, "thread_group_id", threadGroupID, "thread_position", threadPosition, "account_id", account.ID, "error", err)
			continue
		}

		publishOpts := postflow.PublishOptions{
			Mode: postflow.PublishModeRoot,
		}
		if post.ParentPostID != nil && strings.TrimSpace(*post.ParentPostID) != "" {
			parentPostID := strings.TrimSpace(*post.ParentPostID)
			targetPostID := parentPostID
			if post.Platform != domain.PlatformX && rootPostID != "" {
				targetPostID = rootPostID
			}
			parent, err := r.Store.GetPost(ctx, targetPostID)
			if err != nil {
				_ = r.Store.RecordPublishFailure(ctx, post.ID, err, r.RetryBackoff)
				logger.Error("worker parent lookup failed", "post_id", post.ID, "root_post_id", rootPostID, "thread_group_id", threadGroupID, "thread_position", threadPosition, "parent_post_id", parentPostID, "target_post_id", targetPostID, "error", err)
				continue
			}
			if parent.ExternalID == nil || strings.TrimSpace(*parent.ExternalID) == "" {
				err := errors.New("parent post external id is missing")
				_ = r.Store.RecordPublishFailure(ctx, post.ID, err, r.RetryBackoff)
				logger.Error("worker parent external id missing", "post_id", post.ID, "root_post_id", rootPostID, "thread_group_id", threadGroupID, "thread_position", threadPosition, "parent_post_id", parentPostID, "target_post_id", parent.ID)
				continue
			}
			publishOpts.ParentExternalID = strings.TrimSpace(*parent.ExternalID)
			if post.Platform == domain.PlatformX {
				publishOpts.Mode = postflow.PublishModeReply
			} else {
				publishOpts.Mode = postflow.PublishModeComment
			}
		}

		publishResult, err := provider.Publish(ctx, account, credentials, post, publishOpts)
		if err != nil && isAuthFailure(err) {
			credentials, err = r.refreshIfNeeded(ctx, provider, account, credentials, true)
			if err == nil {
				publishResult, err = provider.Publish(ctx, account, credentials, post, publishOpts)
			}
		}
		if err != nil {
			if isTransientMediaProcessingError(err) {
				_ = r.Store.ReschedulePublishWithoutAttempt(ctx, post.ID, err, r.Interval)
				logger.Warn("worker publish deferred while media is processing", "post_id", post.ID, "root_post_id", rootPostID, "thread_group_id", threadGroupID, "thread_position", threadPosition, "account_id", account.ID, "platform", post.Platform, "error", err)
				continue
			}
			_ = r.Store.RecordPublishFailure(ctx, post.ID, err, r.RetryBackoff)
			if isAuthFailure(err) {
				_ = r.Store.UpdateAccountStatus(ctx, account.ID, domain.AccountStatusError, ptr(err.Error()))
			}
			logger.Error("worker publish failed", "post_id", post.ID, "root_post_id", rootPostID, "thread_group_id", threadGroupID, "thread_position", threadPosition, "account_id", account.ID, "platform", post.Platform, "attempt", post.Attempts+1, "error", err)
			continue
		}

		externalID := strings.TrimSpace(publishResult.ExternalID)
		publishedURL := strings.TrimSpace(publishResult.PublishedURL)
		if err := r.Store.MarkPublished(ctx, post.ID, externalID, publishedURL); err != nil {
			logger.Error("worker mark published failed", "post_id", post.ID, "root_post_id", rootPostID, "thread_group_id", threadGroupID, "thread_position", threadPosition, "external_id", externalID, "published_url", publishedURL, "error", err)
			continue
		}
		_ = r.Store.UpdateAccountStatus(ctx, account.ID, domain.AccountStatusConnected, nil)
		logger.Info("worker published post", "post_id", post.ID, "root_post_id", rootPostID, "thread_group_id", threadGroupID, "thread_position", threadPosition, "platform", post.Platform, "external_id", externalID, "published_url", publishedURL)
	}
}

func (r Runner) refreshIfNeeded(ctx context.Context, provider postflow.Provider, account domain.SocialAccount, credentials postflow.Credentials, force bool) (postflow.Credentials, error) {
	if force {
		now := time.Now().UTC().Add(-1 * time.Minute)
		credentials.ExpiresAt = &now
	}
	updated, changed, err := provider.RefreshIfNeeded(ctx, account, credentials)
	if err != nil {
		return postflow.Credentials{}, err
	}
	if changed {
		if err := r.Credentials.SaveCredentials(ctx, account.ID, updated); err != nil {
			return postflow.Credentials{}, err
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
