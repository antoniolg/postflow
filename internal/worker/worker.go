package worker

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	publishcycle "github.com/antoniolg/postflow/internal/application/publishcycle"
	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/postflow"
	"github.com/antoniolg/postflow/internal/secure"
)

type Worker struct {
	Store        *db.Store
	Registry     *postflow.ProviderRegistry
	Cipher       *secure.Cipher
	Interval     time.Duration
	RetryBackoff time.Duration
}

func (w Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	w.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w Worker) runOnce(ctx context.Context) {
	recovered, err := w.Store.RecoverStalePublishingPosts(ctx, 5*time.Minute)
	if err != nil {
		slog.Default().Error("worker stale publishing recovery failed", "error", err)
	} else if recovered > 0 {
		slog.Default().Warn("worker recovered stale publishing posts", "count", recovered)
	}

	runner := publishcycle.Runner{
		Store:        w.Store,
		Registry:     w.Registry,
		Credentials:  workerCredentialsStore{worker: w},
		RetryBackoff: w.RetryBackoff,
		Interval:     w.Interval,
	}
	runner.RunOnce(ctx)
}

func (w Worker) loadCredentials(ctx context.Context, accountID string) (postflow.Credentials, error) {
	encrypted, err := w.Store.GetAccountCredentials(ctx, accountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return postflow.Credentials{}, nil
		}
		return postflow.Credentials{}, err
	}
	var credentials postflow.Credentials
	if err := w.Cipher.DecryptJSON(encrypted.Ciphertext, encrypted.Nonce, &credentials); err != nil {
		return postflow.Credentials{}, err
	}
	if credentials.Extra == nil {
		credentials.Extra = make(map[string]string)
	}
	return credentials, nil
}

type workerCredentialsStore struct {
	worker Worker
}

func (w workerCredentialsStore) LoadCredentials(ctx context.Context, accountID string) (postflow.Credentials, error) {
	return w.worker.loadCredentials(ctx, accountID)
}

func (w workerCredentialsStore) SaveCredentials(ctx context.Context, accountID string, credentials postflow.Credentials) error {
	sealed, nonce, err := w.worker.Cipher.EncryptJSON(credentials)
	if err != nil {
		return err
	}
	return w.worker.Store.SaveAccountCredentials(ctx, accountID, db.EncryptedCredentials{
		Ciphertext: sealed,
		Nonce:      nonce,
		KeyVersion: w.worker.Cipher.KeyVersion(),
		UpdatedAt:  time.Now().UTC(),
	})
}
