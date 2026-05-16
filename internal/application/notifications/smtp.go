package notifications

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/application/ports"
	"github.com/antoniolg/postflow/internal/secure"
)

const SettingSMTPNotifications = "notifications.smtp"

type SettingsStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

type SMTPConfig struct {
	Enabled       bool   `json:"enabled"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	From          string `json:"from"`
	To            string `json:"to"`
	SubjectPrefix string `json:"subject_prefix,omitempty"`
	UseTLS        bool   `json:"use_tls"`
	StartTLS      bool   `json:"start_tls"`
}

type SMTPConfigView struct {
	Enabled             bool     `json:"enabled"`
	Host                string   `json:"host"`
	Port                int      `json:"port"`
	Username            string   `json:"username,omitempty"`
	PasswordConfigured  bool     `json:"password_configured"`
	From                string   `json:"from"`
	To                  string   `json:"to"`
	SubjectPrefix       string   `json:"subject_prefix,omitempty"`
	UseTLS              bool     `json:"use_tls"`
	StartTLS            bool     `json:"start_tls"`
	Configured          bool     `json:"configured"`
	Ready               bool     `json:"ready"`
	MissingRequirements []string `json:"missing_requirements,omitempty"`
}

type SMTPConfigUpdate struct {
	Enabled       bool
	Host          string
	Port          int
	Username      string
	Password      string
	KeepPassword  bool
	From          string
	To            string
	SubjectPrefix string
	UseTLS        bool
	StartTLS      bool
}

type storedSMTPConfig struct {
	Enabled            bool   `json:"enabled"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	Username           string `json:"username,omitempty"`
	PasswordCiphertext string `json:"password_ciphertext,omitempty"`
	PasswordNonce      string `json:"password_nonce,omitempty"`
	PasswordKeyVersion int    `json:"password_key_version,omitempty"`
	From               string `json:"from"`
	To                 string `json:"to"`
	SubjectPrefix      string `json:"subject_prefix,omitempty"`
	UseTLS             bool   `json:"use_tls"`
	StartTLS           bool   `json:"start_tls"`
}

type Service struct {
	Store  SettingsStore
	Cipher *secure.Cipher
	Sender SMTPSender
}

type SMTPSender interface {
	Send(ctx context.Context, cfg SMTPConfig, message EmailMessage) error
}

type EmailMessage struct {
	Subject string
	Text    string
}

func (s Service) GetSMTPConfig(ctx context.Context) (SMTPConfig, bool, error) {
	stored, ok, err := s.loadStoredSMTPConfig(ctx)
	if err != nil || !ok {
		return SMTPConfig{}, ok, err
	}
	password, err := s.decryptPassword(stored)
	if err != nil {
		return SMTPConfig{}, true, err
	}
	return smtpConfigFromStored(stored, password), true, nil
}

func (s Service) GetSMTPConfigView(ctx context.Context) (SMTPConfigView, error) {
	cfg, ok, err := s.GetSMTPConfig(ctx)
	if err != nil {
		return SMTPConfigView{}, err
	}
	view := SMTPConfigView{
		Enabled:            cfg.Enabled,
		Host:               cfg.Host,
		Port:               cfg.Port,
		Username:           cfg.Username,
		PasswordConfigured: cfg.Password != "",
		From:               cfg.From,
		To:                 cfg.To,
		SubjectPrefix:      cfg.SubjectPrefix,
		UseTLS:             cfg.UseTLS,
		StartTLS:           cfg.StartTLS,
		Configured:         ok,
	}
	view.MissingRequirements = missingSMTPRequirements(cfg)
	view.Ready = cfg.Enabled && len(view.MissingRequirements) == 0
	return view, nil
}

func (s Service) SaveSMTPConfig(ctx context.Context, update SMTPConfigUpdate) (SMTPConfigView, error) {
	if s.Store == nil {
		return SMTPConfigView{}, errors.New("settings store is required")
	}
	port := update.Port
	if port == 0 {
		port = 587
	}
	cfg := SMTPConfig{
		Enabled:       update.Enabled,
		Host:          strings.TrimSpace(update.Host),
		Port:          port,
		Username:      strings.TrimSpace(update.Username),
		Password:      strings.TrimSpace(update.Password),
		From:          strings.TrimSpace(update.From),
		To:            strings.TrimSpace(update.To),
		SubjectPrefix: strings.TrimSpace(update.SubjectPrefix),
		UseTLS:        update.UseTLS,
		StartTLS:      update.StartTLS,
	}
	if !cfg.UseTLS && !cfg.StartTLS && cfg.Port == 587 {
		cfg.StartTLS = true
	}
	if cfg.SubjectPrefix == "" {
		cfg.SubjectPrefix = "PostFlow publish failed"
	}
	if update.KeepPassword && cfg.Password == "" {
		existing, ok, err := s.GetSMTPConfig(ctx)
		if err != nil {
			return SMTPConfigView{}, err
		}
		if ok {
			cfg.Password = existing.Password
		}
	}
	if cfg.Enabled {
		if missing := missingSMTPRequirements(cfg); len(missing) > 0 {
			return SMTPConfigView{}, fmt.Errorf("smtp config is missing: %s", strings.Join(missing, ", "))
		}
	}
	stored, err := s.storedFromSMTPConfig(cfg)
	if err != nil {
		return SMTPConfigView{}, err
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return SMTPConfigView{}, err
	}
	if err := s.Store.SetSetting(ctx, SettingSMTPNotifications, string(raw)); err != nil {
		return SMTPConfigView{}, err
	}
	return s.GetSMTPConfigView(ctx)
}

