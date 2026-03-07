package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type migration struct {
	Version            int
	Name               string
	DisableForeignKeys bool
	Up                 func(ctx context.Context, tx *sql.Tx) error
}

var dbMigrations = []migration{
	{
		Version: 1,
		Name:    "initial_schema",
		Up:      migrationInitialSchema,
	},
	{
		Version: 2,
		Name:    "accounts_x_premium",
		Up:      migrationAddAccountsXPremium,
	},
	{
		Version: 3,
		Name:    "posts_threads",
		Up:      migrationAddPostsThreads,
	},
	{
		Version:            4,
		Name:               "accounts_account_kind",
		DisableForeignKeys: true,
		Up:                 migrationAddAccountsAccountKind,
	},
	{
		Version: 5,
		Name:    "oauth_pending_account_selections",
		Up:      migrationAddOAuthPendingAccountSelections,
	},
}

func (s *Store) hasPendingMigrations(ctx context.Context) (bool, error) {
	hasMigrationsTable, err := s.hasTable(ctx, "schema_migrations")
	if err != nil {
		return false, err
	}
	if !hasMigrationsTable {
		return len(dbMigrations) > 0, nil
	}

	applied, err := s.appliedMigrations(ctx)
	if err != nil {
		return false, err
	}
	for _, m := range dbMigrations {
		if _, ok := applied[m.Version]; !ok {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) applyMigrations(ctx context.Context) error {
	if err := s.ensureMigrationsTable(ctx); err != nil {
		return err
	}

	applied, err := s.appliedMigrations(ctx)
	if err != nil {
		return err
	}

	for _, m := range dbMigrations {
		if _, ok := applied[m.Version]; ok {
			continue
		}

		conn, err := s.db.Conn(ctx)
		if err != nil {
			return err
		}
		if m.DisableForeignKeys {
			if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF;`); err != nil {
				conn.Close()
				return err
			}
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			if m.DisableForeignKeys {
				_, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON;`)
			}
			conn.Close()
			return err
		}
		if err := m.Up(ctx, tx); err != nil {
			_ = tx.Rollback()
			if m.DisableForeignKeys {
				_, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON;`)
			}
			conn.Close()
			return fmt.Errorf("apply migration %03d_%s: %w", m.Version, m.Name, err)
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			m.Version,
			m.Name,
			time.Now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			_ = tx.Rollback()
			if m.DisableForeignKeys {
				_, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON;`)
			}
			conn.Close()
			return err
		}
		if err := tx.Commit(); err != nil {
			if m.DisableForeignKeys {
				_, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON;`)
			}
			conn.Close()
			return err
		}
		if m.DisableForeignKeys {
			if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON;`); err != nil {
				conn.Close()
				return err
			}
		}
		if err := conn.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) shouldBackupBeforeMigrations(ctx context.Context) (bool, error) {
	pending, err := s.hasPendingMigrations(ctx)
	if err != nil {
		return false, err
	}
	if !pending {
		return false, nil
	}
	var count int
	err = s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name != 'schema_migrations'`,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) ensureMigrationsTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL
	);`)
	return err
}

func (s *Store) appliedMigrations(ctx context.Context) (map[int]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int]struct{})
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		out[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) hasTable(ctx context.Context, tableName string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`,
		tableName,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func migrationInitialSchema(ctx context.Context, tx *sql.Tx) error {
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
			thread_group_id TEXT NOT NULL DEFAULT '',
			thread_position INTEGER NOT NULL DEFAULT 1,
			parent_post_id TEXT,
			root_post_id TEXT,
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
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func migrationAddAccountsXPremium(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE accounts ADD COLUMN x_premium INTEGER NOT NULL DEFAULT 0;`)
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return nil
	}
	return err
}

