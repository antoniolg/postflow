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
	baseQueries := []string{
		`CREATE TABLE IF NOT EXISTS media (
			id TEXT PRIMARY KEY,
			platform TEXT NOT NULL,
			kind TEXT NOT NULL,
			original_name TEXT NOT NULL,
			storage_path TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS posts (
			id TEXT PRIMARY KEY,
			platform TEXT NOT NULL,
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
			updated_at TEXT NOT NULL
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
	}
	for _, q := range baseQueries {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	compatAlterQueries := []string{
		`ALTER TABLE posts ADD COLUMN next_retry_at TEXT;`,
		`ALTER TABLE posts ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE posts ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 3;`,
		`ALTER TABLE posts ADD COLUMN idempotency_key TEXT;`,
	}
	for _, q := range compatAlterQueries {
		if _, err := s.db.ExecContext(ctx, q); err != nil && !isDuplicateColumnErr(err) {
			return err
		}
	}
	indexQueries := []string{
		`CREATE INDEX IF NOT EXISTS idx_posts_status_scheduled_at ON posts(status, scheduled_at);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_posts_idempotency_key ON posts(idempotency_key) WHERE idempotency_key IS NOT NULL;`,
		`CREATE INDEX IF NOT EXISTS idx_posts_status_next_retry_at ON posts(status, next_retry_at);`,
		`CREATE INDEX IF NOT EXISTS idx_dead_letters_post_id ON dead_letters(post_id);`,
	}
	for _, q := range indexQueries {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func isDuplicateColumnErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate column name")
}

func NewID(prefix string) (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b[:])), nil
}

func (s *Store) CreateMedia(ctx context.Context, m domain.Media) (domain.Media, error) {
	if m.ID == "" {
		id, err := NewID("med")
		if err != nil {
			return domain.Media{}, err
		}
		m.ID = id
	}
	m.CreatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media (id, platform, kind, original_name, storage_path, mime_type, size_bytes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, m.ID, m.Platform, m.Kind, m.OriginalName, m.StoragePath, m.MimeType, m.SizeBytes, m.CreatedAt.Format(time.RFC3339Nano))
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
			SELECT id, platform, kind, original_name, storage_path, mime_type, size_bytes, created_at
			FROM media WHERE id = ?
		`, id).Scan(&m.ID, &m.Platform, &m.Kind, &m.OriginalName, &m.StoragePath, &m.MimeType, &m.SizeBytes, &created)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("media %s not found", id)
			}
			return nil, err
		}
		parsed, _ := time.Parse(time.RFC3339Nano, created)
		m.CreatedAt = parsed
		out = append(out, m)
	}
	return out, nil
}

func (s *Store) CreatePost(ctx context.Context, params CreatePostParams) (CreatePostResult, error) {
	p := params.Post
	mediaIDs := params.MediaIDs
	idempotencyKey := strings.TrimSpace(params.IdempotencyKey)

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
		p.Status = domain.PostStatusScheduled
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
		INSERT INTO posts (id, platform, text, status, scheduled_at, next_retry_at, attempts, max_attempts, idempotency_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.ID, p.Platform, p.Text, p.Status, p.ScheduledAt.UTC().Format(time.RFC3339Nano), sqlNullTimeString(p.NextRetryAt), p.Attempts, p.MaxAttempts, sqlNullString(p.IdempotencyKey), p.CreatedAt.Format(time.RFC3339Nano), p.UpdatedAt.Format(time.RFC3339Nano))
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO post_media (post_id, media_id) VALUES (?, ?)`, p.ID, mediaID); err != nil {
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
	err := s.db.QueryRowContext(ctx, `
		SELECT id, platform, text, status, scheduled_at, next_retry_at, attempts, max_attempts, idempotency_key, published_at, external_id, error, created_at, updated_at
		FROM posts WHERE id = ?
	`, id).Scan(&p.ID, &p.Platform, &p.Text, &p.Status, &scheduled, &nextRetry, &p.Attempts, &p.MaxAttempts, &idempotencyKey, &published, &external, &failed, &created, &updated)
	if err != nil {
		return domain.Post{}, err
	}
	p.ScheduledAt, _ = time.Parse(time.RFC3339Nano, scheduled)
	if nextRetry.Valid && nextRetry.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, nextRetry.String)
		p.NextRetryAt = &t
	}
	if idempotencyKey.Valid {
		p.IdempotencyKey = &idempotencyKey.String
	}
	if published.Valid && published.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, published.String)
		p.PublishedAt = &t
	}
	if external.Valid {
		p.ExternalID = &external.String
	}
	if failed.Valid {
		p.Error = &failed.String
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	p.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)

	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.platform, m.kind, m.original_name, m.storage_path, m.mime_type, m.size_bytes, m.created_at
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
		if err := rows.Scan(&m.ID, &m.Platform, &m.Kind, &m.OriginalName, &m.StoragePath, &m.MimeType, &m.SizeBytes, &c); err != nil {
			return domain.Post{}, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, c)
		p.Media = append(p.Media, m)
	}
	return p, rows.Err()
}

func (s *Store) GetPostByIdempotencyKey(ctx context.Context, idempotencyKey string) (domain.Post, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM posts WHERE idempotency_key = ?`, idempotencyKey).Scan(&id)
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

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
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

func (s *Store) CancelPost(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE posts
		SET status = ?, updated_at = ?
		WHERE id = ? AND status = ?
	`, domain.PostStatusCanceled, time.Now().UTC().Format(time.RFC3339Nano), id, domain.PostStatusScheduled)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("post not cancelable")
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
	`, domain.PostStatusPublished, now.Format(time.RFC3339Nano), externalID, now.Format(time.RFC3339Nano), id)
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
	err = tx.QueryRowContext(ctx, `SELECT attempts, max_attempts FROM posts WHERE id = ?`, id).Scan(&attempts, &maxAttempts)
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
		`, domain.PostStatusFailed, attempts, postErr.Error(), now.Format(time.RFC3339Nano), id); err != nil {
			return err
		}
		dlqID, err := NewID("dlq")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO dead_letters (id, post_id, reason, last_error, attempted_at)
			VALUES (?, ?, ?, ?, ?)
		`, dlqID, id, "max_attempts_exceeded", postErr.Error(), now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		return tx.Commit()
	}

	nextRetry := now.Add(retryBackoff * time.Duration(1<<(attempts-1)))
	if _, err := tx.ExecContext(ctx, `
		UPDATE posts
		SET status = ?, attempts = ?, error = ?, next_retry_at = ?, updated_at = ?
		WHERE id = ?
	`, domain.PostStatusScheduled, attempts, postErr.Error(), nextRetry.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), id); err != nil {
		return err
	}
	return tx.Commit()
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
