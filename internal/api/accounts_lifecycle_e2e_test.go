package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

type oauthLifecycleProvider struct {
}

func (p *oauthLifecycleProvider) Platform() domain.Platform {
	return domain.PlatformLinkedIn
}

func (p *oauthLifecycleProvider) ValidateDraft(_ context.Context, _ domain.SocialAccount, _ postflow.Draft) ([]string, error) {
	return nil, nil
}

func (p *oauthLifecycleProvider) Publish(_ context.Context, _ domain.SocialAccount, _ postflow.Credentials, _ domain.Post, _ postflow.PublishOptions) (postflow.PublishResult, error) {
	return postflow.PublishResult{ExternalID: "ok"}, nil
}

func (p *oauthLifecycleProvider) RefreshIfNeeded(_ context.Context, _ domain.SocialAccount, credentials postflow.Credentials) (postflow.Credentials, bool, error) {
	return credentials, false, nil
}

func (p *oauthLifecycleProvider) StartOAuth(_ context.Context, in postflow.OAuthStartInput) (postflow.OAuthStartOutput, error) {
	return postflow.OAuthStartOutput{
		AuthURL: "https://oauth.example/auth?state=" + url.QueryEscape(in.State),
	}, nil
}

func (p *oauthLifecycleProvider) HandleOAuthCallback(_ context.Context, _ postflow.OAuthCallbackInput) ([]postflow.ConnectedAccount, error) {
	return []postflow.ConnectedAccount{
		{
			Platform:          domain.PlatformLinkedIn,
			AccountKind:       domain.AccountKindPersonal,
			DisplayName:       "LinkedIn OAuth",
			ExternalAccountID: "li-oauth-e2e",
			Credentials: postflow.Credentials{
				AccessToken:  "oauth_access_token",
				RefreshToken: "oauth_refresh_token",
				TokenType:    "Bearer",
				Extra: map[string]string{
					"scope": "r_liteprofile w_member_social",
				},
			},
		},
		{
			Platform:          domain.PlatformLinkedIn,
			AccountKind:       domain.AccountKindOrganization,
			DisplayName:       "LinkedIn Org OAuth",
			ExternalAccountID: "li-org-oauth-e2e",
			Credentials: postflow.Credentials{
				AccessToken:  "oauth_access_token",
				RefreshToken: "oauth_refresh_token",
				TokenType:    "Bearer",
			},
		},
	}, nil
}

