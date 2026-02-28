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
