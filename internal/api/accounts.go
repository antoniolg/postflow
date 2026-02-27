package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
)

type createStaticAccountRequest struct {
	Platform          string         `json:"platform"`
	DisplayName       string         `json:"display_name"`
	ExternalAccountID string         `json:"external_account_id"`
	Credentials       map[string]any `json:"credentials"`
}

func (s Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.Store.ListAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	accept := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept")))
	if strings.Contains(accept, "text/html") {
		s.renderAccountsHTML(w, r, accounts)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count": len(accounts),
		"items": accounts,
	})
}

func (s Server) renderAccountsHTML(w http.ResponseWriter, _ *http.Request, accounts []domain.SocialAccount) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	b.WriteString("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	b.WriteString("<title>publisher · accounts</title><style>body{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;background:#111;color:#eee;padding:24px}a{color:#f60}table{width:100%;border-collapse:collapse;margin-top:16px}th,td{border-bottom:1px solid #333;padding:10px;text-align:left}code{background:#222;padding:2px 6px;border-radius:4px}.muted{color:#aaa}.actions form{display:inline-block;margin-right:8px}</style></head><body>")
	b.WriteString("<h1>Connected accounts</h1><p class=\"muted\">Use POST /oauth/{platform}/start to connect OAuth accounts or POST /accounts/static to create static ones.</p>")
	b.WriteString("<p><a href=\"/\">Back to scheduler</a></p>")
	b.WriteString("<table><thead><tr><th>ID</th><th>Platform</th><th>Name</th><th>External ID</th><th>Auth</th><th>Status</th><th>Actions</th></tr></thead><tbody>")
	if len(accounts) == 0 {
		b.WriteString("<tr><td colspan=\"7\" class=\"muted\">No accounts connected yet.</td></tr>")
	}
	for _, account := range accounts {
		b.WriteString("<tr>")
		b.WriteString("<td><code>" + templateEscape(account.ID) + "</code></td>")
		b.WriteString("<td>" + templateEscape(string(account.Platform)) + "</td>")
		b.WriteString("<td>" + templateEscape(account.DisplayName) + "</td>")
		b.WriteString("<td>" + templateEscape(account.ExternalAccountID) + "</td>")
		b.WriteString("<td>" + templateEscape(string(account.AuthMethod)) + "</td>")
		b.WriteString("<td>" + templateEscape(string(account.Status)) + "</td>")
		b.WriteString("<td class=\"actions\">")
		if account.Status == domain.AccountStatusConnected {
			b.WriteString("<form method=\"post\" action=\"/accounts/" + templateEscape(account.ID) + "/disconnect\"><button type=\"submit\">Disconnect</button></form>")
		}
		if account.Status == domain.AccountStatusDisconnected {
			b.WriteString("<form method=\"post\" action=\"/accounts/" + templateEscape(account.ID) + "/delete\" onsubmit=\"return confirm('Delete account?')\"><button type=\"submit\">Delete</button></form>")
		}
		b.WriteString("</td>")
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")
	b.WriteString("<h2>Connect</h2><ul>")
	b.WriteString("<li><form method=\"post\" action=\"/oauth/linkedin/start\"><button type=\"submit\">Connect LinkedIn</button></form></li>")
	b.WriteString("<li><form method=\"post\" action=\"/oauth/facebook/start\"><button type=\"submit\">Connect Facebook</button></form></li>")
	b.WriteString("<li><form method=\"post\" action=\"/oauth/instagram/start\"><button type=\"submit\">Connect Instagram</button></form></li>")
	b.WriteString("</ul></body></html>")
	_, _ = w.Write([]byte(b.String()))
}

func (s Server) handleCreateStaticAccount(w http.ResponseWriter, r *http.Request) {
	var req createStaticAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json body: %w", err))
		return
	}
	platform := normalizePlatform(req.Platform)
	if platform == "" {
		writeError(w, http.StatusBadRequest, errors.New("platform is required"))
		return
	}
	provider, ok := s.providerRegistry().Get(platform)
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("provider is not configured for platform"))
		return
	}
	_ = provider

	credentials, err := decodeCredentials(req.Credentials)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(credentials.AccessToken) == "" {
		writeError(w, http.StatusBadRequest, errors.New("credentials.access_token is required"))
		return
	}
	if platform == domain.PlatformX && strings.TrimSpace(credentials.AccessTokenSecret) == "" {
		writeError(w, http.StatusBadRequest, errors.New("credentials.access_token_secret is required for x"))
		return
	}
	account, err := s.Store.UpsertAccount(r.Context(), db.UpsertAccountParams{
		Platform:          platform,
		DisplayName:       strings.TrimSpace(req.DisplayName),
		ExternalAccountID: strings.TrimSpace(req.ExternalAccountID),
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.saveCredentials(r.Context(), account.ID, credentials); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, account)
}

