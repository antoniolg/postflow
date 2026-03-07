package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

type oauthPendingSelectionPayload struct {
	OAuthState string                      `json:"oauth_state,omitempty"`
	Platform   domain.Platform             `json:"platform"`
	Accounts   []postflow.ConnectedAccount `json:"accounts"`
}

func shouldPromptOAuthAccountSelection(isHTML bool, connected []postflow.ConnectedAccount) bool {
	return isHTML && len(connected) > 1
}

func (s Server) createOAuthPendingSelection(ctx context.Context, payload oauthPendingSelectionPayload) (string, error) {
	ciphertext, nonce, err := s.credentialsCipher().EncryptJSON(payload)
	if err != nil {
		return "", err
	}
	selection, err := s.Store.CreateOAuthPendingAccountSelection(ctx, db.OAuthPendingAccountSelection{
		Platform:   payload.Platform,
		Ciphertext: ciphertext,
		Nonce:      nonce,
		KeyVersion: s.credentialsCipher().KeyVersion(),
		ExpiresAt:  time.Now().UTC().Add(10 * time.Minute),
	})
	if err != nil {
		return "", err
	}
	return selection.ID, nil
}

func (s Server) loadOAuthPendingSelection(ctx context.Context, id string) (oauthPendingSelectionPayload, error) {
	selection, err := s.Store.GetOAuthPendingAccountSelection(ctx, id)
	if err != nil {
		return oauthPendingSelectionPayload{}, err
	}
	var payload oauthPendingSelectionPayload
	if err := s.credentialsCipher().DecryptJSON(selection.Ciphertext, selection.Nonce, &payload); err != nil {
		return oauthPendingSelectionPayload{}, err
	}
	return payload, nil
}

func (s Server) persistConnectedAccounts(ctx context.Context, connected []postflow.ConnectedAccount) ([]domain.SocialAccount, error) {
	created := make([]domain.SocialAccount, 0, len(connected))
	for _, item := range connected {
		account, err := s.Store.UpsertAccount(ctx, db.UpsertAccountParams{
			Platform:          item.Platform,
			AccountKind:       item.AccountKind,
			DisplayName:       item.DisplayName,
			ExternalAccountID: item.ExternalAccountID,
			AuthMethod:        domain.AuthMethodOAuth,
			Status:            domain.AccountStatusConnected,
		})
		if err != nil {
			return nil, fmt.Errorf("persist account: %w", err)
		}
		if err := s.saveCredentials(ctx, account.ID, item.Credentials); err != nil {
			return nil, fmt.Errorf("save account credentials: %w", err)
		}
		created = append(created, account)
	}
	return created, nil
}

func oauthConnectedAccountsSuccessMessage(count int) string {
	msg := fmt.Sprintf("%d accounts connected", count)
	if count == 1 {
		msg = "1 account connected"
	}
	return msg
}

func oauthSelectionSettingsURL(id string) string {
	values := url.Values{}
	values.Set("view", "settings")
	values.Set("oauth_select", strings.TrimSpace(id))
	return "/?" + values.Encode()
}

func oauthConnectedAccountKey(account postflow.ConnectedAccount) string {
	platform := strings.TrimSpace(string(account.Platform))
	kind := strings.TrimSpace(string(domain.NormalizeAccountKind(account.Platform, account.AccountKind)))
	externalID := strings.TrimSpace(account.ExternalAccountID)
	return strings.Join([]string{platform, kind, externalID}, "|")
}

func defaultSelectOAuthConnectedAccount(account postflow.ConnectedAccount, total int) bool {
	if total <= 1 {
		return true
	}
	return domain.NormalizeAccountKind(account.Platform, account.AccountKind) != domain.AccountKindOrganization
}

