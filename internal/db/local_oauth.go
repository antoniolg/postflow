package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrOAuthClientNotFound       = errors.New("oauth client not found")
	ErrOAuthCodeInvalid          = errors.New("oauth code is invalid")
	ErrOAuthCodeAlreadyUsed      = errors.New("oauth code already used")
	ErrOAuthRefreshTokenInvalid  = errors.New("oauth refresh token is invalid")
	ErrOAuthAccessTokenInvalid   = errors.New("oauth access token is invalid")
	ErrOAuthRedirectURIMismatch  = errors.New("oauth redirect uri mismatch")
	ErrOAuthClientRedirectsEmpty = errors.New("oauth redirect uris are required")
)

type OAuthClient struct {
	ID                      string
	ClientID                string
	RedirectURIs            []string
	TokenEndpointAuthMethod string
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type OAuthAuthorizationCode struct {
	ID                  string
	ClientID            string
	OwnerID             string
	CodeHash            string
	RedirectURI         string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
	ExpiresAt           time.Time
	CreatedAt           time.Time
	UsedAt              *time.Time
}

type OAuthToken struct {
	ID               string
	ClientID         string
	OwnerID          string
	AccessTokenHash  string
	RefreshTokenHash string
	Scope            string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	CreatedAt        time.Time
	LastUsedAt       *time.Time
	RevokedAt        *time.Time
}

type CreateOAuthAuthorizationCodeParams struct {
	ClientID            string
	OwnerID             string
	RedirectURI         string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
	TTL                 time.Duration
}

type CreateOAuthTokenParams struct {
	ClientID   string
	OwnerID    string
	Scope      string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

func (s *Store) RegisterOAuthClient(ctx context.Context, redirectURIs []string) (OAuthClient, error) {
	normalized := normalizeRedirectURIs(redirectURIs)
	if len(normalized) == 0 {
		return OAuthClient{}, ErrOAuthClientRedirectsEmpty
	}
	now := time.Now().UTC()
	rawRedirects, err := json.Marshal(normalized)
	if err != nil {
		return OAuthClient{}, err
	}
	client := OAuthClient{
		ID:                      mustStoreID("ocl"),
		ClientID:                mustStoreID("client"),
		RedirectURIs:            normalized,
		TokenEndpointAuthMethod: "none",
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_clients (id, client_id, redirect_uris_json, token_endpoint_auth_method, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, client.ID, client.ClientID, string(rawRedirects), client.TokenEndpointAuthMethod, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return OAuthClient{}, err
	}
	return client, nil
}

func (s *Store) GetOAuthClientByClientID(ctx context.Context, clientID string) (OAuthClient, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return OAuthClient{}, ErrOAuthClientNotFound
	}
	var client OAuthClient
	var redirectURIsJSON string
	var createdAt string
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, client_id, redirect_uris_json, token_endpoint_auth_method, created_at, updated_at
		FROM oauth_clients
		WHERE client_id = ?
	`, clientID).Scan(&client.ID, &client.ClientID, &redirectURIsJSON, &client.TokenEndpointAuthMethod, &createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OAuthClient{}, ErrOAuthClientNotFound
		}
		return OAuthClient{}, err
	}
	if err := json.Unmarshal([]byte(redirectURIsJSON), &client.RedirectURIs); err != nil {
		return OAuthClient{}, err
	}
	client.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	client.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return client, nil
}

func (s *Store) CreateOAuthAuthorizationCode(ctx context.Context, params CreateOAuthAuthorizationCodeParams) (string, OAuthAuthorizationCode, error) {
	clientID := strings.TrimSpace(params.ClientID)
	ownerID := strings.TrimSpace(params.OwnerID)
	redirectURI := strings.TrimSpace(params.RedirectURI)
	if clientID == "" || ownerID == "" || redirectURI == "" {
		return "", OAuthAuthorizationCode{}, fmt.Errorf("client_id, owner_id and redirect_uri are required")
	}
	if strings.TrimSpace(params.CodeChallenge) == "" {
		return "", OAuthAuthorizationCode{}, fmt.Errorf("code_challenge is required")
	}
	if strings.TrimSpace(params.CodeChallengeMethod) == "" {
		params.CodeChallengeMethod = "S256"
	}
	if params.TTL <= 0 {
		params.TTL = 10 * time.Minute
	}
	rawCode, err := randomOpaqueToken(32)
	if err != nil {
		return "", OAuthAuthorizationCode{}, err
	}
	now := time.Now().UTC()
	code := OAuthAuthorizationCode{
		ID:                  mustStoreID("ocd"),
		ClientID:            clientID,
		OwnerID:             ownerID,
		CodeHash:            tokenHash(rawCode),
		RedirectURI:         redirectURI,
		Scope:               normalizeOAuthScope(params.Scope),
		CodeChallenge:       strings.TrimSpace(params.CodeChallenge),
		CodeChallengeMethod: strings.ToUpper(strings.TrimSpace(params.CodeChallengeMethod)),
		ExpiresAt:           now.Add(params.TTL),
		CreatedAt:           now,
	}
	_, err = s.db.ExecContext(ctx, `
		DELETE FROM oauth_codes
		WHERE expires_at <= ? OR used_at IS NOT NULL
	`, now.Format(time.RFC3339Nano))
	if err != nil {
		return "", OAuthAuthorizationCode{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_codes (id, client_id, owner_id, code_hash, redirect_uri, scope, code_challenge, code_challenge_method, expires_at, created_at, used_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
	`, code.ID, code.ClientID, code.OwnerID, code.CodeHash, code.RedirectURI, code.Scope, code.CodeChallenge, code.CodeChallengeMethod, code.ExpiresAt.Format(time.RFC3339Nano), code.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return "", OAuthAuthorizationCode{}, err
	}
	return rawCode, code, nil
}

func (s *Store) ConsumeOAuthAuthorizationCode(ctx context.Context, rawCode, clientID, redirectURI string) (OAuthAuthorizationCode, error) {
	codeHash := tokenHash(rawCode)
	clientID = strings.TrimSpace(clientID)
	redirectURI = strings.TrimSpace(redirectURI)
	if codeHash == "" || clientID == "" || redirectURI == "" {
		return OAuthAuthorizationCode{}, ErrOAuthCodeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthAuthorizationCode{}, err
	}
	defer tx.Rollback()

	var code OAuthAuthorizationCode
	var expiresAt string
	var createdAt string
	var usedAt sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT id, client_id, owner_id, code_hash, redirect_uri, scope, code_challenge, code_challenge_method, expires_at, created_at, used_at
		FROM oauth_codes
		WHERE code_hash = ?
	`, codeHash).Scan(&code.ID, &code.ClientID, &code.OwnerID, &code.CodeHash, &code.RedirectURI, &code.Scope, &code.CodeChallenge, &code.CodeChallengeMethod, &expiresAt, &createdAt, &usedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OAuthAuthorizationCode{}, ErrOAuthCodeInvalid
		}
		return OAuthAuthorizationCode{}, err
	}
	code.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresAt)
	code.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if usedAt.Valid {
		value, parseErr := time.Parse(time.RFC3339Nano, usedAt.String)
		if parseErr == nil {
			code.UsedAt = &value
		}
	}
	if code.ClientID != clientID {
		return OAuthAuthorizationCode{}, ErrOAuthCodeInvalid
	}
	if code.RedirectURI != redirectURI {
		return OAuthAuthorizationCode{}, ErrOAuthRedirectURIMismatch
	}
	if code.UsedAt != nil {
		return OAuthAuthorizationCode{}, ErrOAuthCodeAlreadyUsed
	}
	if !code.ExpiresAt.After(time.Now().UTC()) {
		_, _ = tx.ExecContext(ctx, `DELETE FROM oauth_codes WHERE id = ?`, code.ID)
		return OAuthAuthorizationCode{}, ErrOAuthCodeInvalid
	}
	usedNow := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_codes SET used_at = ? WHERE id = ?`, usedNow.Format(time.RFC3339Nano), code.ID); err != nil {
		return OAuthAuthorizationCode{}, err
	}
	if err := tx.Commit(); err != nil {
		return OAuthAuthorizationCode{}, err
	}
	code.UsedAt = &usedNow
	return code, nil
}

