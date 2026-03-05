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
	if strings.TrimSpace(p.ThreadGroupID) == "" {
		p.ThreadGroupID = p.ID
	}
	if p.ThreadPosition <= 0 {
		p.ThreadPosition = 1
	}
	if p.RootPostID == nil || strings.TrimSpace(*p.RootPostID) == "" {
		p.RootPostID = ptrTrimmedString(p.ID)
	}
	if p.ThreadPosition == 1 {
		p.ParentPostID = nil
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
		INSERT INTO posts (id, account_id, text, status, scheduled_at, thread_group_id, thread_position, parent_post_id, root_post_id, next_retry_at, attempts, max_attempts, idempotency_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.ID, p.AccountID, strings.TrimSpace(p.Text), p.Status, formatScheduledAt(p.ScheduledAt), strings.TrimSpace(p.ThreadGroupID), p.ThreadPosition, sqlNullString(p.ParentPostID), sqlNullString(p.RootPostID), sqlNullTimeString(p.NextRetryAt), p.Attempts, p.MaxAttempts, sqlNullString(p.IdempotencyKey), p.CreatedAt.Format(time.RFC3339Nano), p.UpdatedAt.Format(time.RFC3339Nano))
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
	var threadGroupID sql.NullString
	var threadPosition sql.NullInt64
	var parentPostID sql.NullString
	var rootPostID sql.NullString
	var platform string
	err := s.db.QueryRowContext(ctx, `
		SELECT p.id, p.account_id, a.platform, p.text, p.status, p.scheduled_at, p.thread_group_id, p.thread_position, p.parent_post_id, p.root_post_id, p.next_retry_at, p.attempts, p.max_attempts, p.idempotency_key, p.published_at, p.external_id, p.error, p.created_at, p.updated_at
		FROM posts p
		JOIN accounts a ON a.id = p.account_id
		WHERE p.id = ?
	`, strings.TrimSpace(id)).Scan(&p.ID, &p.AccountID, &platform, &p.Text, &p.Status, &scheduled, &threadGroupID, &threadPosition, &parentPostID, &rootPostID, &nextRetry, &p.Attempts, &p.MaxAttempts, &idempotencyKey, &published, &external, &failed, &created, &updated)
	if err != nil {
		return domain.Post{}, err
	}
	p.Platform = domain.Platform(strings.TrimSpace(platform))
	p.ScheduledAt, _ = time.Parse(time.RFC3339Nano, scheduled)
	p.ThreadGroupID = strings.TrimSpace(threadGroupID.String)
	if p.ThreadGroupID == "" {
		p.ThreadGroupID = p.ID
	}
	if threadPosition.Valid && threadPosition.Int64 > 0 {
		p.ThreadPosition = int(threadPosition.Int64)
	} else {
		p.ThreadPosition = 1
	}
	if parentPostID.Valid {
		if trimmed := strings.TrimSpace(parentPostID.String); trimmed != "" {
			p.ParentPostID = &trimmed
		}
	}
	if rootPostID.Valid {
		if trimmed := strings.TrimSpace(rootPostID.String); trimmed != "" {
			p.RootPostID = &trimmed
		}
	}
	if p.RootPostID == nil {
		p.RootPostID = ptrTrimmedString(p.ID)
	}
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

func (s *Store) ListThreadPosts(ctx context.Context, rootPostID string) ([]domain.Post, error) {
	rootPostID = strings.TrimSpace(rootPostID)
	if rootPostID == "" {
		return nil, fmt.Errorf("root_post_id is required")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM posts
		WHERE id = ? OR root_post_id = ?
		ORDER BY thread_position ASC, created_at ASC
	`, rootPostID, rootPostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]string, 0)
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

	posts := make([]domain.Post, 0, len(ids))
	for _, id := range ids {
		post, err := s.GetPost(ctx, id)
		if err != nil {
			return nil, err
		}
		posts = append(posts, post)
	}
	return posts, nil
}

func (s *Store) ListSchedule(ctx context.Context, from, to time.Time) ([]domain.Post, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM posts
		WHERE scheduled_at >= ? AND scheduled_at <= ?
		ORDER BY scheduled_at ASC, thread_group_id ASC, thread_position ASC
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
	rootID, err := s.resolveRootPostID(ctx, id)
	if err != nil {
		return fmt.Errorf("post not schedulable")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE posts
		SET status = ?, scheduled_at = ?, next_retry_at = NULL, attempts = 0, error = NULL, updated_at = ?
		WHERE (id = ? OR root_post_id = ?)
		  AND status IN (?, ?, ?, ?)
	`, domain.PostStatusScheduled, formatScheduledAt(scheduledAt.UTC()), time.Now().UTC().Format(time.RFC3339Nano), rootID, rootID, domain.PostStatusDraft, domain.PostStatusScheduled, domain.PostStatusFailed, domain.PostStatusCanceled)
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
	rootID, err := s.resolveRootPostID(ctx, id)
	if err != nil {
		return fmt.Errorf("post not cancelable")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE posts
		SET status = ?, updated_at = ?
		WHERE (id = ? OR root_post_id = ?)
		  AND status = ?
	`, domain.PostStatusCanceled, time.Now().UTC().Format(time.RFC3339Nano), rootID, rootID, domain.PostStatusScheduled)
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

	rootID, err := resolveRootPostIDTx(ctx, tx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPostNotDeletable
		}
		return err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id, status
		FROM posts
		WHERE id = ? OR root_post_id = ?
	`, rootID, rootID)
	if err != nil {
		return err
	}
	defer rows.Close()

	ids := make([]string, 0, 4)
	for rows.Next() {
		var postID string
		var status domain.PostStatus
		if err := rows.Scan(&postID, &status); err != nil {
			return err
		}
		switch status {
		case domain.PostStatusDraft, domain.PostStatusScheduled, domain.PostStatusFailed, domain.PostStatusCanceled:
		default:
			return ErrPostNotDeletable
		}
		ids = append(ids, postID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return ErrPostNotDeletable
	}

	for _, postID := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM post_media WHERE post_id = ?`, postID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM dead_letters WHERE post_id = ?`, postID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM posts WHERE id = ?`, postID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteThreadEditable(ctx context.Context, rootPostID string) error {
	return s.DeletePostEditable(ctx, rootPostID)
}

func (s *Store) UpdatePostEditable(ctx context.Context, id, text string, scheduledAt time.Time, mediaIDs []string, replaceMedia bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("post not editable")
	}

	status := domain.PostStatusDraft
	if !scheduledAt.IsZero() {
		status = domain.PostStatusScheduled
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE posts
		SET text = ?, status = ?, scheduled_at = ?, next_retry_at = NULL, attempts = 0, error = NULL, updated_at = ?
		WHERE id = ?
		  AND status IN (?, ?, ?, ?)
	`, strings.TrimSpace(text), status, formatScheduledAt(scheduledAt.UTC()), time.Now().UTC().Format(time.RFC3339Nano), id, domain.PostStatusDraft, domain.PostStatusScheduled, domain.PostStatusFailed, domain.PostStatusCanceled)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("post not editable")
	}

	if replaceMedia {
		if _, err := tx.ExecContext(ctx, `DELETE FROM post_media WHERE post_id = ?`, id); err != nil {
			return err
		}
		for _, mediaID := range mediaIDs {
			trimmed := strings.TrimSpace(mediaID)
			if trimmed == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO post_media (post_id, media_id) VALUES (?, ?)`, id, trimmed); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func (s *Store) UpdateThreadEditable(ctx context.Context, rootPostID string, steps []ThreadStepUpdate) error {
	rootPostID = strings.TrimSpace(rootPostID)
	if rootPostID == "" {
		return fmt.Errorf("root_post_id is required")
	}
	if len(steps) == 0 {
		return fmt.Errorf("thread steps are required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rootID, err := resolveRootPostIDTx(ctx, tx, rootPostID)
	if err != nil {
		return fmt.Errorf("post not editable")
	}

	type threadRow struct {
		ID            string
		Status        domain.PostStatus
		AccountID     string
		MaxAttempts   int
		ThreadGroupID string
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, status, account_id, max_attempts, thread_group_id
		FROM posts
		WHERE id = ? OR root_post_id = ?
		ORDER BY thread_position ASC, created_at ASC
	`, rootID, rootID)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make([]threadRow, 0, 4)
	for rows.Next() {
		var item threadRow
		if err := rows.Scan(&item.ID, &item.Status, &item.AccountID, &item.MaxAttempts, &item.ThreadGroupID); err != nil {
			return err
		}
		if !isEditablePostStatus(item.Status) {
			return fmt.Errorf("post not editable")
		}
		existing = append(existing, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(existing) == 0 {
		return fmt.Errorf("post not editable")
	}

	accountID := strings.TrimSpace(existing[0].AccountID)
	if accountID == "" {
		return fmt.Errorf("post not editable")
	}
	threadGroupID := strings.TrimSpace(existing[0].ThreadGroupID)
	if threadGroupID == "" {
		threadGroupID = rootID
	}
	maxAttempts := existing[0].MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	now := time.Now().UTC()
	nowFmt := now.Format(time.RFC3339Nano)

	for i := len(existing) - 1; i >= len(steps); i-- {
		postID := strings.TrimSpace(existing[i].ID)
		if postID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM post_media WHERE post_id = ?`, postID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM dead_letters WHERE post_id = ?`, postID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM posts WHERE id = ?`, postID); err != nil {
			return err
		}
	}

	orderedIDs := make([]string, 0, len(steps))
	insertPostWithMedia := func(postID string, pos int, step ThreadStepUpdate, parentID *string) error {
		status := domain.PostStatusDraft
		if !step.ScheduledAt.IsZero() {
			status = domain.PostStatusScheduled
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO posts (id, account_id, text, status, scheduled_at, thread_group_id, thread_position, parent_post_id, root_post_id, next_retry_at, attempts, max_attempts, idempotency_key, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, 0, ?, NULL, ?, ?)
		`, postID, accountID, strings.TrimSpace(step.Text), status, formatScheduledAt(step.ScheduledAt), threadGroupID, pos, sqlNullString(parentID), rootID, maxAttempts, nowFmt, nowFmt); err != nil {
			return err
		}
		for _, mediaID := range step.MediaIDs {
			trimmed := strings.TrimSpace(mediaID)
			if trimmed == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO post_media (post_id, media_id) VALUES (?, ?)`, postID, trimmed); err != nil {
				return err
			}
		}
		return nil
	}

	for idx, step := range steps {
		pos := idx + 1
		var parentID *string
		if len(orderedIDs) > 0 {
			parentID = ptrTrimmedString(orderedIDs[len(orderedIDs)-1])
		}

		if idx < len(existing) {
			postID := strings.TrimSpace(existing[idx].ID)
			status := domain.PostStatusDraft
			if !step.ScheduledAt.IsZero() {
				status = domain.PostStatusScheduled
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE posts
				SET text = ?, status = ?, scheduled_at = ?, thread_group_id = ?, thread_position = ?, parent_post_id = ?, root_post_id = ?, next_retry_at = NULL, attempts = 0, error = NULL, updated_at = ?
				WHERE id = ?
			`, strings.TrimSpace(step.Text), status, formatScheduledAt(step.ScheduledAt), threadGroupID, pos, sqlNullString(parentID), rootID, nowFmt, postID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM post_media WHERE post_id = ?`, postID); err != nil {
				return err
			}
			for _, mediaID := range step.MediaIDs {
				trimmed := strings.TrimSpace(mediaID)
				if trimmed == "" {
					continue
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO post_media (post_id, media_id) VALUES (?, ?)`, postID, trimmed); err != nil {
					return err
				}
			}
			orderedIDs = append(orderedIDs, postID)
			continue
		}

		newID, err := NewID("pst")
		if err != nil {
			return err
		}
		if err := insertPostWithMedia(newID, pos, step, parentID); err != nil {
			return err
		}
		orderedIDs = append(orderedIDs, newID)
	}

	if len(orderedIDs) == 0 {
		return fmt.Errorf("thread steps are required")
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE posts
		SET root_post_id = ?, thread_group_id = ?, thread_position = 1, parent_post_id = NULL, updated_at = ?
		WHERE id = ?
	`, orderedIDs[0], threadGroupID, nowFmt, orderedIDs[0]); err != nil {
		return err
	}
	for idx := 1; idx < len(orderedIDs); idx++ {
		if _, err := tx.ExecContext(ctx, `
			UPDATE posts
			SET root_post_id = ?, thread_group_id = ?, thread_position = ?, parent_post_id = ?, updated_at = ?
			WHERE id = ?
		`, orderedIDs[0], threadGroupID, idx+1, orderedIDs[idx-1], nowFmt, orderedIDs[idx]); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) ClaimDuePosts(ctx context.Context, limit int) ([]domain.Post, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM posts
		WHERE status = ?
		  AND COALESCE(next_retry_at, scheduled_at) <= ?
		  AND (
			parent_post_id IS NULL
			OR EXISTS (
				SELECT 1
				FROM posts parent
				WHERE parent.id = posts.parent_post_id
				  AND parent.status = ?
				  AND TRIM(COALESCE(parent.external_id, '')) != ''
			)
		  )
		ORDER BY COALESCE(next_retry_at, scheduled_at) ASC, thread_group_id ASC, thread_position ASC
		LIMIT ?
	`, domain.PostStatusScheduled, now, domain.PostStatusPublished, limit)
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

func (s *Store) RecoverStalePublishingPosts(ctx context.Context, staleAfter time.Duration) (int, error) {
	if staleAfter <= 0 {
		staleAfter = 5 * time.Minute
	}
	now := time.Now().UTC()
	nowFmt := now.Format(time.RFC3339Nano)
	cutoff := now.Add(-staleAfter).Format(time.RFC3339Nano)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, error
		FROM posts
		WHERE status = ?
		  AND updated_at <= ?
	`, domain.PostStatusPublishing, cutoff)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type stuckPost struct {
		ID    string
		Error sql.NullString
	}
	stuck := make([]stuckPost, 0, 8)
	for rows.Next() {
		var item stuckPost
		if err := rows.Scan(&item.ID, &item.Error); err != nil {
			return 0, err
		}
		stuck = append(stuck, item)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(stuck) == 0 {
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return 0, nil
	}

	recovered := 0
	for _, item := range stuck {
		lastErr := strings.TrimSpace(item.Error.String)
		if lastErr == "" {
			lastErr = "stale publishing state recovered"
		}

		result, err := tx.ExecContext(ctx, `
			UPDATE posts
			SET status = ?, next_retry_at = NULL, error = ?, updated_at = ?
			WHERE id = ? AND status = ?
		`, domain.PostStatusFailed, lastErr, nowFmt, item.ID, domain.PostStatusPublishing)
		if err != nil {
			return 0, err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			continue
		}
		recovered++

		dlqID, err := NewID("dlq")
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO dead_letters (id, post_id, reason, last_error, attempted_at)
			SELECT ?, ?, ?, ?, ?
			WHERE NOT EXISTS (SELECT 1 FROM dead_letters WHERE post_id = ?)
		`, dlqID, item.ID, "stale_publishing_recovered", lastErr, nowFmt, item.ID); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return recovered, nil
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
	postID := strings.TrimSpace(id)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var attempts, maxAttempts, threadPosition int
	var rootPostID sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT attempts, max_attempts, COALESCE(NULLIF(TRIM(root_post_id), ''), id), thread_position
		FROM posts
		WHERE id = ?
	`, postID).Scan(&attempts, &maxAttempts, &rootPostID, &threadPosition)
	if err != nil {
		return err
	}
	attempts++
	now := time.Now().UTC()
	nowFmt := now.Format(time.RFC3339Nano)
	rootID := strings.TrimSpace(rootPostID.String)
	if rootID == "" {
		rootID = postID
	}
	if threadPosition <= 0 {
		threadPosition = 1
	}

	if attempts >= maxAttempts {
		if _, err := tx.ExecContext(ctx, `
			UPDATE posts
			SET status = ?, attempts = ?, error = ?, next_retry_at = NULL, updated_at = ?
			WHERE id = ?
		`, domain.PostStatusFailed, attempts, postErr.Error(), nowFmt, postID); err != nil {
			return err
		}
		if err := insertDeadLetterTx(ctx, tx, postID, "max_attempts_exceeded", postErr.Error(), nowFmt); err != nil {
			return err
		}
		if err := failBlockedThreadDescendantsTx(ctx, tx, rootID, threadPosition, postID, postErr.Error(), nowFmt); err != nil {
			return err
		}
		return tx.Commit()
	}

	nextRetry := now.Add(retryBackoff * time.Duration(1<<(attempts-1)))
	if _, err := tx.ExecContext(ctx, `
		UPDATE posts
		SET status = ?, attempts = ?, error = ?, next_retry_at = ?, updated_at = ?
		WHERE id = ?
	`, domain.PostStatusScheduled, attempts, postErr.Error(), nextRetry.Format(time.RFC3339Nano), nowFmt, postID); err != nil {
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

func insertDeadLetterTx(ctx context.Context, tx *sql.Tx, postID, reason, lastError, attemptedAt string) error {
	dlqID, err := NewID("dlq")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO dead_letters (id, post_id, reason, last_error, attempted_at)
		SELECT ?, ?, ?, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM dead_letters WHERE post_id = ?)
	`, dlqID, strings.TrimSpace(postID), strings.TrimSpace(reason), strings.TrimSpace(lastError), strings.TrimSpace(attemptedAt), strings.TrimSpace(postID))
	return err
}

