package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

var ErrOAuthPendingAccountSelectionExpired = errors.New("oauth account selection expired")

type OAuthPendingAccountSelection struct {
	ID         string
	Platform   domain.Platform
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int
	ExpiresAt  time.Time
	CreatedAt  time.Time
}

func (s *Store) CreateOAuthPendingAccountSelection(ctx context.Context, selection OAuthPendingAccountSelection) (OAuthPendingAccountSelection, error) {
	if strings.TrimSpace(selection.ID) == "" {
		id, err := NewID("ops")
		if err != nil {
			return OAuthPendingAccountSelection{}, err
		}
		selection.ID = id
	}
	selection.Platform = domain.Platform(strings.ToLower(strings.TrimSpace(string(selection.Platform))))
	if selection.Platform == "" {
		return OAuthPendingAccountSelection{}, fmt.Errorf("platform is required")
	}
	if len(selection.Ciphertext) == 0 || len(selection.Nonce) == 0 {
		return OAuthPendingAccountSelection{}, fmt.Errorf("ciphertext and nonce are required")
	}
	if selection.KeyVersion <= 0 {
		return OAuthPendingAccountSelection{}, fmt.Errorf("key_version is required")
	}
	if selection.ExpiresAt.IsZero() {
		selection.ExpiresAt = time.Now().UTC().Add(10 * time.Minute)
	}
	selection.CreatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO oauth_pending_account_selections (id, platform, ciphertext, nonce, key_version, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		selection.ID,
		string(selection.Platform),
		selection.Ciphertext,
		selection.Nonce,
		selection.KeyVersion,
		selection.ExpiresAt.Format(time.RFC3339Nano),
		selection.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return OAuthPendingAccountSelection{}, err
	}
	return selection, nil
}

func (s *Store) GetOAuthPendingAccountSelection(ctx context.Context, id string) (OAuthPendingAccountSelection, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OAuthPendingAccountSelection{}, sql.ErrNoRows
	}
	var selection OAuthPendingAccountSelection
	var expiresAt string
	var createdAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, platform, ciphertext, nonce, key_version, expires_at, created_at
		FROM oauth_pending_account_selections
		WHERE id = ?
	`, id).Scan(
		&selection.ID,
		&selection.Platform,
		&selection.Ciphertext,
		&selection.Nonce,
		&selection.KeyVersion,
		&expiresAt,
		&createdAt,
	)
	if err != nil {
		return OAuthPendingAccountSelection{}, err
	}
	selection.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresAt)
	selection.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if selection.ExpiresAt.Before(time.Now().UTC()) {
		_ = s.DeleteOAuthPendingAccountSelection(ctx, selection.ID)
		return OAuthPendingAccountSelection{}, ErrOAuthPendingAccountSelectionExpired
	}
	return selection, nil
}

func (s *Store) DeleteOAuthPendingAccountSelection(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_pending_account_selections WHERE id = ?`, id)
	return err
}