func (s Server) handleAccountActions(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(strings.TrimSpace(r.URL.Path), "/accounts/")
	if path == "" {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	accountID := strings.TrimSpace(parts[0])
	action := strings.TrimSpace(parts[1])
	if accountID == "" {
		writeError(w, http.StatusBadRequest, errors.New("account id is required"))
		return
	}
	switch action {
	case "disconnect":
		if err := s.Store.DisconnectAccount(r.Context(), accountID); err != nil {
			if errors.Is(err, db.ErrAccountNotFound) {
				writeError(w, http.StatusNotFound, errors.New("account not found"))
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if wantsHTML(r) {
			http.Redirect(w, r, "/accounts", http.StatusSeeOther)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": accountID, "status": domain.AccountStatusDisconnected})
	case "delete":
		if err := s.Store.DeleteAccount(r.Context(), accountID); err != nil {
			switch {
			case errors.Is(err, db.ErrAccountNotFound):
				writeError(w, http.StatusNotFound, errors.New("account not found"))
			case errors.Is(err, db.ErrAccountNotDisconnect):
				writeError(w, http.StatusConflict, errors.New("account must be disconnected first"))
			case errors.Is(err, db.ErrAccountHasPosts):
				writeError(w, http.StatusConflict, errors.New("account has existing posts"))
			default:
				writeError(w, http.StatusInternalServerError, err)
			}
			return
		}
		if wantsHTML(r) {
			http.Redirect(w, r, "/accounts", http.StatusSeeOther)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": accountID})
	default:
		writeError(w, http.StatusNotFound, errors.New("not found"))
	}
}

func (s Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimPrefix(strings.TrimSpace(r.URL.Path), "/accounts/")
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		writeError(w, http.StatusBadRequest, errors.New("account id is required"))
		return
	}
	if strings.Contains(accountID, "/") {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	if err := s.Store.DeleteAccount(r.Context(), accountID); err != nil {
		if errors.Is(err, db.ErrAccountNotFound) {
			writeError(w, http.StatusNotFound, errors.New("account not found"))
			return
		}
		switch {
		case errors.Is(err, db.ErrAccountNotFound):
			writeError(w, http.StatusNotFound, errors.New("account not found"))
		case errors.Is(err, db.ErrAccountNotDisconnect):
			writeError(w, http.StatusConflict, errors.New("account must be disconnected first"))
		case errors.Is(err, db.ErrAccountHasPosts):
			writeError(w, http.StatusConflict, errors.New("account has existing posts"))
		default:
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": accountID})
}

func (s Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	platform, suffix, ok := parseOAuthPath(strings.TrimSpace(r.URL.Path))
	if !ok || suffix != "start" {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	provider, ok := s.providerRegistry().GetOAuth(platform)
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("oauth is not available for platform"))
		return
	}
	state := mustID("state")
	codeVerifier := mustID("verifier")
	recorded, err := s.Store.CreateOAuthState(r.Context(), domain.OauthState{
		Platform:     platform,
		State:        state,
		CodeVerifier: codeVerifier,
		ExpiresAt:    time.Now().UTC().Add(10 * time.Minute),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	redirectURL := s.oauthCallbackURL(r, platform)
	out, err := provider.StartOAuth(r.Context(), publisher.OAuthStartInput{
		State:        recorded.State,
		CodeVerifier: recorded.CodeVerifier,
		RedirectURL:  redirectURL,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if wantsHTML(r) {
		http.Redirect(w, r, out.AuthURL, http.StatusFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"platform": platform,
		"auth_url": out.AuthURL,
		"state":    recorded.State,
		"expires":  recorded.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (s Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	platform, suffix, ok := parseOAuthPath(strings.TrimSpace(r.URL.Path))
	if !ok || suffix != "callback" {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	provider, ok := s.providerRegistry().GetOAuth(platform)
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("oauth is not available for platform"))
		return
	}
	stateRaw := strings.TrimSpace(r.URL.Query().Get("state"))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		if errDesc := strings.TrimSpace(r.URL.Query().Get("error_description")); errDesc != "" {
			writeError(w, http.StatusBadRequest, errors.New(errDesc))
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("missing authorization code"))
		return
	}
	recorded, err := s.Store.ConsumeOAuthState(r.Context(), stateRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusBadRequest, errors.New("invalid oauth state"))
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	connected, err := provider.HandleOAuthCallback(r.Context(), publisher.OAuthCallbackInput{
		Code:         code,
		State:        recorded.State,
		CodeVerifier: recorded.CodeVerifier,
		RedirectURL:  s.oauthCallbackURL(r, platform),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	created := make([]domain.SocialAccount, 0, len(connected))
	for _, item := range connected {
		account, err := s.Store.UpsertAccount(r.Context(), db.UpsertAccountParams{
			Platform:          item.Platform,
			DisplayName:       item.DisplayName,
			ExternalAccountID: item.ExternalAccountID,
			AuthMethod:        domain.AuthMethodOAuth,
			Status:            domain.AccountStatusConnected,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if err := s.saveCredentials(r.Context(), account.ID, item.Credentials); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		created = append(created, account)
	}
	if wantsHTML(r) {
		http.Redirect(w, r, "/accounts", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"platform": platform,
		"count":    len(created),
		"items":    created,
	})
}

func (s Server) saveCredentials(ctx context.Context, accountID string, credentials publisher.Credentials) error {
	if credentials.Extra == nil {
		credentials.Extra = map[string]string{}
	}
	ciphertext, nonce, err := s.credentialsCipher().EncryptJSON(credentials)
	if err != nil {
		return err
	}
	return s.Store.SaveAccountCredentials(ctx, accountID, db.EncryptedCredentials{
		Ciphertext: ciphertext,
		Nonce:      nonce,
		KeyVersion: s.credentialsCipher().KeyVersion(),
		UpdatedAt:  time.Now().UTC(),
	})
}

func decodeCredentials(raw map[string]any) (publisher.Credentials, error) {
	if len(raw) == 0 {
		return publisher.Credentials{}, errors.New("credentials are required")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return publisher.Credentials{}, err
	}
	var out publisher.Credentials
	if err := json.Unmarshal(encoded, &out); err != nil {
		return publisher.Credentials{}, err
	}
	if out.Extra == nil {
		out.Extra = map[string]string{}
	}
	return out, nil
}

func parseOAuthPath(path string) (platform domain.Platform, suffix string, ok bool) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(path), "/oauth/")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 {
		return "", "", false
	}
	platform = normalizePlatform(parts[0])
	suffix = strings.TrimSpace(parts[1])
	return platform, suffix, platform != "" && suffix != ""
}

func (s Server) oauthCallbackURL(r *http.Request, platform domain.Platform) string {
	base := strings.TrimRight(strings.TrimSpace(s.PublicBaseURL), "/")
	if base == "" {
		base = strings.TrimRight(requestBaseURL(r), "/")
	}
	if base == "" {
		base = "http://localhost:8080"
	}
	return base + "/oauth/" + url.PathEscape(string(platform)) + "/callback"
}

func normalizePlatform(raw string) domain.Platform {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch domain.Platform(raw) {
	case domain.PlatformX, domain.PlatformLinkedIn, domain.PlatformFacebook, domain.PlatformInstagram:
		return domain.Platform(raw)
	default:
		return ""
	}
}

func mustID(prefix string) string {
	id, err := db.NewID(prefix)
	if err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return id
}

func templateEscape(value string) string {
	return html.EscapeString(strings.TrimSpace(value))
}

func wantsHTML(r *http.Request) bool {
	accept := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept")))
	return strings.Contains(accept, "text/html")
}