func failBlockedThreadDescendantsTx(ctx context.Context, tx *sql.Tx, rootPostID string, failedPosition int, failedPostID, lastError, attemptedAt string) error {
	rootPostID = strings.TrimSpace(rootPostID)
	failedPostID = strings.TrimSpace(failedPostID)
	if rootPostID == "" || failedPosition <= 0 {
		return nil
	}

	blockedError := strings.TrimSpace(lastError)
	if blockedError == "" {
		blockedError = "thread blocked because a previous step failed"
	} else {
		blockedError = "thread blocked because a previous step failed: " + blockedError
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM posts
		WHERE id != ?
		  AND COALESCE(NULLIF(TRIM(root_post_id), ''), id) = ?
		  AND thread_position > ?
		  AND status IN (?, ?)
		ORDER BY thread_position ASC, created_at ASC
	`, failedPostID, rootPostID, failedPosition, domain.PostStatusScheduled, domain.PostStatusPublishing)
	if err != nil {
		return err
	}
	defer rows.Close()

	descendantIDs := make([]string, 0, 4)
	for rows.Next() {
		var descendantID string
		if err := rows.Scan(&descendantID); err != nil {
			return err
		}
		descendantIDs = append(descendantIDs, strings.TrimSpace(descendantID))
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, descendantID := range descendantIDs {
		if descendantID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE posts
			SET status = ?, attempts = max_attempts, error = ?, next_retry_at = NULL, updated_at = ?
			WHERE id = ?
		`, domain.PostStatusFailed, blockedError, attemptedAt, descendantID); err != nil {
			return err
		}
		if err := insertDeadLetterTx(ctx, tx, descendantID, "thread_dependency_failed", blockedError, attemptedAt); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) resolveRootPostID(ctx context.Context, postID string) (string, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(NULLIF(TRIM(root_post_id), ''), id)
		FROM posts
		WHERE id = ?
	`, strings.TrimSpace(postID))
	var rootID string
	if err := row.Scan(&rootID); err != nil {
		return "", err
	}
	return strings.TrimSpace(rootID), nil
}

func resolveRootPostIDTx(ctx context.Context, tx *sql.Tx, postID string) (string, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT COALESCE(NULLIF(TRIM(root_post_id), ''), id)
		FROM posts
		WHERE id = ?
	`, strings.TrimSpace(postID))
	var rootID string
	if err := row.Scan(&rootID); err != nil {
		return "", err
	}
	return strings.TrimSpace(rootID), nil
}

func isEditablePostStatus(status domain.PostStatus) bool {
	switch status {
	case domain.PostStatusDraft, domain.PostStatusScheduled, domain.PostStatusFailed, domain.PostStatusCanceled:
		return true
	default:
		return false
	}
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

func ptrTrimmedString(raw string) *string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