func buildOAuthPendingSelectionView(uiLang, id string, payload oauthPendingSelectionPayload) *oauthPendingSelectionView {
	if strings.TrimSpace(id) == "" || len(payload.Accounts) == 0 {
		return nil
	}
	items := make([]oauthPendingSelectionItem, 0, len(payload.Accounts))
	for _, account := range payload.Accounts {
		metaAccount := domain.SocialAccount{
			Platform:    account.Platform,
			AccountKind: domain.NormalizeAccountKind(account.Platform, account.AccountKind),
			AuthMethod:  domain.AuthMethodOAuth,
		}
		items = append(items, oauthPendingSelectionItem{
			Key:             oauthConnectedAccountKey(account),
			DisplayName:     strings.TrimSpace(account.DisplayName),
			AccountMeta:     settingsAccountMeta(uiLang, metaAccount),
			DefaultSelected: defaultSelectOAuthConnectedAccount(account, len(payload.Accounts)),
		})
	}
	return &oauthPendingSelectionView{
		ID:       strings.TrimSpace(id),
		Platform: payload.Platform,
		Count:    len(items),
		Items:    items,
	}
}

func (s Server) handleOAuthSelect(w http.ResponseWriter, r *http.Request) {
	uiLang := preferredUILanguage(r.Header.Get("Accept-Language"))
	returnTo := accountReturnTo(r)
	selectionID := strings.TrimSpace(r.FormValue("selection_id"))
	selectionURL := oauthSelectionSettingsURL(selectionID)
	if selectionID == "" {
		http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", uiMessage(uiLang, "settings.oauth_selection_expired")), http.StatusSeeOther)
		return
	}
	payload, err := s.loadOAuthPendingSelection(r.Context(), selectionID)
	if err != nil {
		msg := uiMessage(uiLang, "settings.oauth_selection_expired")
		if errors.Is(err, db.ErrOAuthPendingAccountSelectionExpired) || errors.Is(err, sql.ErrNoRows) {
			http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", msg), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, withQueryValue(returnTo, "accounts_error", err.Error()), http.StatusSeeOther)
		return
	}

	selectedAccounts := payload.Accounts
	if strings.TrimSpace(r.FormValue("connect_all")) == "" {
		selectedKeys := make(map[string]struct{}, len(r.Form["account_key"]))
		for _, raw := range r.Form["account_key"] {
			key := strings.TrimSpace(raw)
			if key == "" {
				continue
			}
			selectedKeys[key] = struct{}{}
		}
		if len(selectedKeys) == 0 {
			http.Redirect(w, r, withQueryValue(selectionURL, "accounts_error", uiMessage(uiLang, "settings.oauth_selection_required")), http.StatusSeeOther)
			return
		}
		selectedAccounts = make([]postflow.ConnectedAccount, 0, len(selectedKeys))
		for _, account := range payload.Accounts {
			if _, ok := selectedKeys[oauthConnectedAccountKey(account)]; ok {
				selectedAccounts = append(selectedAccounts, account)
			}
		}
		if len(selectedAccounts) == 0 {
			http.Redirect(w, r, withQueryValue(selectionURL, "accounts_error", uiMessage(uiLang, "settings.oauth_selection_required")), http.StatusSeeOther)
			return
		}
	}

	created, err := s.persistConnectedAccounts(r.Context(), selectedAccounts)
	if err != nil {
		http.Redirect(w, r, withQueryValue(selectionURL, "accounts_error", err.Error()), http.StatusSeeOther)
		return
	}
	if err := s.Store.DeleteOAuthPendingAccountSelection(r.Context(), selectionID); err != nil {
		http.Redirect(w, r, withQueryValue(selectionURL, "accounts_error", "failed to finalize account selection"), http.StatusSeeOther)
		return
	}

	msg := oauthConnectedAccountsSuccessMessage(len(created))
	if strings.TrimSpace(payload.OAuthState) != "" {
		rememberOAuthCallbackOutcome(payload.OAuthState, true, msg, "")
	}
	http.Redirect(w, r, withQueryValue(returnTo, "accounts_success", msg), http.StatusSeeOther)
}
