package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var ErrLocalOwnerAuthFailed = errors.New("invalid email or password")

type LocalOwner struct {
	ID           string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type WebSession struct {
	ID        string
	OwnerID   string
	TokenHash string
	ExpiresAt time.Time
	CreatedAt time.Time
}

func (s *Store) HasLocalOwner(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM local_owners`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) GetLocalOwner(ctx context.Context) (LocalOwner, error) {
	var owner LocalOwner
	var createdAt string
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, email, password_hash, created_at, updated_at
		FROM local_owners
		ORDER BY created_at ASC
		LIMIT 1
	`).Scan(&owner.ID, &owner.Email, &owner.PasswordHash, &createdAt, &updatedAt)
	if err != nil {
		return LocalOwner{}, err
	}
	owner.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	owner.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return owner, nil
}

func (s *Store) GetLocalOwnerByID(ctx context.Context, id string) (LocalOwner, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return LocalOwner{}, sql.ErrNoRows
	}
	var owner LocalOwner
	var createdAt string
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, email, password_hash, created_at, updated_at
		FROM local_owners
		WHERE id = ?
	`, id).Scan(&owner.ID, &owner.Email, &owner.PasswordHash, &createdAt, &updatedAt)
	if err != nil {
		return LocalOwner{}, err
	}
	owner.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	owner.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return owner, nil
}

func (s *Store) UpsertLocalOwnerBootstrap(ctx context.Context, email, passwordHash string) (LocalOwner, error) {
	email = normalizeOwnerEmail(email)
	passwordHash = strings.TrimSpace(passwordHash)
	if email == "" {
		return LocalOwner{}, fmt.Errorf("owner email is required")
	}
	if passwordHash == "" {
		return LocalOwner{}, fmt.Errorf("owner password hash is required")
	}
	now := time.Now().UTC()

	owner, err := s.GetLocalOwner(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return LocalOwner{}, err
	}
	if err == nil {
		if _, updateErr := s.db.ExecContext(ctx, `
			UPDATE local_owners
			SET email = ?, password_hash = ?, updated_at = ?
			WHERE id = ?
		`, email, passwordHash, now.Format(time.RFC3339Nano), owner.ID); updateErr != nil {
			return LocalOwner{}, updateErr
		}
		return s.GetLocalOwnerByID(ctx, owner.ID)
	}

	id, err := NewID("own")
	if err != nil {
		return LocalOwner{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO local_owners (id, email, password_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, id, email, passwordHash, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		return LocalOwner{}, err
	}
	return s.GetLocalOwnerByID(ctx, id)
}

func (s *Store) AuthenticateLocalOwner(ctx context.Context, email, password string) (LocalOwner, error) {
	email = normalizeOwnerEmail(email)
	password = strings.TrimSpace(password)
	if email == "" || password == "" {
		return LocalOwner{}, ErrLocalOwnerAuthFailed
	}
	owner, err := s.GetLocalOwner(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LocalOwner{}, ErrLocalOwnerAuthFailed
		}
		return LocalOwner{}, err
	}
	if owner.Email != email {
		return LocalOwner{}, ErrLocalOwnerAuthFailed
	}
	if compareErr := bcrypt.CompareHashAndPassword([]byte(owner.PasswordHash), []byte(password)); compareErr != nil {
		return LocalOwner{}, ErrLocalOwnerAuthFailed
	}
	return owner, nil
}

func (s *Store) CreateWebSession(ctx context.Context, ownerID string, ttl time.Duration) (string, WebSession, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return "", WebSession{}, fmt.Errorf("owner id is required")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM web_sessions WHERE expires_at <= ?`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return "", WebSession{}, err
	}
	rawToken, err := randomOpaqueToken(32)
	if err != nil {
		return "", WebSession{}, err
	}
	session := WebSession{
		ID:        mustStoreID("ses"),
		OwnerID:   ownerID,
		TokenHash: tokenHash(rawToken),
		ExpiresAt: time.Now().UTC().Add(ttl),
		CreatedAt: time.Now().UTC(),
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO web_sessions (id, owner_id, token_hash, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, session.ID, session.OwnerID, session.TokenHash, session.ExpiresAt.Format(time.RFC3339Nano), session.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return "", WebSession{}, err
	}
	return rawToken, session, nil
}

func (s *Store) GetWebSessionByToken(ctx context.Context, rawToken string) (WebSession, error) {
	hash := tokenHash(rawToken)
	if hash == "" {
		return WebSession{}, sql.ErrNoRows
	}
	var session WebSession
	var expiresAt string
	var createdAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, owner_id, token_hash, expires_at, created_at
		FROM web_sessions
		WHERE token_hash = ?
	`, hash).Scan(&session.ID, &session.OwnerID, &session.TokenHash, &expiresAt, &createdAt)
	if err != nil {
		return WebSession{}, err
	}
	session.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresAt)
	session.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if !session.ExpiresAt.After(time.Now().UTC()) {
		_ = s.DeleteWebSessionByToken(ctx, rawToken)
		return WebSession{}, sql.ErrNoRows
	}
	return session, nil
}

func (s *Store) DeleteWebSessionByToken(ctx context.Context, rawToken string) error {
	hash := tokenHash(rawToken)
	if hash == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM web_sessions WHERE token_hash = ?`, hash)
	return err
}

func normalizeOwnerEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func mustStoreID(prefix string) string {
	id, err := NewID(prefix)
	if err == nil {
		return id
	}
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func tokenHash(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func randomOpaqueToken(size int) (string, error) {
	if size <= 0 {
		size = 32
	}
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
