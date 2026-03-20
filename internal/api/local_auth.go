package api

import (
	"database/sql"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/db"
)

const (
	localSessionCookieName = "postflow_session"
	localSessionTTL        = 30 * 24 * time.Hour
)

type loginPageData struct {
	Lang            string
	Error           string
	Success         string
	Email           string
	ReturnTo        string
	OwnerConfigured bool
}

func (s Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	uiLang := preferredUILanguage(r.Header.Get("Accept-Language"))
	returnTo := sanitizeReturnTo(strings.TrimSpace(r.URL.Query().Get("return_to")))
	if returnTo == "" {
		returnTo = "/"
	}
	if s.LocalAuthEnabled {
		if _, _, err := s.currentOwnerFromSession(r); err == nil {
			http.Redirect(w, r, returnTo, http.StatusSeeOther)
			return
		}
	}
	s.renderLoginPage(w, r, loginPageData{
		Lang:            uiLang,
		Error:           strings.TrimSpace(r.URL.Query().Get("error")),
		Success:         strings.TrimSpace(r.URL.Query().Get("success")),
		Email:           strings.TrimSpace(r.URL.Query().Get("email")),
		ReturnTo:        returnTo,
		OwnerConfigured: s.LocalAuthEnabled,
	})
}

func (s Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	returnTo := sanitizeReturnTo(strings.TrimSpace(r.FormValue("return_to")))
	if returnTo == "" {
		returnTo = "/"
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	if !s.LocalAuthEnabled {
		http.Redirect(w, r, withQueryValue("/login?return_to="+url.QueryEscape(returnTo), "error", "owner auth is not configured"), http.StatusSeeOther)
		return
	}
	owner, err := s.Store.AuthenticateLocalOwner(r.Context(), email, password)
	if err != nil {
		if errors.Is(err, db.ErrLocalOwnerAuthFailed) {
			target := "/login?return_to=" + url.QueryEscape(returnTo)
			target = withQueryValue(target, "email", email)
			target = withQueryValue(target, "error", "invalid email or password")
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	sessionToken, _, err := s.Store.CreateWebSession(r.Context(), owner.ID, localSessionTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.SetCookie(w, s.sessionCookie(sessionToken, r))
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}

func (s Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(localSessionCookieName); err == nil {
		_ = s.Store.DeleteWebSessionByToken(r.Context(), cookie.Value)
	}
	http.SetCookie(w, expiredSessionCookie(r))
	http.Redirect(w, r, "/login?success=signed+out", http.StatusSeeOther)
}

func (s Server) renderLoginPage(w http.ResponseWriter, r *http.Request, data loginPageData) {
	if strings.TrimSpace(data.Lang) == "" {
		data.Lang = preferredUILanguage(r.Header.Get("Accept-Language"))
	}
	t, err := template.New("login").Funcs(template.FuncMap{
		"t": func(key string, args ...any) string {
			return uiMessage(data.Lang, key, args...)
		},
	}).Parse(loginHTMLTemplate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.Execute(w, data)
}

func (s Server) currentOwnerFromSession(r *http.Request) (db.LocalOwner, db.WebSession, error) {
	if s.Store == nil {
		return db.LocalOwner{}, db.WebSession{}, sql.ErrNoRows
	}
	cookie, err := r.Cookie(localSessionCookieName)
	if err != nil {
		return db.LocalOwner{}, db.WebSession{}, err
	}
	session, err := s.Store.GetWebSessionByToken(r.Context(), cookie.Value)
	if err != nil {
		return db.LocalOwner{}, db.WebSession{}, err
	}
	owner, err := s.Store.GetLocalOwnerByID(r.Context(), session.OwnerID)
	if err != nil {
		return db.LocalOwner{}, db.WebSession{}, err
	}
	return owner, session, nil
}

func (s Server) sessionCookie(rawToken string, r *http.Request) *http.Cookie {
	return &http.Cookie{
		Name:     localSessionCookieName,
		Value:    strings.TrimSpace(rawToken),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestUsesHTTPS(r),
		MaxAge:   int(localSessionTTL.Seconds()),
		Expires:  time.Now().UTC().Add(localSessionTTL),
	}
}

func expiredSessionCookie(r *http.Request) *http.Cookie {
	return &http.Cookie{
		Name:     localSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestUsesHTTPS(r),
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	}
}

func requestCanUseLocalSession(r *http.Request) bool {
	if r == nil {
		return false
	}
	if wantsHTML(r) {
		return true
	}
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	return strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "multipart/form-data")
}

func requestUsesHTTPS(r *http.Request) bool {
	if r == nil {
		return false
	}
	if strings.EqualFold(firstCSVHeaderValue(r.Header.Get("X-Forwarded-Proto")), "https") {
		return true
	}
	return r.TLS != nil
}
