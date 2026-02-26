package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
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
			m.platform,
			m.kind,
			m.original_name,
			m.storage_path,
			m.mime_type,
			m.size_bytes,
			m.created_at,
			COUNT(pm.post_id) AS usage_count
		FROM media m
		LEFT JOIN post_media pm ON pm.media_id = m.id
		GROUP BY
			m.id,
			m.platform,
			m.kind,
			m.original_name,
			m.storage_path,
			m.mime_type,
			m.size_bytes,
			m.created_at
		ORDER BY m.created_at DESC
		LIMIT ?
	`, limit)
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
			&item.Media.Platform,
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
			m.platform,
			m.kind,
			m.original_name,
			m.storage_path,
			m.mime_type,
			m.size_bytes,
			m.created_at,
			COUNT(pm.post_id) AS usage_count
		FROM media m
		LEFT JOIN post_media pm ON pm.media_id = m.id
		WHERE m.id = ?
		GROUP BY
			m.id,
			m.platform,
			m.kind,
			m.original_name,
			m.storage_path,
			m.mime_type,
			m.size_bytes,
			m.created_at
	`, mediaID).Scan(
		&media.ID,
		&media.Platform,
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
		return domain.Media{}, fmt.Errorf("%w: used by %d posts", ErrMediaInUse, usageCount)
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
