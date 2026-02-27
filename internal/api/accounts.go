package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	if wantsHTML(r) {
		http.Redirect(w, r, settingsViewURL, http.StatusSeeOther)
		return
	}
	accounts, err := s.Store.ListAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count": len(accounts),
		"items": accounts,
	})
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
	isHTML := wantsHTML(r)
	returnTo := settingsViewURL
	if isHTML {
		returnTo = accountReturnTo(r)
	}
	path := strings.TrimPrefix(strings.TrimSpace(r.URL.Path), "/accounts/")
	if path == "" {
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "invalid account action"), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "invalid account action"), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	accountID := strings.TrimSpace(parts[0])
	action := strings.TrimSpace(parts[1])
	if accountID == "" {
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "account id is required"), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("account id is required"))
		return
	}
	switch action {
	case "connect":
		if _, err := s.Store.GetAccountCredentials(r.Context(), accountID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				if isHTML {
					http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "account has no saved credentials"), http.StatusSeeOther)
					return
				}
				writeError(w, http.StatusConflict, errors.New("account has no saved credentials"))
				return
			}
			if isHTML {
				http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "failed to load account credentials"), http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if err := s.Store.UpdateAccountStatus(r.Context(), accountID, domain.AccountStatusConnected, nil); err != nil {
			if errors.Is(err, db.ErrAccountNotFound) {
				if isHTML {
					http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "account not found"), http.StatusSeeOther)
					return
				}
				writeError(w, http.StatusNotFound, errors.New("account not found"))
				return
			}
			if isHTML {
				http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "failed to connect account"), http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_success", "account connected"), http.StatusSeeOther)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": accountID, "status": domain.AccountStatusConnected})
	case "disconnect":
		if err := s.Store.DisconnectAccount(r.Context(), accountID); err != nil {
			if errors.Is(err, db.ErrAccountNotFound) {
				if isHTML {
					http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "account not found"), http.StatusSeeOther)
					return
				}
				writeError(w, http.StatusNotFound, errors.New("account not found"))
				return
			}
			if isHTML {
				http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "failed to disconnect account"), http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_success", "account disconnected"), http.StatusSeeOther)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": accountID, "status": domain.AccountStatusDisconnected})
	case "x-premium":
		premium, err := parseAccountXPremiumValue(r)
		if err != nil {
			if isHTML {
				http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", err.Error()), http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.Store.UpdateAccountXPremium(r.Context(), accountID, premium); err != nil {
			switch {
			case errors.Is(err, db.ErrAccountNotFound):
				if isHTML {
					http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "account not found"), http.StatusSeeOther)
					return
				}
				writeError(w, http.StatusNotFound, errors.New("account not found"))
			case errors.Is(err, db.ErrAccountNotXPlatform):
				if isHTML {
					http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "x premium setting is only available for x accounts"), http.StatusSeeOther)
					return
				}
				writeError(w, http.StatusBadRequest, errors.New("x premium setting is only available for x accounts"))
			default:
				if isHTML {
					http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "failed to update x premium"), http.StatusSeeOther)
					return
				}
				writeError(w, http.StatusInternalServerError, err)
			}
			return
		}
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_success", "x premium updated"), http.StatusSeeOther)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": accountID, "x_premium": premium})
	case "delete":
		if err := s.Store.DeleteAccount(r.Context(), accountID); err != nil {
			switch {
			case errors.Is(err, db.ErrAccountNotFound):
				if isHTML {
					http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "account not found"), http.StatusSeeOther)
					return
				}
				writeError(w, http.StatusNotFound, errors.New("account not found"))
			case errors.Is(err, db.ErrAccountNotDisconnect):
				if isHTML {
					http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "account must be disconnected first"), http.StatusSeeOther)
					return
				}
				writeError(w, http.StatusConflict, errors.New("account must be disconnected first"))
			case errors.Is(err, db.ErrAccountHasPosts):
				if isHTML {
					http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "account has pending posts"), http.StatusSeeOther)
					return
				}
				writeError(w, http.StatusConflict, errors.New("account has pending posts"))
			default:
				if isHTML {
					http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "failed to delete account"), http.StatusSeeOther)
					return
				}
				writeError(w, http.StatusInternalServerError, err)
			}
			return
		}
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_success", "account deleted"), http.StatusSeeOther)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": accountID})
	default:
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "unsupported account action"), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusNotFound, errors.New("not found"))
	}
}

