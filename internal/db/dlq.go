package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

func (s *Store) ListDeadLetters(ctx context.Context, limit int) ([]domain.DeadLetter, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, post_id, reason, last_error, attempted_at
		FROM dead_letters
		ORDER BY attempted_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.DeadLetter, 0)
	for rows.Next() {
		var d domain.DeadLetter
		var attemptedAt string
		if err := rows.Scan(&d.ID, &d.PostID, &d.Reason, &d.LastError, &attemptedAt); err != nil {
			return nil, err
		}
		d.AttemptedAt, _ = time.Parse(time.RFC3339Nano, attemptedAt)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) RequeueDeadLetter(ctx context.Context, deadLetterID string) (domain.Post, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Post{}, err
	}
	defer tx.Rollback()

	var postID string
	if err := tx.QueryRowContext(ctx, `SELECT post_id FROM dead_letters WHERE id = ?`, deadLetterID).Scan(&postID); err != nil {
		if err == sql.ErrNoRows {
			return domain.Post{}, err
		}
		return domain.Post{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `
		UPDATE posts
		SET status = ?, attempts = 0, next_retry_at = NULL, error = NULL, updated_at = ?
		WHERE id = ? AND status = ?
	`, domain.PostStatusScheduled, now, postID, domain.PostStatusFailed)
	if err != nil {
		return domain.Post{}, err
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return domain.Post{}, fmt.Errorf("post %s is not requeueable", postID)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM dead_letters WHERE id = ?`, deadLetterID); err != nil {
		return domain.Post{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Post{}, err
	}

	return s.GetPost(ctx, postID)
}

func (s *Store) DeleteDeadLetter(ctx context.Context, deadLetterID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var postID string
	if err := tx.QueryRowContext(ctx, `SELECT post_id FROM dead_letters WHERE id = ?`, deadLetterID).Scan(&postID); err != nil {
		if err == sql.ErrNoRows {
			return err
		}
		return err
	}

	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM posts WHERE id = ?`, postID).Scan(&status); err != nil {
		if err == sql.ErrNoRows {
			return err
		}
		return err
	}
	if strings.TrimSpace(status) != string(domain.PostStatusFailed) {
		return fmt.Errorf("post %s is not deletable", postID)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM dead_letters WHERE id = ?`, deadLetterID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM post_media WHERE post_id = ?`, postID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM posts WHERE id = ?`, postID)
	if err != nil {
		return err
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return tx.Commit()
}
