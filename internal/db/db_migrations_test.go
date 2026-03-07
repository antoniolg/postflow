package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestOpenMigratesLegacySchemaWithoutDataLoss(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	accountID := "acc_legacy_x"
	postID := "pst_legacy_1"

	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer legacyDB.Close()
	if err := seedLegacySchemaV1(t.Context(), legacyDB, accountID, postID); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	defer store.Close()

	post, err := store.GetPost(context.Background(), postID)
	if err != nil {
		t.Fatalf("get migrated post: %v", err)
	}
	if post.ID != postID || post.AccountID != accountID || post.Text != "legacy scheduled post" {
		t.Fatalf("unexpected post after migration: %+v", post)
	}

	var xPremium int
	if err := store.db.QueryRowContext(t.Context(), `SELECT x_premium FROM accounts WHERE id = ?`, accountID).Scan(&xPremium); err != nil {
		t.Fatalf("query x_premium: %v", err)
	}
	if xPremium != 0 {
		t.Fatalf("expected x_premium default 0, got %d", xPremium)
	}

	var accountKind string
	if err := store.db.QueryRowContext(t.Context(), `SELECT account_kind FROM accounts WHERE id = ?`, accountID).Scan(&accountKind); err != nil {
		t.Fatalf("query account_kind: %v", err)
	}
	if accountKind != string(domain.AccountKindDefault) {
		t.Fatalf("expected account_kind default, got %q", accountKind)
	}

	var migrationCount int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("count schema migrations: %v", err)
	}
	if migrationCount != len(dbMigrations) {
		t.Fatalf("expected %d applied migrations, got %d", len(dbMigrations), migrationCount)
	}

	backupFiles, err := filepath.Glob(dbPath + ".bak-*")
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(backupFiles) == 0 {
		t.Fatalf("expected backup file before migration")
	}
}

func seedLegacySchemaV1(ctx context.Context, db *sql.DB, accountID, postID string) error {
	queries := []string{
		`PRAGMA foreign_keys = ON;`,
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
		if _, err := db.ExecContext(ctx, query); err != nil {
			return err
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	scheduledAt := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)
	if _, err := db.ExecContext(ctx, `INSERT INTO accounts (id, platform, display_name, external_account_id, auth_method, status, created_at, updated_at) VALUES (?, 'x', 'Legacy X', 'legacy-x', 'static', 'connected', ?, ?)`, accountID, now, now); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO posts (id, account_id, text, status, scheduled_at, attempts, max_attempts, created_at, updated_at) VALUES (?, ?, 'legacy scheduled post', 'scheduled', ?, 0, 3, ?, ?)`, postID, accountID, scheduledAt, now, now); err != nil {
		return err
	}
	return nil
}
