package worker

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
	"github.com/antoniolg/publisher/internal/secure"
)

type Worker struct {
	Store        *db.Store
	Registry     *publisher.ProviderRegistry
	Cipher       *secure.Cipher
	Interval     time.Duration
	RetryBackoff time.Duration
}

func (w Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	w.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w Worker) runOnce(ctx context.Context) {
	posts, err := w.Store.ClaimDuePosts(ctx, 25)
	if err != nil {
		slog.Error("worker claim due posts failed", "error", err)
		return
	}
	if len(posts) > 0 {
		slog.Info("worker claimed posts", "count", len(posts))
	}
	for _, post := range posts {
		account, err := w.Store.GetAccount(ctx, post.AccountID)
		if err != nil {
			_ = w.Store.RecordPublishFailure(ctx, post.ID, err, w.RetryBackoff)
			slog.Error("worker account lookup failed", "post_id", post.ID, "account_id", post.AccountID, "error", err)
			continue
		}
		provider, ok := w.Registry.Get(account.Platform)
		if !ok {
			err := errUnsupportedPlatform(account.Platform)
			_ = w.Store.RecordPublishFailure(ctx, post.ID, err, w.RetryBackoff)
			slog.Error("worker provider not found", "post_id", post.ID, "platform", account.Platform)
			continue
		}
		credentials, err := w.loadCredentials(ctx, account.ID)
		if err != nil {
			_ = w.Store.RecordPublishFailure(ctx, post.ID, err, w.RetryBackoff)
			slog.Error("worker credentials load failed", "post_id", post.ID, "account_id", account.ID, "error", err)
			continue
		}

		credentials, err = w.refreshIfNeeded(ctx, provider, account, credentials, false)
		if err != nil {
			_ = w.Store.RecordPublishFailure(ctx, post.ID, err, w.RetryBackoff)
			_ = w.Store.UpdateAccountStatus(ctx, account.ID, domain.AccountStatusError, ptr(err.Error()))
			slog.Error("worker proactive refresh failed", "post_id", post.ID, "account_id", account.ID, "error", err)
			continue
		}

		externalID, err := provider.Publish(ctx, account, credentials, post)
		if err != nil && isAuthFailure(err) {
			credentials, err = w.refreshIfNeeded(ctx, provider, account, credentials, true)
			if err == nil {
				externalID, err = provider.Publish(ctx, account, credentials, post)
			}
		}
		if err != nil {
			if isTransientMediaProcessingError(err) {
				_ = w.Store.ReschedulePublishWithoutAttempt(ctx, post.ID, err, w.Interval)
				slog.Warn("worker publish deferred while media is processing", "post_id", post.ID, "account_id", account.ID, "platform", post.Platform, "error", err)
				continue
			}
			_ = w.Store.RecordPublishFailure(ctx, post.ID, err, w.RetryBackoff)
			if isAuthFailure(err) {
				_ = w.Store.UpdateAccountStatus(ctx, account.ID, domain.AccountStatusError, ptr(err.Error()))
			}
			slog.Error("worker publish failed", "post_id", post.ID, "account_id", account.ID, "platform", post.Platform, "attempt", post.Attempts+1, "error", err)
			continue
		}
		if err := w.Store.MarkPublished(ctx, post.ID, externalID); err != nil {
			slog.Error("worker mark published failed", "post_id", post.ID, "external_id", externalID, "error", err)
			continue
		}
		_ = w.Store.UpdateAccountStatus(ctx, account.ID, domain.AccountStatusConnected, nil)
		slog.Info("worker published post", "post_id", post.ID, "platform", post.Platform, "external_id", externalID)
	}
}

func (w Worker) loadCredentials(ctx context.Context, accountID string) (publisher.Credentials, error) {
	encrypted, err := w.Store.GetAccountCredentials(ctx, accountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return publisher.Credentials{}, nil
		}
		return publisher.Credentials{}, err
	}
	var credentials publisher.Credentials
	if err := w.Cipher.DecryptJSON(encrypted.Ciphertext, encrypted.Nonce, &credentials); err != nil {
		return publisher.Credentials{}, err
	}
	if credentials.Extra == nil {
		credentials.Extra = make(map[string]string)
	}
	return credentials, nil
}

func (w Worker) refreshIfNeeded(ctx context.Context, provider publisher.Provider, account domain.SocialAccount, credentials publisher.Credentials, force bool) (publisher.Credentials, error) {
	if force {
		now := time.Now().UTC().Add(-1 * time.Minute)
		credentials.ExpiresAt = &now
	}
	updated, changed, err := provider.RefreshIfNeeded(ctx, account, credentials)
	if err != nil {
		return publisher.Credentials{}, err
	}
	if changed {
		sealed, nonce, err := w.Cipher.EncryptJSON(updated)
		if err != nil {
			return publisher.Credentials{}, err
		}
		if err := w.Store.SaveAccountCredentials(ctx, account.ID, db.EncryptedCredentials{
			Ciphertext: sealed,
			Nonce:      nonce,
			KeyVersion: w.Cipher.KeyVersion(),
			UpdatedAt:  time.Now().UTC(),
		}); err != nil {
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
