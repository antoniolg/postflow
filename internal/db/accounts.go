package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
)

var (
	ErrAccountNotFound      = errors.New("account not found")
	ErrAccountHasPosts      = errors.New("account has pending posts")
	ErrAccountNotDisconnect = errors.New("account must be disconnected before delete")
	ErrAccountNotXPlatform  = errors.New("x premium setting is only available for x accounts")
)

type UpsertAccountParams struct {
	Platform          domain.Platform
	DisplayName       string
	ExternalAccountID string
	XPremium          *bool
	AuthMethod        domain.AuthMethod
	Status            domain.AccountStatus
	LastError         *string
}

func (s *Store) UpsertAccount(ctx context.Context, params UpsertAccountParams) (domain.SocialAccount, error) {
	platform := domain.Platform(strings.ToLower(strings.TrimSpace(string(params.Platform))))
	externalID := strings.TrimSpace(params.ExternalAccountID)
	if platform == "" {
		return domain.SocialAccount{}, fmt.Errorf("platform is required")
	}
	if externalID == "" {
		return domain.SocialAccount{}, fmt.Errorf("external_account_id is required")
	}
	displayName := strings.TrimSpace(params.DisplayName)
	if displayName == "" {
		displayName = string(platform) + " " + externalID
	}
	authMethod := params.AuthMethod
	if strings.TrimSpace(string(authMethod)) == "" {
		authMethod = domain.AuthMethodOAuth
	}
	status := params.Status
	if strings.TrimSpace(string(status)) == "" {
		status = domain.AccountStatusConnected
	}
	xPremium := false
	if platform == domain.PlatformX && params.XPremium != nil {
		xPremium = *params.XPremium
	}
	now := time.Now().UTC()

	existing, err := s.GetAccountByPlatformExternalID(ctx, platform, externalID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.SocialAccount{}, err
	}
	if err == nil {
		nextXPremium := existing.XPremium
		if platform != domain.PlatformX {
			nextXPremium = false
		}
		if platform == domain.PlatformX && params.XPremium != nil {
			nextXPremium = *params.XPremium
		}
		_, updateErr := s.db.ExecContext(ctx, `
			UPDATE accounts
			SET display_name = ?, x_premium = ?, auth_method = ?, status = ?, last_error = ?, updated_at = ?
			WHERE id = ?
		`, displayName, boolToSQLiteInt(nextXPremium), authMethod, status, sqlNullString(params.LastError), now.Format(time.RFC3339Nano), existing.ID)
		if updateErr != nil {
			return domain.SocialAccount{}, updateErr
		}
		return s.GetAccount(ctx, existing.ID)
	}

	id, err := NewID("acc")
	if err != nil {
		return domain.SocialAccount{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO accounts (id, platform, display_name, external_account_id, x_premium, auth_method, status, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, platform, displayName, externalID, boolToSQLiteInt(xPremium), authMethod, status, sqlNullString(params.LastError), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return domain.SocialAccount{}, err
	}
	return s.GetAccount(ctx, id)
}

func (s *Store) SaveAccountCredentials(ctx context.Context, accountID string, encrypted EncryptedCredentials) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return fmt.Errorf("account_id is required")
	}
	if len(encrypted.Ciphertext) == 0 || len(encrypted.Nonce) == 0 {
		return fmt.Errorf("ciphertext and nonce are required")
	}
	updatedAt := encrypted.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO account_credentials (account_id, ciphertext, nonce, key_version, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(account_id) DO UPDATE SET
			ciphertext = excluded.ciphertext,
			nonce = excluded.nonce,
			key_version = excluded.key_version,
			updated_at = excluded.updated_at
	`, accountID, encrypted.Ciphertext, encrypted.Nonce, encrypted.KeyVersion, updatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) GetAccountCredentials(ctx context.Context, accountID string) (EncryptedCredentials, error) {
	var creds EncryptedCredentials
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT ciphertext, nonce, key_version, updated_at
		FROM account_credentials
		WHERE account_id = ?
	`, strings.TrimSpace(accountID)).Scan(&creds.Ciphertext, &creds.Nonce, &creds.KeyVersion, &updatedAt)
	if err != nil {
		return EncryptedCredentials{}, err
	}
	creds.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return creds, nil
}

func (s *Store) GetAccount(ctx context.Context, id string) (domain.SocialAccount, error) {
	var out domain.SocialAccount
	var lastError sql.NullString
	var xPremium int
	var created, updated string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, platform, display_name, external_account_id, x_premium, auth_method, status, last_error, created_at, updated_at
		FROM accounts
		WHERE id = ?
	`, strings.TrimSpace(id)).Scan(&out.ID, &out.Platform, &out.DisplayName, &out.ExternalAccountID, &xPremium, &out.AuthMethod, &out.Status, &lastError, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.SocialAccount{}, ErrAccountNotFound
		}
		return domain.SocialAccount{}, err
	}
	if lastError.Valid {
		value := strings.TrimSpace(lastError.String)
		if value != "" {
			out.LastError = &value
		}
	}
	out.XPremium = xPremium != 0
	out.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	out.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return out, nil
}

func (s *Store) GetAccountByPlatformExternalID(ctx context.Context, platform domain.Platform, externalAccountID string) (domain.SocialAccount, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM accounts WHERE platform = ? AND external_account_id = ?`, strings.TrimSpace(string(platform)), strings.TrimSpace(externalAccountID)).Scan(&id)
	if err != nil {
		return domain.SocialAccount{}, err
	}
	return s.GetAccount(ctx, id)
}

