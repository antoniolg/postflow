package db

import (
	"context"
	"database/sql"
)

func migrationAddLocalAuth(ctx context.Context, tx *sql.Tx) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS local_owners (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS web_sessions (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(owner_id) REFERENCES local_owners(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS oauth_clients (
			id TEXT PRIMARY KEY,
			client_id TEXT NOT NULL UNIQUE,
			redirect_uris_json TEXT NOT NULL,
			token_endpoint_auth_method TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS oauth_codes (
			id TEXT PRIMARY KEY,
			client_id TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			code_hash TEXT NOT NULL UNIQUE,
			redirect_uri TEXT NOT NULL,
			scope TEXT NOT NULL,
			code_challenge TEXT NOT NULL,
			code_challenge_method TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			used_at TEXT,
			FOREIGN KEY(client_id) REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
			FOREIGN KEY(owner_id) REFERENCES local_owners(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS oauth_tokens (
			id TEXT PRIMARY KEY,
			client_id TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			access_token_hash TEXT NOT NULL UNIQUE,
			refresh_token_hash TEXT NOT NULL UNIQUE,
			scope TEXT NOT NULL,
			access_expires_at TEXT NOT NULL,
			refresh_expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_used_at TEXT,
			revoked_at TEXT,
			FOREIGN KEY(client_id) REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
			FOREIGN KEY(owner_id) REFERENCES local_owners(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_web_sessions_expires_at ON web_sessions(expires_at);`,
		`CREATE INDEX IF NOT EXISTS idx_oauth_codes_expires_at ON oauth_codes(expires_at);`,
		`CREATE INDEX IF NOT EXISTS idx_oauth_tokens_access_expires_at ON oauth_tokens(access_expires_at);`,
		`CREATE INDEX IF NOT EXISTS idx_oauth_tokens_refresh_expires_at ON oauth_tokens(refresh_expires_at);`,
	}
	for _, query := range queries {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}
