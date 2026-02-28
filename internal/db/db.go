package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/antoniolg/publisher/internal/domain"
)

const schemaVersion = "2"

var ErrPostNotDeletable = errors.New("post not deletable")

type Store struct {
	db *sql.DB
}

type CreatePostParams struct {
	Post           domain.Post
	MediaIDs       []string
	IdempotencyKey string
}

type CreatePostResult struct {
	Post    domain.Post
	Created bool
}

type EncryptedCredentials struct {
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int
	UpdatedAt  time.Time
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = ON;`); err != nil {
		return err
	}

	needsRecreate, err := s.needsSchemaReset(ctx)
	if err != nil {
		return err
	}
	if needsRecreate {
		if err := s.resetSchema(ctx); err != nil {
			return err
		}
	}

	if err := s.createSchema(ctx); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('schema_version', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, schemaVersion)
	return err
}

func (s *Store) needsSchemaReset(ctx context.Context) (bool, error) {
	var hasSettings int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='settings'`).Scan(&hasSettings)
	if err != nil {
		return false, err
	}
	if hasSettings == 0 {
		return false, nil
	}
	var version string
	err = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='schema_version'`).Scan(&version)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true, nil
		}
		return false, err
	}
	return strings.TrimSpace(version) != schemaVersion, nil
}

func (s *Store) resetSchema(ctx context.Context) error {
	queries := []string{
		`DROP TABLE IF EXISTS dead_letters;`,
		`DROP TABLE IF EXISTS post_media;`,
		`DROP TABLE IF EXISTS posts;`,
		`DROP TABLE IF EXISTS oauth_states;`,
		`DROP TABLE IF EXISTS account_credentials;`,
		`DROP TABLE IF EXISTS accounts;`,
		`DROP TABLE IF EXISTS media;`,
		`DROP TABLE IF EXISTS settings;`,
	}
	for _, query := range queries {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) createSchema(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS media (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			original_name TEXT NOT NULL,
			storage_path TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS accounts (
				id TEXT PRIMARY KEY,
				platform TEXT NOT NULL,
				display_name TEXT NOT NULL,
				external_account_id TEXT NOT NULL,
				x_premium INTEGER NOT NULL DEFAULT 0,
				auth_method TEXT NOT NULL,
				status TEXT NOT NULL,
				last_error TEXT,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
			UNIQUE(platform, external_account_id)
		);`,
		`CREATE TABLE IF NOT EXISTS account_credentials (
			account_id TEXT PRIMARY KEY,
			ciphertext BLOB NOT NULL,
			nonce BLOB NOT NULL,
			key_version INTEGER NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS oauth_states (
			id TEXT PRIMARY KEY,
			platform TEXT NOT NULL,
			state TEXT NOT NULL UNIQUE,
			code_verifier TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS posts (
			id TEXT PRIMARY KEY,
			account_id TEXT NOT NULL,
			text TEXT NOT NULL,
			status TEXT NOT NULL,
			scheduled_at TEXT NOT NULL,
			next_retry_at TEXT,
			attempts INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 3,
			idempotency_key TEXT,
			published_at TEXT,
			external_id TEXT,
			error TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(account_id) REFERENCES accounts(id)
		);`,
		`CREATE TABLE IF NOT EXISTS post_media (
			post_id TEXT NOT NULL,
			media_id TEXT NOT NULL,
			PRIMARY KEY (post_id, media_id),
			FOREIGN KEY(post_id) REFERENCES posts(id),
			FOREIGN KEY(media_id) REFERENCES media(id)
		);`,
		`CREATE TABLE IF NOT EXISTS dead_letters (
			id TEXT PRIMARY KEY,
			post_id TEXT NOT NULL,
			reason TEXT NOT NULL,
			last_error TEXT NOT NULL,
			attempted_at TEXT NOT NULL,
			FOREIGN KEY(post_id) REFERENCES posts(id)
		);`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_posts_status_scheduled_at ON posts(status, scheduled_at);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_posts_idempotency_key ON posts(idempotency_key) WHERE idempotency_key IS NOT NULL;`,
		`CREATE INDEX IF NOT EXISTS idx_posts_status_next_retry_at ON posts(status, next_retry_at);`,
		`CREATE INDEX IF NOT EXISTS idx_dead_letters_post_id ON dead_letters(post_id);`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_platform ON accounts(platform);`,
	}
	for _, query := range queries {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	if err := s.ensureAccountsColumns(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureAccountsColumns(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `ALTER TABLE accounts ADD COLUMN x_premium INTEGER NOT NULL DEFAULT 0;`)
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return nil
	}
	return err
}