func (s *Store) ListAccounts(ctx context.Context) ([]domain.SocialAccount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, platform, display_name, external_account_id, x_premium, auth_method, status, last_error, created_at, updated_at
		FROM accounts
		ORDER BY platform ASC, display_name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SocialAccount, 0)
	for rows.Next() {
		var item domain.SocialAccount
		var lastError sql.NullString
		var xPremium int
		var created, updated string
		if err := rows.Scan(&item.ID, &item.Platform, &item.DisplayName, &item.ExternalAccountID, &xPremium, &item.AuthMethod, &item.Status, &lastError, &created, &updated); err != nil {
			return nil, err
		}
		if lastError.Valid {
			value := strings.TrimSpace(lastError.String)
			if value != "" {
				item.LastError = &value
			}
		}
		item.XPremium = xPremium != 0
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) UpdateAccountXPremium(ctx context.Context, id string, premium bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrAccountNotFound
	}
	account, err := s.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	if account.Platform != domain.PlatformX {
		return ErrAccountNotXPlatform
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE accounts
		SET x_premium = ?, updated_at = ?
		WHERE id = ?
	`, boolToSQLiteInt(premium), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrAccountNotFound
	}
	return nil
}

func boolToSQLiteInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) DisconnectAccount(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `
		UPDATE accounts
		SET status = ?, updated_at = ?
		WHERE id = ?
	`, domain.AccountStatusDisconnected, now, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrAccountNotFound
	}
	return nil
}

func (s *Store) UpdateAccountStatus(ctx context.Context, id string, status domain.AccountStatus, lastError *string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `
		UPDATE accounts
		SET status = ?, last_error = ?, updated_at = ?
		WHERE id = ?
	`, status, sqlNullString(lastError), now, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrAccountNotFound
	}
	return nil
}

func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrAccountNotFound
	}
	account, err := s.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	if account.Status != domain.AccountStatusDisconnected {
		return ErrAccountNotDisconnect
	}
	var pending int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM posts
		WHERE account_id = ?
		  AND status IN (?, ?, ?)
	`, id, domain.PostStatusDraft, domain.PostStatusScheduled, domain.PostStatusPublishing).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return ErrAccountHasPosts
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM dead_letters WHERE post_id IN (SELECT id FROM posts WHERE account_id = ?)`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM post_media WHERE post_id IN (SELECT id FROM posts WHERE account_id = ?)`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM posts WHERE account_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_credentials WHERE account_id = ?`, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrAccountNotFound
	}
	return tx.Commit()
}

func (s *Store) CreateOAuthState(ctx context.Context, state domain.OauthState) (domain.OauthState, error) {
	if strings.TrimSpace(state.ID) == "" {
		id, err := NewID("oas")
		if err != nil {
			return domain.OauthState{}, err
		}
		state.ID = id
	}
	if strings.TrimSpace(state.State) == "" {
		return domain.OauthState{}, fmt.Errorf("state is required")
	}
	if strings.TrimSpace(string(state.Platform)) == "" {
		return domain.OauthState{}, fmt.Errorf("platform is required")
	}
	if state.ExpiresAt.IsZero() {
		state.ExpiresAt = time.Now().UTC().Add(10 * time.Minute)
	}
	state.CreatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO oauth_states (id, platform, state, code_verifier, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, state.ID, strings.TrimSpace(string(state.Platform)), strings.TrimSpace(state.State), strings.TrimSpace(state.CodeVerifier), state.ExpiresAt.Format(time.RFC3339Nano), state.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return domain.OauthState{}, err
	}
	return state, nil
}

func (s *Store) ConsumeOAuthState(ctx context.Context, stateRaw string) (domain.OauthState, error) {
	stateRaw = strings.TrimSpace(stateRaw)
	if stateRaw == "" {
		return domain.OauthState{}, sql.ErrNoRows
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.OauthState{}, err
	}
	defer tx.Rollback()

	var state domain.OauthState
	var expires, created string
	err = tx.QueryRowContext(ctx, `
		SELECT id, platform, state, code_verifier, expires_at, created_at
		FROM oauth_states
		WHERE state = ?
	`, stateRaw).Scan(&state.ID, &state.Platform, &state.State, &state.CodeVerifier, &expires, &created)
	if err != nil {
		return domain.OauthState{}, err
	}
	state.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
	state.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if state.ExpiresAt.Before(time.Now().UTC()) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_states WHERE id = ?`, state.ID); err != nil {
			return domain.OauthState{}, err
		}
		if err := tx.Commit(); err != nil {
			return domain.OauthState{}, err
		}
		return domain.OauthState{}, fmt.Errorf("oauth state expired")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_states WHERE id = ?`, state.ID); err != nil {
		return domain.OauthState{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.OauthState{}, err
	}
	return state, nil
}

func (s *Store) GetAnyConnectedAccountForPlatform(ctx context.Context, platform domain.Platform) (domain.SocialAccount, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM accounts
		WHERE platform = ? AND status = ?
		ORDER BY created_at ASC
		LIMIT 1
	`, strings.TrimSpace(string(platform)), domain.AccountStatusConnected).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.SocialAccount{}, ErrAccountNotFound
		}
		return domain.SocialAccount{}, err
	}
	return s.GetAccount(ctx, id)
}