func (s Service) NotifyPublishFailure(ctx context.Context, notification ports.PublishFailureNotification) error {
	cfg, ok, err := s.GetSMTPConfig(ctx)
	if err != nil {
		return err
	}
	if !ok || !cfg.Enabled {
		return nil
	}
	if missing := missingSMTPRequirements(cfg); len(missing) > 0 {
		return fmt.Errorf("smtp config is missing: %s", strings.Join(missing, ", "))
	}
	sender := s.Sender
	if sender == nil {
		sender = NetSMTPSender{}
	}
	return sender.Send(ctx, cfg, buildFailureEmail(cfg, notification))
}

func (s Service) SendSMTPTest(ctx context.Context) (SMTPConfigView, error) {
	cfg, ok, err := s.GetSMTPConfig(ctx)
	if err != nil {
		return SMTPConfigView{}, err
	}
	if !ok {
		return SMTPConfigView{}, errors.New("smtp config is not configured")
	}
	view, err := s.GetSMTPConfigView(ctx)
	if err != nil {
		return SMTPConfigView{}, err
	}
	if missing := missingSMTPRequirements(cfg); len(missing) > 0 {
		return view, fmt.Errorf("smtp config is missing: %s", strings.Join(missing, ", "))
	}
	sender := s.Sender
	if sender == nil {
		sender = NetSMTPSender{}
	}
	if err := sender.Send(ctx, cfg, buildTestEmail(cfg)); err != nil {
		return view, err
	}
	return view, nil
}

func (s Service) loadStoredSMTPConfig(ctx context.Context) (storedSMTPConfig, bool, error) {
	if s.Store == nil {
		return storedSMTPConfig{}, false, errors.New("settings store is required")
	}
	raw, err := s.Store.GetSetting(ctx, SettingSMTPNotifications)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storedSMTPConfig{}, false, nil
		}
		return storedSMTPConfig{}, false, err
	}
	var stored storedSMTPConfig
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return storedSMTPConfig{}, true, fmt.Errorf("decode smtp config: %w", err)
	}
	return stored, true, nil
}

func (s Service) storedFromSMTPConfig(cfg SMTPConfig) (storedSMTPConfig, error) {
	stored := storedSMTPConfig{
		Enabled:       cfg.Enabled,
		Host:          cfg.Host,
		Port:          cfg.Port,
		Username:      cfg.Username,
		From:          cfg.From,
		To:            cfg.To,
		SubjectPrefix: cfg.SubjectPrefix,
		UseTLS:        cfg.UseTLS,
		StartTLS:      cfg.StartTLS,
	}
	if cfg.Password == "" {
		return stored, nil
	}
	if s.Cipher == nil {
		return storedSMTPConfig{}, errors.New("cipher is required to store smtp password")
	}
	ciphertext, nonce, err := s.Cipher.EncryptJSON(cfg.Password)
	if err != nil {
		return storedSMTPConfig{}, err
	}
	stored.PasswordCiphertext = base64.StdEncoding.EncodeToString(ciphertext)
	stored.PasswordNonce = base64.StdEncoding.EncodeToString(nonce)
	stored.PasswordKeyVersion = s.Cipher.KeyVersion()
	return stored, nil
}