func NewID(prefix string) (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b[:])), nil
}

func (s *Store) CreateMedia(ctx context.Context, m domain.Media) (domain.Media, error) {
	if strings.TrimSpace(m.ID) == "" {
		id, err := NewID("med")
		if err != nil {
			return domain.Media{}, err
		}
		m.ID = id
	}
	m.CreatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media (id, kind, original_name, storage_path, mime_type, size_bytes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, m.ID, strings.TrimSpace(m.Kind), strings.TrimSpace(m.OriginalName), strings.TrimSpace(m.StoragePath), strings.TrimSpace(m.MimeType), m.SizeBytes, m.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return domain.Media{}, err
	}
	return m, nil
}

func (s *Store) GetMediaByIDs(ctx context.Context, ids []string) ([]domain.Media, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]domain.Media, 0, len(ids))
	for _, id := range ids {
		var m domain.Media
		var created string
		err := s.db.QueryRowContext(ctx, `
			SELECT id, kind, original_name, storage_path, mime_type, size_bytes, created_at
			FROM media WHERE id = ?
		`, strings.TrimSpace(id)).Scan(&m.ID, &m.Kind, &m.OriginalName, &m.StoragePath, &m.MimeType, &m.SizeBytes, &created)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("media %s not found", strings.TrimSpace(id))
			}
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, m)
	}
	return out, nil
}

func (s *Store) CreatePost(ctx context.Context, params CreatePostParams) (CreatePostResult, error) {
	p := params.Post
	mediaIDs := params.MediaIDs
	idempotencyKey := strings.TrimSpace(params.IdempotencyKey)
	if strings.TrimSpace(p.AccountID) == "" {
		return CreatePostResult{}, fmt.Errorf("account_id is required")
	}

	if idempotencyKey != "" {
		existing, err := s.GetPostByIdempotencyKey(ctx, idempotencyKey)
		if err == nil {
			return CreatePostResult{Post: existing, Created: false}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return CreatePostResult{}, err
		}
	}

	if p.ID == "" {
		id, err := NewID("pst")
		if err != nil {
			return CreatePostResult{}, err
		}
		p.ID = id
	}
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.Status == "" {
		if p.ScheduledAt.IsZero() {
			p.Status = domain.PostStatusDraft
		} else {
			p.Status = domain.PostStatusScheduled
		}
	}
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 3
	}
	if idempotencyKey != "" {
		p.IdempotencyKey = &idempotencyKey
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CreatePostResult{}, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO posts (id, account_id, text, status, scheduled_at, next_retry_at, attempts, max_attempts, idempotency_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.ID, p.AccountID, strings.TrimSpace(p.Text), p.Status, formatScheduledAt(p.ScheduledAt), sqlNullTimeString(p.NextRetryAt), p.Attempts, p.MaxAttempts, sqlNullString(p.IdempotencyKey), p.CreatedAt.Format(time.RFC3339Nano), p.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		if idempotencyKey != "" && strings.Contains(strings.ToLower(err.Error()), "unique") {
			existing, findErr := s.GetPostByIdempotencyKey(ctx, idempotencyKey)
			if findErr != nil {
				return CreatePostResult{}, findErr
			}
			return CreatePostResult{Post: existing, Created: false}, nil
		}
		return CreatePostResult{}, err
	}

	for _, mediaID := range mediaIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO post_media (post_id, media_id) VALUES (?, ?)`, p.ID, strings.TrimSpace(mediaID)); err != nil {
			return CreatePostResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return CreatePostResult{}, err
	}
	createdPost, err := s.GetPost(ctx, p.ID)
	if err != nil {
		return CreatePostResult{}, err
	}
	return CreatePostResult{Post: createdPost, Created: true}, nil
}

func (s *Store) GetPost(ctx context.Context, id string) (domain.Post, error) {
	var p domain.Post
	var scheduled, created, updated string
	var published, nextRetry sql.NullString
	var external, failed, idempotencyKey sql.NullString
	var platform string
	err := s.db.QueryRowContext(ctx, `
		SELECT p.id, p.account_id, a.platform, p.text, p.status, p.scheduled_at, p.next_retry_at, p.attempts, p.max_attempts, p.idempotency_key, p.published_at, p.external_id, p.error, p.created_at, p.updated_at
		FROM posts p
		JOIN accounts a ON a.id = p.account_id
		WHERE p.id = ?
	`, strings.TrimSpace(id)).Scan(&p.ID, &p.AccountID, &platform, &p.Text, &p.Status, &scheduled, &nextRetry, &p.Attempts, &p.MaxAttempts, &idempotencyKey, &published, &external, &failed, &created, &updated)
	if err != nil {
		return domain.Post{}, err
	}
	p.Platform = domain.Platform(strings.TrimSpace(platform))
	p.ScheduledAt, _ = time.Parse(time.RFC3339Nano, scheduled)
	if nextRetry.Valid && strings.TrimSpace(nextRetry.String) != "" {
		t, _ := time.Parse(time.RFC3339Nano, nextRetry.String)
		p.NextRetryAt = &t
	}
	if idempotencyKey.Valid {
		value := strings.TrimSpace(idempotencyKey.String)
		if value != "" {
			p.IdempotencyKey = &value
		}
	}
	if published.Valid && strings.TrimSpace(published.String) != "" {
		t, _ := time.Parse(time.RFC3339Nano, published.String)
		p.PublishedAt = &t
	}
	if external.Valid {
		value := strings.TrimSpace(external.String)
		if value != "" {
			p.ExternalID = &value
		}
	}
	if failed.Valid {
		value := strings.TrimSpace(failed.String)
		if value != "" {
			p.Error = &value
		}
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	p.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)

	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.kind, m.original_name, m.storage_path, m.mime_type, m.size_bytes, m.created_at
		FROM media m
		JOIN post_media pm ON pm.media_id = m.id
		WHERE pm.post_id = ?
		ORDER BY m.created_at ASC
	`, p.ID)
	if err != nil {
		return domain.Post{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var m domain.Media
		var c string
		if err := rows.Scan(&m.ID, &m.Kind, &m.OriginalName, &m.StoragePath, &m.MimeType, &m.SizeBytes, &c); err != nil {
			return domain.Post{}, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, c)
		p.Media = append(p.Media, m)
	}
	return p, rows.Err()
}

