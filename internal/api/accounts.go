package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
)

type createStaticAccountRequest struct {
	Platform          string         `json:"platform"`
	AccountKind       string         `json:"account_kind,omitempty"`
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
	if platform == domain.PlatformX {
		writeError(w, http.StatusBadRequest, errors.New("static x accounts are not supported; connect via oauth"))
		return
	}
	accountKind := normalizeAccountKind(platform, req.AccountKind)
	if accountKind == "" {
		writeError(w, http.StatusBadRequest, errors.New("account_kind is invalid for platform"))
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
	account, err := s.Store.UpsertAccount(r.Context(), db.UpsertAccountParams{
		Platform:          platform,
		AccountKind:       accountKind,
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