func (s Service) decryptPassword(stored storedSMTPConfig) (string, error) {
	if stored.PasswordCiphertext == "" && stored.PasswordNonce == "" {
		return "", nil
	}
	if s.Cipher == nil {
		return "", errors.New("cipher is required to read smtp password")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(stored.PasswordCiphertext)
	if err != nil {
		return "", fmt.Errorf("decode smtp password ciphertext: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(stored.PasswordNonce)
	if err != nil {
		return "", fmt.Errorf("decode smtp password nonce: %w", err)
	}
	var password string
	if err := s.Cipher.DecryptJSON(ciphertext, nonce, &password); err != nil {
		return "", fmt.Errorf("decrypt smtp password: %w", err)
	}
	return password, nil
}

func smtpConfigFromStored(stored storedSMTPConfig, password string) SMTPConfig {
	return SMTPConfig{
		Enabled:       stored.Enabled,
		Host:          strings.TrimSpace(stored.Host),
		Port:          stored.Port,
		Username:      strings.TrimSpace(stored.Username),
		Password:      password,
		From:          strings.TrimSpace(stored.From),
		To:            strings.TrimSpace(stored.To),
		SubjectPrefix: strings.TrimSpace(stored.SubjectPrefix),
		UseTLS:        stored.UseTLS,
		StartTLS:      stored.StartTLS,
	}
}

func missingSMTPRequirements(cfg SMTPConfig) []string {
	var missing []string
	if strings.TrimSpace(cfg.Host) == "" {
		missing = append(missing, "host")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		missing = append(missing, "port")
	}
	if strings.TrimSpace(cfg.From) == "" {
		missing = append(missing, "from")
	} else if _, err := mail.ParseAddress(cfg.From); err != nil {
		missing = append(missing, "valid from")
	}
	if strings.TrimSpace(cfg.To) == "" {
		missing = append(missing, "to")
	} else if _, err := mail.ParseAddress(cfg.To); err != nil {
		missing = append(missing, "valid to")
	}
	if strings.TrimSpace(cfg.Username) != "" && strings.TrimSpace(cfg.Password) == "" {
		missing = append(missing, "password")
	}
	return missing
}

func buildFailureEmail(cfg SMTPConfig, notification ports.PublishFailureNotification) EmailMessage {
	post := notification.Post
	account := notification.Account
	errText := "unknown error"
	if notification.Error != nil {
		errText = strings.TrimSpace(notification.Error.Error())
	}
	subjectPrefix := strings.TrimSpace(cfg.SubjectPrefix)
	if subjectPrefix == "" {
		subjectPrefix = "PostFlow publish failed"
	}
	subject := fmt.Sprintf("%s: %s", subjectPrefix, strings.TrimSpace(post.ID))
	var body bytes.Buffer
	fmt.Fprintf(&body, "PostFlow failed to publish a post.\n\n")
	fmt.Fprintf(&body, "Post ID: %s\n", post.ID)
	fmt.Fprintf(&body, "Platform: %s\n", post.Platform)
	if account.ID != "" {
		fmt.Fprintf(&body, "Account: %s (%s)\n", account.DisplayName, account.ID)
	}
	fmt.Fprintf(&body, "Status: %s\n", post.Status)
	fmt.Fprintf(&body, "Attempts: %d/%d\n", post.Attempts, post.MaxAttempts)
	if !post.ScheduledAt.IsZero() {
		fmt.Fprintf(&body, "Scheduled at: %s\n", post.ScheduledAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&body, "\nError:\n%s\n", errText)
	if text := strings.TrimSpace(post.Text); text != "" {
		if len(text) > 1000 {
			text = text[:1000] + "..."
		}
		fmt.Fprintf(&body, "\nPost text:\n%s\n", text)
	}
	return EmailMessage{Subject: subject, Text: body.String()}
}

func buildTestEmail(cfg SMTPConfig) EmailMessage {
	subjectPrefix := strings.TrimSpace(cfg.SubjectPrefix)
	if subjectPrefix == "" {
		subjectPrefix = "PostFlow publish failed"
	}
	return EmailMessage{
		Subject: subjectPrefix + ": SMTP test",
		Text: strings.Join([]string{
			"PostFlow SMTP test email.",
			"",
			"If you received this email, the SMTP configuration can authenticate and deliver messages.",
			"Future publish failures will use this same SMTP configuration.",
		}, "\n"),
	}
}

type NetSMTPSender struct{}

func (NetSMTPSender) Send(ctx context.Context, cfg SMTPConfig, message EmailMessage) error {
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return err
	}
	to, err := mail.ParseAddress(cfg.To)
	if err != nil {
		return err
	}
	host := strings.TrimSpace(cfg.Host)
	port := cfg.Port
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	raw := buildRFC822Message(from.String(), to.String(), message.Subject, message.Text)
	dialer := net.Dialer{Timeout: 10 * time.Second}
	if cfg.UseTLS {
		conn, err := tls.DialWithDialer(&dialer, "tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return err
		}
		defer conn.Close()
		client, err := smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
		return sendWithClient(ctx, client, cfg, from.Address, to.Address, raw)
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	if cfg.StartTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				_ = client.Close()
				return err
			}
		}
	}
	return sendWithClient(ctx, client, cfg, from.Address, to.Address, raw)
}

func sendWithClient(ctx context.Context, client *smtp.Client, cfg SMTPConfig, from, to string, raw []byte) error {
	done := make(chan error, 1)
	go func() {
		defer client.Quit()
		if cfg.Username != "" {
			if err := client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, strings.TrimSpace(cfg.Host))); err != nil {
				done <- err
				return
			}
		}
		if err := client.Mail(from); err != nil {
			done <- err
			return
		}
		if err := client.Rcpt(to); err != nil {
			done <- err
			return
		}
		writer, err := client.Data()
		if err != nil {
			done <- err
			return
		}
		if _, err := writer.Write(raw); err != nil {
			_ = writer.Close()
			done <- err
			return
		}
		done <- writer.Close()
	}()
	select {
	case <-ctx.Done():
		_ = client.Close()
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func buildRFC822Message(from, to, subject, text string) []byte {
	subject = strings.ReplaceAll(strings.TrimSpace(subject), "\n", " ")
	subject = strings.ReplaceAll(subject, "\r", " ")
	headers := []string{
		"From: " + from,
		"To: " + to,
		"Subject: " + subject,
		"Content-Type: text/plain; charset=UTF-8",
		"X-PostFlow-Notification: publish-failure",
	}
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + text)
}

var _ ports.PublishFailureNotifier = Service{}