func (s *Store) GetPostByIdempotencyKey(ctx context.Context, idempotencyKey string) (domain.Post, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM posts WHERE idempotency_key = ?`, strings.TrimSpace(idempotencyKey)).Scan(&id)
	if err != nil {
		return domain.Post{}, err
	}
	return s.GetPost(ctx, id)
}

func (s *Store) ListSchedule(ctx context.Context, from, to time.Time) ([]domain.Post, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM posts
		WHERE scheduled_at >= ? AND scheduled_at <= ?
		ORDER BY scheduled_at ASC
	`, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]domain.Post, 0, len(ids))
	for _, id := range ids {
		p, err := s.GetPost(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) ListDrafts(ctx context.Context) ([]domain.Post, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM posts
		WHERE status = ?
		ORDER BY updated_at DESC
	`, domain.PostStatusDraft)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]domain.Post, 0, len(ids))
	for _, id := range ids {
		p, err := s.GetPost(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) ScheduleDraftPost(ctx context.Context, id string, scheduledAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE posts
		SET status = ?, scheduled_at = ?, next_retry_at = NULL, attempts = 0, error = NULL, updated_at = ?
		WHERE id = ? AND status = ?
	`, domain.PostStatusScheduled, formatScheduledAt(scheduledAt.UTC()), time.Now().UTC().Format(time.RFC3339Nano), strings.TrimSpace(id), domain.PostStatusDraft)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("post not schedulable")
	}
	return nil
}

func (s *Store) CancelPost(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE posts
		SET status = ?, updated_at = ?
		WHERE id = ? AND status = ?
	`, domain.PostStatusCanceled, time.Now().UTC().Format(time.RFC3339Nano), strings.TrimSpace(id), domain.PostStatusScheduled)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("post not cancelable")
	}
	return nil
}

func (s *Store) DeletePostEditable(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrPostNotDeletable
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var status domain.PostStatus
	err = tx.QueryRowContext(ctx, `SELECT status FROM posts WHERE id = ?`, id).Scan(&status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPostNotDeletable
		}
		return err
	}
	switch status {
	case domain.PostStatusDraft, domain.PostStatusScheduled, domain.PostStatusFailed, domain.PostStatusCanceled:
	default:
		return ErrPostNotDeletable
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM post_media WHERE post_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM dead_letters WHERE post_id = ?`, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM posts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrPostNotDeletable
	}
	return tx.Commit()
}

