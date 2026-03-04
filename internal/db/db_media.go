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