func TestAccountsStaticLifecycleJSON(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		Registry:          postflow.NewProviderRegistry(postflow.NewMockProvider(domain.PlatformX)),
	}
	h := srv.Handler()

	createPayload := map[string]any{
		"platform":            "x",
		"display_name":        "X Primary",
		"external_account_id": "x-primary-e2e",
		"credentials": map[string]any{
			"access_token":        "token_x",
			"access_token_secret": "secret_x",
			"token_type":          "oauth1",
		},
	}
	createStatus, createRaw := apiTestJSON(t, h, http.MethodPost, "/accounts/static", createPayload)
	if createStatus != http.StatusCreated {
		t.Fatalf("expected 201 creating static account, got %d body=%s", createStatus, string(createRaw))
	}
	var created domain.SocialAccount
	mustDecodeJSON(t, createRaw, &created)
	if created.Status != domain.AccountStatusConnected {
		t.Fatalf("expected created account to be connected, got %s", created.Status)
	}

	listStatus, listRaw := apiTestJSON(t, h, http.MethodGet, "/accounts", nil)
	if listStatus != http.StatusOK {
		t.Fatalf("expected 200 listing accounts, got %d body=%s", listStatus, string(listRaw))
	}
	var listed struct {
		Count int                    `json:"count"`
		Items []domain.SocialAccount `json:"items"`
	}
	mustDecodeJSON(t, listRaw, &listed)
	if listed.Count != 1 || len(listed.Items) != 1 {
		t.Fatalf("expected exactly one listed account, got count=%d items=%d", listed.Count, len(listed.Items))
	}
	if listed.Items[0].Status != domain.AccountStatusConnected {
		t.Fatalf("expected listed account connected, got %s", listed.Items[0].Status)
	}

	deleteWhileConnectedStatus, deleteWhileConnectedRaw := apiTestJSON(t, h, http.MethodDelete, "/accounts/"+created.ID, nil)
	if deleteWhileConnectedStatus != http.StatusConflict {
		t.Fatalf("expected 409 deleting connected account, got %d body=%s", deleteWhileConnectedStatus, string(deleteWhileConnectedRaw))
	}
	assertAPIErrorContains(t, deleteWhileConnectedRaw, "account must be disconnected first")

	disconnectStatus, disconnectRaw := apiTestJSON(t, h, http.MethodPost, "/accounts/"+created.ID+"/disconnect", nil)
	if disconnectStatus != http.StatusOK {
		t.Fatalf("expected 200 disconnecting account, got %d body=%s", disconnectStatus, string(disconnectRaw))
	}
	var disconnected struct {
		ID     string               `json:"id"`
		Status domain.AccountStatus `json:"status"`
	}
	mustDecodeJSON(t, disconnectRaw, &disconnected)
	if disconnected.Status != domain.AccountStatusDisconnected {
		t.Fatalf("expected disconnected status, got %s", disconnected.Status)
	}

	connectStatus, connectRaw := apiTestJSON(t, h, http.MethodPost, "/accounts/"+created.ID+"/connect", nil)
	if connectStatus != http.StatusOK {
		t.Fatalf("expected 200 reconnecting account, got %d body=%s", connectStatus, string(connectRaw))
	}
	var reconnected struct {
		ID     string               `json:"id"`
		Status domain.AccountStatus `json:"status"`
	}
	mustDecodeJSON(t, connectRaw, &reconnected)
	if reconnected.Status != domain.AccountStatusConnected {
		t.Fatalf("expected reconnected status, got %s", reconnected.Status)
	}

	if err := store.DisconnectAccount(t.Context(), created.ID); err != nil {
		t.Fatalf("disconnect for final delete: %v", err)
	}
	deleteStatus, deleteRaw := apiTestJSON(t, h, http.MethodDelete, "/accounts/"+created.ID, nil)
	if deleteStatus != http.StatusOK {
		t.Fatalf("expected 200 deleting disconnected account, got %d body=%s", deleteStatus, string(deleteRaw))
	}
	var deleted struct {
		Deleted bool   `json:"deleted"`
		ID      string `json:"id"`
	}
	mustDecodeJSON(t, deleteRaw, &deleted)
	if !deleted.Deleted || deleted.ID != created.ID {
		t.Fatalf("expected deleted=true for %s, got deleted=%v id=%s", created.ID, deleted.Deleted, deleted.ID)
	}

	withoutCreds, err := store.UpsertAccount(t.Context(), db.UpsertAccountParams{
		Platform:          domain.PlatformLinkedIn,
		DisplayName:       "LinkedIn No Credentials",
		ExternalAccountID: "li-no-creds",
		AuthMethod:        domain.AuthMethodOAuth,
		Status:            domain.AccountStatusDisconnected,
	})
	if err != nil {
		t.Fatalf("create disconnected account without credentials: %v", err)
	}
	connectWithoutCredsStatus, connectWithoutCredsRaw := apiTestJSON(t, h, http.MethodPost, "/accounts/"+withoutCreds.ID+"/connect", nil)
	if connectWithoutCredsStatus != http.StatusConflict {
		t.Fatalf("expected 409 connecting account without credentials, got %d body=%s", connectWithoutCredsStatus, string(connectWithoutCredsRaw))
	}
	assertAPIErrorContains(t, connectWithoutCredsRaw, "account has no saved credentials")
}