func (s *Store) CreateOAuthToken(ctx context.Context, params CreateOAuthTokenParams) (string, string, OAuthToken, error) {
	clientID := strings.TrimSpace(params.ClientID)
	ownerID := strings.TrimSpace(params.OwnerID)
	if clientID == "" || ownerID == "" {
		return "", "", OAuthToken{}, fmt.Errorf("client_id and owner_id are required")
	}
	if params.AccessTTL <= 0 {
		params.AccessTTL = time.Hour
	}
	if params.RefreshTTL <= 0 {
		params.RefreshTTL = 30 * 24 * time.Hour
	}
	accessToken, err := randomOpaqueToken(32)
	if err != nil {
		return "", "", OAuthToken{}, err
	}
	refreshToken, err := randomOpaqueToken(32)
	if err != nil {
		return "", "", OAuthToken{}, err
	}
	now := time.Now().UTC()
	token := OAuthToken{
		ID:               mustStoreID("otk"),
		ClientID:         clientID,
		OwnerID:          ownerID,
		AccessTokenHash:  tokenHash(accessToken),
		RefreshTokenHash: tokenHash(refreshToken),
		Scope:            normalizeOAuthScope(params.Scope),
		AccessExpiresAt:  now.Add(params.AccessTTL),
		RefreshExpiresAt: now.Add(params.RefreshTTL),
		CreatedAt:        now,
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_tokens (id, client_id, owner_id, access_token_hash, refresh_token_hash, scope, access_expires_at, refresh_expires_at, created_at, last_used_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL)
	`, token.ID, token.ClientID, token.OwnerID, token.AccessTokenHash, token.RefreshTokenHash, token.Scope, token.AccessExpiresAt.Format(time.RFC3339Nano), token.RefreshExpiresAt.Format(time.RFC3339Nano), token.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return "", "", OAuthToken{}, err
	}
	return accessToken, refreshToken, token, nil
}

func (s *Store) GetOAuthTokenByAccessToken(ctx context.Context, rawAccessToken string) (OAuthToken, error) {
	hash := tokenHash(rawAccessToken)
	if hash == "" {
		return OAuthToken{}, ErrOAuthAccessTokenInvalid
	}
	token, err := s.getOAuthTokenByColumn(ctx, "access_token_hash", hash)
	if err != nil {
		return OAuthToken{}, err
	}
	if token.RevokedAt != nil || !token.AccessExpiresAt.After(time.Now().UTC()) {
		return OAuthToken{}, ErrOAuthAccessTokenInvalid
	}
	now := time.Now().UTC()
	_, _ = s.db.ExecContext(ctx, `UPDATE oauth_tokens SET last_used_at = ? WHERE id = ?`, now.Format(time.RFC3339Nano), token.ID)
	token.LastUsedAt = &now
	return token, nil
}

func (s *Store) RotateOAuthRefreshToken(ctx context.Context, rawRefreshToken, clientID string, accessTTL, refreshTTL time.Duration) (string, string, OAuthToken, error) {
	hash := tokenHash(rawRefreshToken)
	clientID = strings.TrimSpace(clientID)
	if hash == "" || clientID == "" {
		return "", "", OAuthToken{}, ErrOAuthRefreshTokenInvalid
	}
	current, err := s.getOAuthTokenByColumn(ctx, "refresh_token_hash", hash)
	if err != nil {
		return "", "", OAuthToken{}, err
	}
	if current.ClientID != clientID || current.RevokedAt != nil || !current.RefreshExpiresAt.After(time.Now().UTC()) {
		return "", "", OAuthToken{}, ErrOAuthRefreshTokenInvalid
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE oauth_tokens SET revoked_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339Nano), current.ID); err != nil {
		return "", "", OAuthToken{}, err
	}
	return s.CreateOAuthToken(ctx, CreateOAuthTokenParams{
		ClientID:   current.ClientID,
		OwnerID:    current.OwnerID,
		Scope:      current.Scope,
		AccessTTL:  accessTTL,
		RefreshTTL: refreshTTL,
	})
}

func (s *Store) getOAuthTokenByColumn(ctx context.Context, column, hash string) (OAuthToken, error) {
	switch column {
	case "access_token_hash", "refresh_token_hash":
	default:
		return OAuthToken{}, fmt.Errorf("unsupported token column")
	}
	query := `
		SELECT id, client_id, owner_id, access_token_hash, refresh_token_hash, scope, access_expires_at, refresh_expires_at, created_at, last_used_at, revoked_at
		FROM oauth_tokens
		WHERE ` + column + ` = ?`
	var token OAuthToken
	var accessExpiresAt string
	var refreshExpiresAt string
	var createdAt string
	var lastUsedAt sql.NullString
	var revokedAt sql.NullString
	err := s.db.QueryRowContext(ctx, query, hash).Scan(
		&token.ID,
		&token.ClientID,
		&token.OwnerID,
		&token.AccessTokenHash,
		&token.RefreshTokenHash,
		&token.Scope,
		&accessExpiresAt,
		&refreshExpiresAt,
		&createdAt,
		&lastUsedAt,
		&revokedAt,
	)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows) && column == "access_token_hash":
			return OAuthToken{}, ErrOAuthAccessTokenInvalid
		case errors.Is(err, sql.ErrNoRows):
			return OAuthToken{}, ErrOAuthRefreshTokenInvalid
		default:
			return OAuthToken{}, err
		}
	}
	token.AccessExpiresAt, _ = time.Parse(time.RFC3339Nano, accessExpiresAt)
	token.RefreshExpiresAt, _ = time.Parse(time.RFC3339Nano, refreshExpiresAt)
	token.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if lastUsedAt.Valid {
		if parsed, err := time.Parse(time.RFC3339Nano, lastUsedAt.String); err == nil {
			token.LastUsedAt = &parsed
		}
	}
	if revokedAt.Valid {
		if parsed, err := time.Parse(time.RFC3339Nano, revokedAt.String); err == nil {
			token.RevokedAt = &parsed
		}
	}
	return token, nil
}

func normalizeRedirectURIs(uris []string) []string {
	seen := make(map[string]struct{}, len(uris))
	out := make([]string, 0, len(uris))
	for _, raw := range uris {
		uri := strings.TrimSpace(raw)
		if uri == "" {
			continue
		}
		if _, ok := seen[uri]; ok {
			continue
		}
		seen[uri] = struct{}{}
		out = append(out, uri)
	}
	return out
}

func normalizeOAuthScope(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "mcp"
	}
	parts := strings.Fields(raw)
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	if len(out) == 0 {
		return "mcp"
	}
	return strings.Join(out, " ")
}
