package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
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
	codeVerifier := newOAuthCodeVerifier()
	redirectURL := s.oauthCallbackURL(r, platform)
	out, err := provider.StartOAuth(r.Context(), postflow.OAuthStartInput{
		State:        state,
		CodeVerifier: codeVerifier,
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
	recordedCodeVerifier := strings.TrimSpace(out.CodeVerifier)
	if recordedCodeVerifier == "" {
		recordedCodeVerifier = codeVerifier
	}
	slog.Info("oauth start requested",
		"platform", platform,
		"state", oauthStateLabel(state),
		"return_to", returnTo,
		"is_html", isHTML,
	)
	recorded, err := s.Store.CreateOAuthState(r.Context(), domain.OauthState{
		Platform:     platform,
		State:        state,
		CodeVerifier: recordedCodeVerifier,
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
	callbackCtx, cancelCallback := oauthCallbackContext(r, isHTML)
	defer cancelCallback()
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
		code = strings.TrimSpace(r.URL.Query().Get("oauth_verifier"))
	}
	slog.Info("oauth callback received",
		"platform", platform,
		"state", oauthStateLabel(stateRaw),
		"code_present", code != "",
		"is_html", isHTML,
	)
	if code == "" {
		if denied := strings.TrimSpace(r.URL.Query().Get("denied")); denied != "" {
			if isHTML {
				http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "access denied"), http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusBadRequest, errors.New("access denied"))
			return
		}
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
	recorded, err := s.Store.ConsumeOAuthState(callbackCtx, stateRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if outcome, ok := recentOAuthCallbackOutcome(stateRaw); ok {
				slog.Warn("oauth callback replay detected",
					"platform", platform,
					"state", oauthStateLabel(stateRaw),
					"success", outcome.Success,
				)
				writeOAuthReplayOutcome(w, r, platform, returnTo, isHTML, outcome)
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
	connected, err := provider.HandleOAuthCallback(callbackCtx, postflow.OAuthCallbackInput{
		Code:         code,
		State:        recorded.State,
		CodeVerifier: recorded.CodeVerifier,
		RedirectURL:  s.oauthCallbackURL(r, platform),
	})
	if err != nil {
		rememberOAuthCallbackOutcome(recorded.State, false, err.Error(), "")
		slog.Error("oauth callback provider failed",
			"platform", platform,
			"state", oauthStateLabel(recorded.State),
			"error", err.Error(),
		)
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", err.Error()), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if shouldPromptOAuthAccountSelection(isHTML, connected) {
		selectionID, err := s.createOAuthPendingSelection(callbackCtx, oauthPendingSelectionPayload{
			OAuthState: recorded.State,
			Platform:   platform,
			Accounts:   connected,
		})
		if err != nil {
			rememberOAuthCallbackOutcome(recorded.State, false, "failed to prepare account selection", "")
			if isHTML {
				http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", "failed to prepare account selection"), http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		rememberOAuthCallbackOutcome(recorded.State, true, "", selectionID)
		http.Redirect(w, r, oauthSelectionSettingsURL(selectionID), http.StatusSeeOther)
		return
	}

	created, err := s.persistConnectedAccounts(callbackCtx, connected)
	if err != nil {
		rememberOAuthCallbackOutcome(recorded.State, false, err.Error(), "")
		slog.Error("oauth callback persist accounts failed",
			"platform", platform,
			"state", oauthStateLabel(recorded.State),
			"error", err.Error(),
		)
		if isHTML {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", err.Error()), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	msg := oauthConnectedAccountsSuccessMessage(len(created))
	rememberOAuthCallbackOutcome(recorded.State, true, msg, "")
	if isHTML {
		http.Redirect(w, r, withQueryValue(returnTo, "accounts_success", msg), http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"platform": platform,
		"count":    len(created),
		"items":    created,
	})
}

func (s Server) saveCredentials(ctx context.Context, accountID string, credentials postflow.Credentials) error {
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

func decodeCredentials(raw map[string]any) (postflow.Credentials, error) {
	if len(raw) == 0 {
		return postflow.Credentials{}, errors.New("credentials are required")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return postflow.Credentials{}, err
	}
	var out postflow.Credentials
	if err := json.Unmarshal(encoded, &out); err != nil {
		return postflow.Credentials{}, err
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

func normalizeAccountKind(platform domain.Platform, raw string) domain.AccountKind {
	return domain.NormalizeAccountKind(platform, domain.AccountKind(strings.ToLower(strings.TrimSpace(raw))))
}

func mustID(prefix string) string {
	id, err := db.NewID(prefix)
	if err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return id
}

func newOAuthCodeVerifier() string {
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return mustID("verifier") + mustID("pkce")
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

const settingsViewURL = "/?view=settings"

type oauthCallbackOutcome struct {
	Success     bool
	Message     string
	SelectionID string
	ExpiresAt   time.Time
}

var recentOAuthCallbackOutcomes sync.Map

func rememberOAuthCallbackOutcome(state string, success bool, message, selectionID string) {
	state = strings.TrimSpace(state)
	if state == "" {
		return
	}
	now := time.Now().UTC()
	recentOAuthCallbackOutcomes.Store(state, oauthCallbackOutcome{
		Success:     success,
		Message:     strings.TrimSpace(message),
		SelectionID: strings.TrimSpace(selectionID),
		ExpiresAt:   now.Add(2 * time.Minute),
	})
	recentOAuthCallbackOutcomes.Range(func(key, value any) bool {
		outcome, ok := value.(oauthCallbackOutcome)
		if !ok || !outcome.ExpiresAt.After(now) {
			recentOAuthCallbackOutcomes.Delete(key)
		}
		return true
	})
}

func recentOAuthCallbackOutcome(state string) (oauthCallbackOutcome, bool) {
	state = strings.TrimSpace(state)
	if state == "" {
		return oauthCallbackOutcome{}, false
	}
	raw, ok := recentOAuthCallbackOutcomes.Load(state)
	if !ok {
		return oauthCallbackOutcome{}, false
	}
	outcome, ok := raw.(oauthCallbackOutcome)
	if !ok {
		recentOAuthCallbackOutcomes.Delete(state)
		return oauthCallbackOutcome{}, false
	}
	if !outcome.ExpiresAt.After(time.Now().UTC()) {
		recentOAuthCallbackOutcomes.Delete(state)
		return oauthCallbackOutcome{}, false
	}
	return outcome, true
}

func writeOAuthReplayOutcome(w http.ResponseWriter, r *http.Request, platform domain.Platform, returnTo string, isHTML bool, outcome oauthCallbackOutcome) {
	if isHTML && strings.TrimSpace(outcome.SelectionID) != "" {
		http.Redirect(w, r, oauthSelectionSettingsURL(outcome.SelectionID), http.StatusSeeOther)
		return
	}
	message := strings.TrimSpace(outcome.Message)
	if message == "" {
		if outcome.Success {
			message = "oauth callback already processed"
		} else {
			message = "oauth callback already failed"
		}
	}
	if isHTML {
		param := "accounts_success"
		if !outcome.Success {
			param = "accounts_error"
		}
		http.Redirect(w, r, withQueryValue(returnTo, param, message), http.StatusSeeOther)
		return
	}
	if outcome.Success {
		writeJSON(w, http.StatusOK, map[string]any{
			"platform": platform,
			"status":   "already_processed",
		})
		return
	}
	writeError(w, http.StatusBadRequest, errors.New(message))
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

func oauthCallbackContext(r *http.Request, isHTML bool) (context.Context, context.CancelFunc) {
	if !isHTML {
		return r.Context(), func() {}
	}
	return context.WithTimeout(context.WithoutCancel(r.Context()), 45*time.Second)
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
