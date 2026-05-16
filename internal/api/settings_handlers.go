package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	notificationsapp "github.com/antoniolg/postflow/internal/application/notifications"
)

func (s Server) handleSetTimezone(w http.ResponseWriter, r *http.Request) {
	contentType := strings.ToLower(r.Header.Get("content-type"))
	fromForm := strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "multipart/form-data")

	timezone := ""
	returnTo := "/?view=settings"
	if fromForm {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/?view=settings&tz_error=invalid+form", http.StatusSeeOther)
			return
		}
		timezone = strings.TrimSpace(r.FormValue("timezone"))
		returnTo = sanitizeReturnTo(strings.TrimSpace(r.FormValue("return_to")))
		if returnTo == "" {
			returnTo = "/?view=settings"
		}
	} else {
		var body struct {
			Timezone string `json:"timezone"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json body: %w", err))
			return
		}
		timezone = strings.TrimSpace(body.Timezone)
	}

	if timezone == "" {
		if fromForm {
			http.Redirect(w, r, withQueryValue(returnTo, "tz_error", "timezone is required"), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("timezone is required"))
		return
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		if fromForm {
			http.Redirect(w, r, withQueryValue(returnTo, "tz_error", "invalid timezone"), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid timezone: %w", err))
		return
	}
	if err := s.Store.SetUITimezone(r.Context(), timezone); err != nil {
		if fromForm {
			http.Redirect(w, r, withQueryValue(returnTo, "tz_error", err.Error()), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if fromForm {
		http.Redirect(w, r, withQueryValue(returnTo, "tz_success", "timezone saved"), http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"timezone": timezone})
}

func (s Server) handleSetSMTPNotifications(w http.ResponseWriter, r *http.Request) {
	update, returnTo, fromForm, err := parseSMTPConfigUpdate(r)
	if err != nil {
		if fromForm {
			http.Redirect(w, r, "/?view=settings&smtp_error=invalid+form", http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	service := notificationsapp.Service{Store: s.Store, Cipher: s.credentialsCipher(), Sender: s.SMTPSender}
	view, err := service.SaveSMTPConfig(r.Context(), update)
	if err != nil {
		if fromForm {
			http.Redirect(w, r, withQueryValue(returnTo, "smtp_error", err.Error()), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if fromForm {
		http.Redirect(w, r, withQueryValue(returnTo, "smtp_success", "smtp saved"), http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s Server) handleTestSMTPNotifications(w http.ResponseWriter, r *http.Request) {
	update, returnTo, fromForm, err := parseSMTPConfigUpdate(r)
	if err != nil {
		if fromForm {
			http.Redirect(w, r, "/?view=settings&smtp_error=invalid+form", http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	service := notificationsapp.Service{Store: s.Store, Cipher: s.credentialsCipher(), Sender: s.SMTPSender}
	view, err := service.SaveSMTPConfig(r.Context(), update)
	if err != nil {
		if fromForm {
			http.Redirect(w, r, withQueryValue(returnTo, "smtp_error", err.Error()), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	view, err = service.SendSMTPTest(r.Context())
	if err != nil {
		if fromForm {
			http.Redirect(w, r, withQueryValue(returnTo, "smtp_error", err.Error()), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if fromForm {
		http.Redirect(w, r, withQueryValue(returnTo, "smtp_success", "test email sent"), http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func parseSMTPConfigUpdate(r *http.Request) (notificationsapp.SMTPConfigUpdate, string, bool, error) {
	contentType := strings.ToLower(r.Header.Get("content-type"))
	fromForm := strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "multipart/form-data")
	returnTo := "/?view=settings"
	if fromForm {
		if err := r.ParseForm(); err != nil {
			return notificationsapp.SMTPConfigUpdate{}, returnTo, true, fmt.Errorf("invalid form: %w", err)
		}
		returnTo = sanitizeReturnTo(strings.TrimSpace(r.FormValue("return_to")))
		if returnTo == "" {
			returnTo = "/?view=settings"
		}
		port, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("port")))
		return notificationsapp.SMTPConfigUpdate{
			Enabled:       r.FormValue("enabled") == "1",
			Host:          r.FormValue("host"),
			Port:          port,
			Username:      r.FormValue("username"),
			Password:      r.FormValue("password"),
			KeepPassword:  true,
			From:          r.FormValue("from"),
			To:            r.FormValue("to"),
			SubjectPrefix: r.FormValue("subject_prefix"),
			UseTLS:        r.FormValue("use_tls") == "1",
			StartTLS:      r.FormValue("start_tls") == "1",
		}, returnTo, true, nil
	}
	var body struct {
		Enabled       bool   `json:"enabled"`
		Host          string `json:"host"`
		Port          int    `json:"port"`
		Username      string `json:"username"`
		Password      string `json:"password"`
		KeepPassword  bool   `json:"keep_password"`
		From          string `json:"from"`
		To            string `json:"to"`
		SubjectPrefix string `json:"subject_prefix"`
		UseTLS        bool   `json:"use_tls"`
		StartTLS      bool   `json:"start_tls"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return notificationsapp.SMTPConfigUpdate{}, returnTo, false, fmt.Errorf("invalid json body: %w", err)
	}
	return notificationsapp.SMTPConfigUpdate{
		Enabled:       body.Enabled,
		Host:          body.Host,
		Port:          body.Port,
		Username:      body.Username,
		Password:      body.Password,
		KeepPassword:  body.KeepPassword,
		From:          body.From,
		To:            body.To,
		SubjectPrefix: body.SubjectPrefix,
		UseTLS:        body.UseTLS,
		StartTLS:      body.StartTLS,
	}, returnTo, false, nil
}
