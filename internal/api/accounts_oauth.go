package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
)

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
	slog.Info("oauth start requested",
		"platform", platform,
		"state", oauthStateLabel(state),
		"return_to", returnTo,
		"is_html", isHTML,
	)
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
	slog.Info("oauth callback received",
		"platform", platform,
		"state", oauthStateLabel(stateRaw),
		"code_present", code != "",
		"is_html", isHTML,
	)
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
			if wasRecentlyCompletedOAuthState(stateRaw) {
				slog.Warn("oauth callback replay detected",
					"platform", platform,
					"state", oauthStateLabel(stateRaw),
				)
				if isHTML {
					http.Redirect(w, r, withQueryValue(returnTo, "accounts_success", "oauth callback already processed"), http.StatusSeeOther)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"platform": platform,
					"status":   "already_processed",
				})
				return
			}
			slog.Warn("oauth callback state not found",
				"platform", platform,
				"state", oauthStateLabel(stateRaw),
			)
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
	rememberCompletedOAuthState(recorded.State)
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

var recentCompletedOAuthStates sync.Map

func rememberCompletedOAuthState(state string) {
	state = strings.TrimSpace(state)
	if state == "" {
		return
	}
	now := time.Now().UTC()
	expireAt := now.Add(2 * time.Minute)
	recentCompletedOAuthStates.Store(state, expireAt)
	recentCompletedOAuthStates.Range(func(key, value any) bool {
		storedExpireAt, ok := value.(time.Time)
		if !ok || !storedExpireAt.After(now) {
			recentCompletedOAuthStates.Delete(key)
		}
		return true
	})
}

func wasRecentlyCompletedOAuthState(state string) bool {
	state = strings.TrimSpace(state)
	if state == "" {
		return false
	}
	raw, ok := recentCompletedOAuthStates.Load(state)
	if !ok {
		return false
	}
	expireAt, ok := raw.(time.Time)
	if !ok {
		recentCompletedOAuthStates.Delete(state)
		return false
	}
	if !expireAt.After(time.Now().UTC()) {
		recentCompletedOAuthStates.Delete(state)
		return false
	}
	return true
}

func oauthStateLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "empty"
	}
	if len(raw) <= 12 {
		return raw
	}
	return raw[:6] + "..." + raw[len(raw)-4:]
}

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