func parseAccountXPremiumValue(r *http.Request) (bool, error) {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("content-type")))
	if strings.Contains(contentType, "application/json") {
		var body struct {
			XPremium *bool `json:"x_premium"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return false, fmt.Errorf("invalid json body: %w", err)
		}
		if body.XPremium == nil {
			return false, errors.New("x_premium is required")
		}
		return *body.XPremium, nil
	}
	if err := r.ParseForm(); err != nil {
		return false, fmt.Errorf("invalid form: %w", err)
	}
	return truthyValues(r.Form["x_premium"]), nil
}

func truthyValues(values []string) bool {
	for _, raw := range values {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
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
			writeError(w, http.StatusConflict, errors.New("account has pending posts"))
		default:
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": accountID})
}

func (s Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	isHTML := wantsHTML(r)
	returnTo := settingsViewURL
	if isHTML {
		returnTo = accountReturnTo(r)
	}
	platform, suffix, ok := parseOAuthPath(strings.TrimSpace(r.URL.Path))
	if !ok || suffix != "start" {
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "invalid oauth route"), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	provider, ok := s.providerRegistry().GetOAuth(platform)
	if !ok {
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "oauth is not available for platform"), http.StatusSeeOther)
			return
		}
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
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "failed to initialize oauth state"), http.StatusSeeOther)
			return
		}
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
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", err.Error()), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if isHTML {
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
	isHTML := wantsHTML(r)
	returnTo := settingsViewURL
	platform, suffix, ok := parseOAuthPath(strings.TrimSpace(r.URL.Path))
	if !ok || suffix != "callback" {
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "invalid oauth callback"), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	provider, ok := s.providerRegistry().GetOAuth(platform)
	if !ok {
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "oauth is not available for platform"), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("oauth is not available for platform"))
		return
	}
	stateRaw := strings.TrimSpace(r.URL.Query().Get("state"))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		if errDesc := strings.TrimSpace(r.URL.Query().Get("error_description")); errDesc != "" {
			if isHTML {
				http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", errDesc), http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusBadRequest, errors.New(errDesc))
			return
		}
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "missing authorization code"), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("missing authorization code"))
		return
	}
	recorded, err := s.Store.ConsumeOAuthState(r.Context(), stateRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if isHTML {
				http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "invalid oauth state"), http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusBadRequest, errors.New("invalid oauth state"))
			return
		}
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "failed to consume oauth state"), http.StatusSeeOther)
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
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", err.Error()), http.StatusSeeOther)
			return
		}
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
			if isHTML {
				http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "failed to persist account"), http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if err := s.saveCredentials(r.Context(), account.ID, item.Credentials); err != nil {
			if isHTML {
				http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "failed to save account credentials"), http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		created = append(created, account)
	}
	if isHTML {
		msg := fmt.Sprintf("%d accounts connected", len(created))
		if len(created) == 1 {
			msg = "1 account connected"
		}
		http.Redirect(w, r, withQueryValue(returnTo, "accounts_success", msg), http.StatusSeeOther)
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

const settingsViewURL = "/?view=settings"

func accountReturnTo(r *http.Request) string {
	returnTo := sanitizeReturnTo(strings.TrimSpace(r.FormValue("return_to")))
	if returnTo == "" {
		return settingsViewURL
	}
	return returnTo
}

func wantsHTML(r *http.Request) bool {
	accept := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept")))
	return strings.Contains(accept, "text/html")
}