func migrationAddPostsThreads(ctx context.Context, tx *sql.Tx) error {
	columns := []string{
		`ALTER TABLE posts ADD COLUMN thread_group_id TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE posts ADD COLUMN thread_position INTEGER NOT NULL DEFAULT 1;`,
		`ALTER TABLE posts ADD COLUMN parent_post_id TEXT;`,
		`ALTER TABLE posts ADD COLUMN root_post_id TEXT;`,
	}
	for _, query := range columns {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
				continue
			}
			return err
		}
	}
	updates := []string{
		`UPDATE posts SET thread_group_id = id WHERE TRIM(COALESCE(thread_group_id, '')) = '';`,
		`UPDATE posts SET thread_position = 1 WHERE thread_position IS NULL OR thread_position <= 0;`,
		`UPDATE posts SET root_post_id = id WHERE TRIM(COALESCE(root_post_id, '')) = '';`,
	}
	for _, query := range updates {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_posts_thread_group_position ON posts(thread_group_id, thread_position);`,
		`CREATE INDEX IF NOT EXISTS idx_posts_parent_status ON posts(parent_post_id, status);`,
		`CREATE INDEX IF NOT EXISTS idx_posts_root ON posts(root_post_id);`,
	}
	for _, query := range indexes {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func migrationAddAccountsAccountKind(ctx context.Context, tx *sql.Tx) error {
	queries := []string{
		`CREATE TABLE accounts_next (
			id TEXT PRIMARY KEY,
			platform TEXT NOT NULL,
			account_kind TEXT NOT NULL,
			display_name TEXT NOT NULL,
			external_account_id TEXT NOT NULL,
			x_premium INTEGER NOT NULL DEFAULT 0,
			auth_method TEXT NOT NULL,
			status TEXT NOT NULL,
			last_error TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(platform, account_kind, external_account_id)
		);`,
		`INSERT INTO accounts_next (
			id,
			platform,
			account_kind,
			display_name,
			external_account_id,
			x_premium,
			auth_method,
			status,
			last_error,
			created_at,
			updated_at
		)
		SELECT
			id,
			platform,
			CASE
				WHEN TRIM(COALESCE(platform, '')) = 'linkedin' THEN 'personal'
				ELSE 'default'
			END,
			display_name,
			external_account_id,
			COALESCE(x_premium, 0),
			auth_method,
			status,
			last_error,
			created_at,
			updated_at
		FROM accounts;`,
		`DROP TABLE accounts;`,
		`ALTER TABLE accounts_next RENAME TO accounts;`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_platform ON accounts(platform);`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_platform_kind ON accounts(platform, account_kind);`,
	}
	for _, query := range queries {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func migrationAddOAuthPendingAccountSelections(ctx context.Context, tx *sql.Tx) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS oauth_pending_account_selections (
			id TEXT PRIMARY KEY,
			platform TEXT NOT NULL,
			ciphertext BLOB NOT NULL,
			nonce BLOB NOT NULL,
			key_version INTEGER NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_oauth_pending_account_selections_expires_at ON oauth_pending_account_selections(expires_at);`,
	}
	for _, query := range queries {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func backupSQLiteDatabase(path string) error {
	if !shouldBackupDatabase(path) {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Size() == 0 {
		return nil
	}

	stamp := time.Now().UTC().Format("20060102T150405Z")
	backupBase := fmt.Sprintf("%s.bak-%s", path, stamp)
	if err := copyFile(path, backupBase); err != nil {
		return fmt.Errorf("copy main db backup: %w", err)
	}
	for _, sidecar := range []string{"-wal", "-shm"} {
		src := path + sidecar
		if _, err := os.Stat(src); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if err := copyFile(src, backupBase+sidecar); err != nil {
			return fmt.Errorf("copy %s backup: %w", sidecar, err)
		}
	}
	return nil
}

func shouldBackupDatabase(path string) bool {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if lower == ":memory:" || strings.HasPrefix(lower, "file::memory:") {
		return false
	}
	if strings.HasPrefix(lower, "file:") {
		return false
	}
	return true
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