func TestOAuthJSONLifecycleIncludesReplayInvalidAndExpiredState(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	provider := &oauthLifecycleProvider{}
	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		Registry:          postflow.NewProviderRegistry(provider),
		PublicBaseURL:     "https://postflow.example",
	}
	h := srv.Handler()

	startStatus, startRaw := apiTestJSON(t, h, http.MethodPost, "/oauth/linkedin/start", nil)
	if startStatus != http.StatusOK {
		t.Fatalf("expected 200 starting oauth, got %d body=%s", startStatus, string(startRaw))
	}
	var started struct {
		Platform domain.Platform `json:"platform"`
		AuthURL  string          `json:"auth_url"`
		State    string          `json:"state"`
		Expires  string          `json:"expires"`
	}
	mustDecodeJSON(t, startRaw, &started)
	if started.Platform != domain.PlatformLinkedIn {
		t.Fatalf("expected linkedin platform from start, got %s", started.Platform)
	}
	if strings.TrimSpace(started.State) == "" {
		t.Fatalf("expected oauth start state to be present")
	}

	callbackStatus, callbackRaw := apiTestJSON(
		t,
		h,
		http.MethodGet,
		"/oauth/linkedin/callback?state="+url.QueryEscape(started.State)+"&code=valid_code",
		nil,
	)
	if callbackStatus != http.StatusOK {
		t.Fatalf("expected 200 oauth callback success, got %d body=%s", callbackStatus, string(callbackRaw))
	}
	var callbackPayload struct {
		Platform domain.Platform        `json:"platform"`
		Count    int                    `json:"count"`
		Items    []domain.SocialAccount `json:"items"`
	}
	mustDecodeJSON(t, callbackRaw, &callbackPayload)
	if callbackPayload.Platform != domain.PlatformLinkedIn || callbackPayload.Count != 2 || len(callbackPayload.Items) != 2 {
		t.Fatalf("unexpected oauth callback payload: %+v", callbackPayload)
	}
	persistedAfterFirst, err := store.GetAccountByPlatformExternalID(t.Context(), domain.PlatformLinkedIn, domain.AccountKindPersonal, "li-oauth-e2e")
	if err != nil {
		t.Fatalf("expected linked account to be persisted after oauth callback: %v", err)
	}
	if persistedAfterFirst.ID != callbackPayload.Items[0].ID {
		t.Fatalf("expected callback payload account id %s to match persisted id %s", callbackPayload.Items[0].ID, persistedAfterFirst.ID)
	}
	persistedOrgAfterFirst, err := store.GetAccountByPlatformExternalID(t.Context(), domain.PlatformLinkedIn, domain.AccountKindOrganization, "li-org-oauth-e2e")
	if err != nil {
		t.Fatalf("expected linked organization account to be persisted after oauth callback: %v", err)
	}
	if persistedOrgAfterFirst.ID != callbackPayload.Items[1].ID {
		t.Fatalf("expected callback payload org account id %s to match persisted id %s", callbackPayload.Items[1].ID, persistedOrgAfterFirst.ID)
	}

	encrypted, err := store.GetAccountCredentials(t.Context(), callbackPayload.Items[0].ID)
	if err != nil {
		t.Fatalf("expected encrypted credentials to be persisted after oauth callback: %v", err)
	}
	if len(encrypted.Ciphertext) == 0 || len(encrypted.Nonce) == 0 {
		t.Fatalf("expected non-empty encrypted credentials after oauth callback")
	}

	replayStatus, replayRaw := apiTestJSON(
		t,
		h,
		http.MethodGet,
		"/oauth/linkedin/callback?state="+url.QueryEscape(started.State)+"&code=valid_code",
		nil,
	)
	if replayStatus != http.StatusOK {
		t.Fatalf("expected 200 oauth replay callback, got %d body=%s", replayStatus, string(replayRaw))
	}
	var replayPayload struct {
		Platform domain.Platform `json:"platform"`
		Status   string          `json:"status"`
	}
	mustDecodeJSON(t, replayRaw, &replayPayload)
	if replayPayload.Status != "already_processed" {
		t.Fatalf("expected replay status already_processed, got %q", replayPayload.Status)
	}
	persistedAfterReplay, err := store.GetAccountByPlatformExternalID(t.Context(), domain.PlatformLinkedIn, domain.AccountKindPersonal, "li-oauth-e2e")
	if err != nil {
		t.Fatalf("expected linked account to remain persisted after replay: %v", err)
	}
	if persistedAfterReplay.ID != persistedAfterFirst.ID {
		t.Fatalf("expected replay to keep same linked account id, got first=%s replay=%s", persistedAfterFirst.ID, persistedAfterReplay.ID)
	}
	persistedOrgAfterReplay, err := store.GetAccountByPlatformExternalID(t.Context(), domain.PlatformLinkedIn, domain.AccountKindOrganization, "li-org-oauth-e2e")
	if err != nil {
		t.Fatalf("expected linked organization account to remain persisted after replay: %v", err)
	}
	if persistedOrgAfterReplay.ID != persistedOrgAfterFirst.ID {
		t.Fatalf("expected replay to keep same linked org account id, got first=%s replay=%s", persistedOrgAfterFirst.ID, persistedOrgAfterReplay.ID)
	}
	accountsAfterReplay, err := store.ListAccounts(t.Context())
	if err != nil {
		t.Fatalf("list accounts after oauth replay: %v", err)
	}
	if len(accountsAfterReplay) != 2 {
		t.Fatalf("expected replay to avoid duplicating linked accounts, got %d", len(accountsAfterReplay))
	}

	invalidStatus, invalidRaw := apiTestJSON(t, h, http.MethodGet, "/oauth/linkedin/callback?state=missing_state&code=valid_code", nil)
	if invalidStatus != http.StatusBadRequest {
		t.Fatalf("expected 400 invalid oauth state, got %d body=%s", invalidStatus, string(invalidRaw))
	}
	assertAPIErrorContains(t, invalidRaw, "invalid oauth state")

	expiredState := "state_expired_" + strings.ReplaceAll(t.Name(), "/", "_")
	_, err = store.CreateOAuthState(t.Context(), domain.OauthState{
		Platform:     domain.PlatformLinkedIn,
		State:        expiredState,
		CodeVerifier: "expired_verifier",
		ExpiresAt:    time.Now().UTC().Add(-1 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create expired oauth state: %v", err)
	}
	expiredStatus, expiredRaw := apiTestJSON(
		t,
		h,
		http.MethodGet,
		"/oauth/linkedin/callback?state="+url.QueryEscape(expiredState)+"&code=valid_code",
		nil,
	)
	if expiredStatus != http.StatusBadRequest {
		t.Fatalf("expected 400 for expired oauth state, got %d body=%s", expiredStatus, string(expiredRaw))
	}
	assertAPIErrorContains(t, expiredRaw, "oauth state expired")

	_, err = store.ConsumeOAuthState(t.Context(), expiredState)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected expired oauth state to be deleted after failed consume, got %v", err)
	}
}

func apiTestJSON(t *testing.T, h http.Handler, method, path string, payload any) (int, []byte) {
	t.Helper()
	var body *bytes.Reader
	if payload == nil {
		body = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

func mustDecodeJSON(t *testing.T, raw []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode json: %v body=%s", err, string(raw))
	}
}

func assertAPIErrorContains(t *testing.T, raw []byte, expected string) {
	t.Helper()
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode error payload: %v body=%s", err, string(raw))
	}
	if !strings.Contains(strings.ToLower(strings.TrimSpace(payload.Error)), strings.ToLower(strings.TrimSpace(expected))) {
		t.Fatalf("expected error to contain %q, got %q", expected, payload.Error)
	}
}