func (s *Store) UpdatePostEditable(ctx context.Context, id, text string, scheduledAt time.Time) error {
	status := domain.PostStatusDraft
	if !scheduledAt.IsZero() {
		status = domain.PostStatusScheduled
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE posts
		SET text = ?, status = ?, scheduled_at = ?, next_retry_at = NULL, attempts = 0, error = NULL, updated_at = ?
		WHERE id = ?
		  AND status IN (?, ?, ?, ?)
	`, strings.TrimSpace(text), status, formatScheduledAt(scheduledAt.UTC()), time.Now().UTC().Format(time.RFC3339Nano), strings.TrimSpace(id), domain.PostStatusDraft, domain.PostStatusScheduled, domain.PostStatusFailed, domain.PostStatusCanceled)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("post not editable")
	}
	return nil
}

func (s *Store) ClaimDuePosts(ctx context.Context, limit int) ([]domain.Post, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM posts
		WHERE status = ?
		  AND COALESCE(next_retry_at, scheduled_at) <= ?
		ORDER BY COALESCE(next_retry_at, scheduled_at) ASC
		LIMIT ?
	`, domain.PostStatusScheduled, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	claimed := make([]domain.Post, 0, len(ids))
	for _, id := range ids {
		result, err := s.db.ExecContext(ctx, `
			UPDATE posts
			SET status = ?, updated_at = ?
			WHERE id = ? AND status = ?
		`, domain.PostStatusPublishing, time.Now().UTC().Format(time.RFC3339Nano), id, domain.PostStatusScheduled)
		if err != nil {
			return nil, err
		}
		n, _ := result.RowsAffected()
		if n == 1 {
			p, err := s.GetPost(ctx, id)
			if err != nil {
				return nil, err
			}
			claimed = append(claimed, p)
		}
	}
	return claimed, nil
}

func (s *Store) MarkPublished(ctx context.Context, id, externalID string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE posts
		SET status = ?, published_at = ?, external_id = ?, error = NULL, next_retry_at = NULL, updated_at = ?
		WHERE id = ?
	`, domain.PostStatusPublished, now.Format(time.RFC3339Nano), strings.TrimSpace(externalID), now.Format(time.RFC3339Nano), strings.TrimSpace(id))
	return err
}

func (s *Store) RecordPublishFailure(ctx context.Context, id string, postErr error, retryBackoff time.Duration) error {
	if retryBackoff <= 0 {
		retryBackoff = 30 * time.Second
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var attempts, maxAttempts int
	err = tx.QueryRowContext(ctx, `SELECT attempts, max_attempts FROM posts WHERE id = ?`, strings.TrimSpace(id)).Scan(&attempts, &maxAttempts)
	if err != nil {
		return err
	}
	attempts++
	now := time.Now().UTC()

	if attempts >= maxAttempts {
		if _, err := tx.ExecContext(ctx, `
			UPDATE posts
			SET status = ?, attempts = ?, error = ?, next_retry_at = NULL, updated_at = ?
			WHERE id = ?
		`, domain.PostStatusFailed, attempts, postErr.Error(), now.Format(time.RFC3339Nano), strings.TrimSpace(id)); err != nil {
			return err
		}
		dlqID, err := NewID("dlq")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO dead_letters (id, post_id, reason, last_error, attempted_at)
			VALUES (?, ?, ?, ?, ?)
		`, dlqID, strings.TrimSpace(id), "max_attempts_exceeded", postErr.Error(), now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		return tx.Commit()
	}

	nextRetry := now.Add(retryBackoff * time.Duration(1<<(attempts-1)))
	if _, err := tx.ExecContext(ctx, `
		UPDATE posts
		SET status = ?, attempts = ?, error = ?, next_retry_at = ?, updated_at = ?
		WHERE id = ?
	`, domain.PostStatusScheduled, attempts, postErr.Error(), nextRetry.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), strings.TrimSpace(id)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReschedulePublishWithoutAttempt(ctx context.Context, id string, postErr error, retryDelay time.Duration) error {
	if retryDelay <= 0 {
		retryDelay = 30 * time.Second
	}
	now := time.Now().UTC()
	nextRetry := now.Add(retryDelay)
	_, err := s.db.ExecContext(ctx, `
		UPDATE posts
		SET status = ?, error = ?, next_retry_at = ?, updated_at = ?
		WHERE id = ? AND status = ?
	`, domain.PostStatusScheduled, postErr.Error(), nextRetry.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), strings.TrimSpace(id), domain.PostStatusPublishing)
	return err
}

func sqlNullString(v *string) any {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil
	}
	return strings.TrimSpace(*v)
}

func sqlNullTimeString(v *time.Time) any {
	if v == nil {
		return nil
	}
	return v.UTC().Format(time.RFC3339Nano)
}

func formatScheduledAt(v time.Time) string {
	if v.IsZero() {
		return ""
	}
	return v.UTC().Format(time.RFC3339Nano)
}
