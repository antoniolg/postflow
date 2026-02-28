package posts

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/application/ports"
	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
)

var (
	ErrAccountIDRequired     = errors.New("account_id is required")
	ErrTextRequired          = errors.New("text is required")
	ErrAccountNotFound       = errors.New("account not found")
	ErrAccountNotConnected   = errors.New("account is not connected")
	ErrProviderNotConfigured = errors.New("provider is not configured for account platform")
	ErrIdempotencyKeyTooLong = errors.New("idempotency key too long (max 128 chars)")
	ErrStoreNotConfigured    = errors.New("post create store is not configured")
	ErrRegistryNotConfigured = errors.New("post provider registry is not configured")
)

type Store interface {
	GetAccount(ctx context.Context, id string) (domain.SocialAccount, error)
	GetMediaByIDs(ctx context.Context, ids []string) ([]domain.Media, error)
	CreatePost(ctx context.Context, params db.CreatePostParams) (db.CreatePostResult, error)
	DeletePostEditable(ctx context.Context, id string) error
}

type CreateService struct {
	Store             Store
	Registry          ports.ProviderRegistry
	DefaultMaxRetries int
}

type CreateInput struct {
	AccountIDs     []string
	Text           string
	ScheduledAt    time.Time
	MediaIDs       []string
	MaxAttempts    int
	IdempotencyKey string
}

type CreateItem struct {
	Post    domain.Post
	Created bool
}

type CreateOutput struct {
	Items        []CreateItem
	CreatedCount int
}

type ValidationError struct {
	Err error
}

func (e ValidationError) Error() string {
	if e.Err == nil {
		return "validation error"
	}
	return e.Err.Error()
}

func (e ValidationError) Unwrap() error {
	return e.Err
}

func IsValidationError(err error) bool {
	var target ValidationError
	return errors.As(err, &target)
}

func NormalizeAccountIDs(primary string, many []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(many)+1)
	add := func(raw string) {
		id := strings.TrimSpace(raw)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range many {
		add(id)
	}
	add(primary)
	return out
}

func (s CreateService) Create(ctx context.Context, in CreateInput) (CreateOutput, error) {
	if s.Store == nil {
		return CreateOutput{}, ErrStoreNotConfigured
	}
	if s.Registry == nil {
		return CreateOutput{}, ErrRegistryNotConfigured
	}

	accountIDs := NormalizeAccountIDs("", in.AccountIDs)
	if len(accountIDs) == 0 {
		return CreateOutput{}, ValidationError{Err: ErrAccountIDRequired}
	}

	text := strings.TrimSpace(in.Text)
	if text == "" {
		return CreateOutput{}, ValidationError{Err: ErrTextRequired}
	}

	mediaIDs := normalizeMediaIDs(in.MediaIDs)
	mediaItems, err := s.Store.GetMediaByIDs(ctx, mediaIDs)
	if err != nil {
		return CreateOutput{}, ValidationError{Err: err}
	}

	maxAttempts := in.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = s.DefaultMaxRetries
		if maxAttempts <= 0 {
			maxAttempts = 3
		}
	}

	idempotencyKey := strings.TrimSpace(in.IdempotencyKey)
	if len(idempotencyKey) > 128 {
		return CreateOutput{}, ValidationError{Err: ErrIdempotencyKeyTooLong}
	}

	targetAccounts := make([]domain.SocialAccount, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		account, err := s.Store.GetAccount(ctx, accountID)
		if err != nil {
			return CreateOutput{}, ValidationError{Err: ErrAccountNotFound}
		}
		if account.Status != domain.AccountStatusConnected {
			return CreateOutput{}, ValidationError{Err: ErrAccountNotConnected}
		}
		provider, ok := s.Registry.Get(account.Platform)
		if !ok {
			return CreateOutput{}, ValidationError{Err: ErrProviderNotConfigured}
		}
		if _, err := provider.ValidateDraft(ctx, account, publisher.Draft{Text: text, Media: mediaItems}); err != nil {
			return CreateOutput{}, ValidationError{Err: err}
		}
		targetAccounts = append(targetAccounts, account)
	}

	out := CreateOutput{
		Items: make([]CreateItem, 0, len(targetAccounts)),
	}
	createdIDs := make([]string, 0, len(targetAccounts))
	for _, account := range targetAccounts {
		result, err := s.Store.CreatePost(ctx, db.CreatePostParams{
			Post: domain.Post{
				AccountID:   account.ID,
				Platform:    account.Platform,
				Text:        text,
				Status:      defaultStatusForScheduledAt(in.ScheduledAt),
				ScheduledAt: in.ScheduledAt,
				MaxAttempts: maxAttempts,
			},
			MediaIDs:       mediaIDs,
			IdempotencyKey: scopeIdempotencyKey(idempotencyKey, account.ID),
		})
		if err != nil {
			if rollbackErr := s.rollbackCreatedPosts(ctx, createdIDs); rollbackErr != nil {
				return CreateOutput{}, fmt.Errorf("%w (rollback failed: %v)", err, rollbackErr)
			}
			return CreateOutput{}, err
		}
		if result.Created {
			out.CreatedCount++
			createdIDs = append(createdIDs, result.Post.ID)
		}
		out.Items = append(out.Items, CreateItem{
			Post:    result.Post,
			Created: result.Created,
		})
	}

	return out, nil
}

func normalizeMediaIDs(mediaIDs []string) []string {
	if len(mediaIDs) == 0 {
		return nil
	}
	out := make([]string, 0, len(mediaIDs))
	for _, raw := range mediaIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	return out
}

func defaultStatusForScheduledAt(scheduledAt time.Time) domain.PostStatus {
	if scheduledAt.IsZero() {
		return domain.PostStatusDraft
	}
	return domain.PostStatusScheduled
}

func (s CreateService) rollbackCreatedPosts(ctx context.Context, postIDs []string) error {
	rollbackErrors := make([]string, 0, len(postIDs))
	for _, postID := range postIDs {
		postID = strings.TrimSpace(postID)
		if postID == "" {
			continue
		}
		if err := s.Store.DeletePostEditable(ctx, postID); err != nil {
			rollbackErrors = append(rollbackErrors, postID+": "+err.Error())
		}
	}
	if len(rollbackErrors) > 0 {
		return errors.New(strings.Join(rollbackErrors, "; "))
	}
	return nil
}

func scopeIdempotencyKey(base, accountID string) string {
	base = strings.TrimSpace(base)
	accountID = strings.TrimSpace(accountID)
	if base == "" || accountID == "" {
		return base
	}
	scoped := base + ":" + accountID
	if len(scoped) <= 128 {
		return scoped
	}
	digest := sha256.Sum256([]byte(scoped))
	suffix := fmt.Sprintf("%x", digest[:8])
	prefixLen := 128 - 1 - len(suffix)
	if prefixLen < 1 {
		return suffix
	}
	if len(base) > prefixLen {
		base = base[:prefixLen]
	}
	return base + ":" + suffix
}
