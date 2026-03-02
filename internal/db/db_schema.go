package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/antoniolg/publisher/internal/domain"
)

var ErrPostNotDeletable = errors.New("post not deletable")

type Store struct {
	db     *sql.DB
	dbPath string
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
	store := &Store{db: db, dbPath: path}
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
	needsBackup, err := s.shouldBackupBeforeMigrations(ctx)
	if err != nil {
		return err
	}
	if needsBackup {
		if err := backupSQLiteDatabase(s.dbPath); err != nil {
			return err
		}
	}
	return s.applyMigrations(ctx)
}

func NewID(prefix string) (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b[:])), nil
}
