package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
)

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
