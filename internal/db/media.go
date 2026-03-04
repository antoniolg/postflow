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

var ErrMediaInUse = errors.New("media in use")

type MediaWithUsage struct {
	Media      domain.Media
	UsageCount int
}

func (s *Store) ListMedia(ctx context.Context, limit int) ([]MediaWithUsage, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			m.id,
			m.kind,
			m.original_name,
			m.storage_path,
			m.mime_type,
			m.size_bytes,
			m.created_at,
			SUM(CASE WHEN p.status NOT IN (?, ?) THEN 1 ELSE 0 END) AS usage_count
		FROM media m
		LEFT JOIN post_media pm ON pm.media_id = m.id
		LEFT JOIN posts p ON p.id = pm.post_id
		GROUP BY
			m.id,
			m.kind,
			m.original_name,
			m.storage_path,
			m.mime_type,
			m.size_bytes,
			m.created_at
		ORDER BY m.created_at DESC
		LIMIT ?
	`, domain.PostStatusPublished, domain.PostStatusCanceled, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]MediaWithUsage, 0, limit)
	for rows.Next() {
		var created string
		var item MediaWithUsage
		if err := rows.Scan(
			&item.Media.ID,
			&item.Media.Kind,
			&item.Media.OriginalName,
			&item.Media.StoragePath,
			&item.Media.MimeType,
			&item.Media.SizeBytes,
			&created,
			&item.UsageCount,
		); err != nil {
			return nil, err
		}
		item.Media.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) DeleteMediaIfUnused(ctx context.Context, mediaID string) (domain.Media, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Media{}, err
	}
	defer tx.Rollback()
	var media domain.Media
	var created string
	var usageCount int
	err = tx.QueryRowContext(ctx, `
		SELECT
			m.id,
			m.kind,
			m.original_name,
			m.storage_path,
			m.mime_type,
			m.size_bytes,
			m.created_at,
			SUM(CASE WHEN p.status NOT IN (?, ?) THEN 1 ELSE 0 END) AS usage_count
		FROM media m
		LEFT JOIN post_media pm ON pm.media_id = m.id
		LEFT JOIN posts p ON p.id = pm.post_id
		WHERE m.id = ?
		GROUP BY
			m.id,
			m.kind,
			m.original_name,
			m.storage_path,
			m.mime_type,
			m.size_bytes,
			m.created_at
	`, domain.PostStatusPublished, domain.PostStatusCanceled, mediaID).Scan(
		&media.ID,
		&media.Kind,
		&media.OriginalName,
		&media.StoragePath,
		&media.MimeType,
		&media.SizeBytes,
		&created,
		&usageCount,
	)
	if err != nil {
		return domain.Media{}, err
	}
	media.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)

	if usageCount > 0 {
		return domain.Media{}, fmt.Errorf("%w: used by %d non-published posts", ErrMediaInUse, usageCount)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM post_media WHERE media_id = ?`, mediaID); err != nil {
		return domain.Media{}, err
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM media WHERE id = ?`, mediaID)
	if err != nil {
		return domain.Media{}, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return domain.Media{}, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return domain.Media{}, err
	}
	return media, nil
}

func (s *Store) DeleteMediaUnusedByPendingPosts(ctx context.Context) ([]domain.Media, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT
			m.id,
			m.kind,
			m.original_name,
			m.storage_path,
			m.mime_type,
			m.size_bytes,
			m.created_at
		FROM media m
		WHERE NOT EXISTS (
			SELECT 1
			FROM post_media pm
			INNER JOIN posts p ON p.id = pm.post_id
			WHERE pm.media_id = m.id
			  AND p.status NOT IN (?, ?)
		)
		ORDER BY m.created_at ASC
	`, domain.PostStatusPublished, domain.PostStatusCanceled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	deleted := make([]domain.Media, 0, 32)
	ids := make([]string, 0, 32)
	for rows.Next() {
		var created string
		var item domain.Media
		if err := rows.Scan(
			&item.ID,
			&item.Kind,
			&item.OriginalName,
			&item.StoragePath,
			&item.MimeType,
			&item.SizeBytes,
			&created,
		); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		deleted = append(deleted, item)
		ids = append(ids, item.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return deleted, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i := range ids {
		placeholders[i] = "?"
		args[i] = ids[i]
	}
	placeholderSQL := strings.Join(placeholders, ",")

	if _, err := tx.ExecContext(ctx, `DELETE FROM post_media WHERE media_id IN (`+placeholderSQL+`)`, args...); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM media WHERE id IN (`+placeholderSQL+`)`, args...); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return deleted, nil
}
