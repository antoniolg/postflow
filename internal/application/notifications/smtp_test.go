package notifications

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/antoniolg/postflow/internal/application/ports"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/secure"
)

type memorySettingsStore struct {
	values map[string]string
}

func (m *memorySettingsStore) GetSetting(_ context.Context, key string) (string, error) {
	if m.values == nil {
		return "", sql.ErrNoRows
	}
	value, ok := m.values[key]
	if !ok {
		return "", sql.ErrNoRows
	}
	return value, nil
}

func (m *memorySettingsStore) SetSetting(_ context.Context, key, value string) error {
	if m.values == nil {
		m.values = make(map[string]string)
	}
	m.values[key] = value
	return nil
}

type recordingSMTPSender struct {
	calls   int
	cfg     SMTPConfig
	message EmailMessage
	sendErr error
}

func (r *recordingSMTPSender) Send(_ context.Context, cfg SMTPConfig, message EmailMessage) error {
	r.calls++
	r.cfg = cfg
	r.message = message
	return r.sendErr
}

func TestServiceSavesSMTPConfigWithEncryptedPassword(t *testing.T) {
	cipher := testCipher(t)
	store := &memorySettingsStore{}
	service := Service{Store: store, Cipher: cipher}

	view, err := service.SaveSMTPConfig(t.Context(), SMTPConfigUpdate{
		Enabled:       true,
		Host:          "smtp.sendgrid.net",
		Port:          587,
		Username:      "apikey",
		Password:      "secret",
		From:          "PostFlow <postflow@example.com>",
		To:            "antonio@example.com",
		SubjectPrefix: "PostFlow failed",
		StartTLS:      true,
	})
	if err != nil {
		t.Fatalf("save smtp config: %v", err)
	}
	if !view.Ready || !view.PasswordConfigured {
		t.Fatalf("expected ready config with password, got %+v", view)
	}
	raw := store.values[SettingSMTPNotifications]
	if strings.Contains(raw, "secret") {
		t.Fatalf("stored smtp config leaked plaintext password: %s", raw)
	}
	cfg, ok, err := service.GetSMTPConfig(t.Context())
	if err != nil {
		t.Fatalf("get smtp config: %v", err)
	}
	if !ok || cfg.Password != "secret" || cfg.Host != "smtp.sendgrid.net" {
		t.Fatalf("unexpected smtp config: ok=%v cfg=%+v", ok, cfg)
	}
}

func TestServiceNotifiesPublishFailureWhenEnabled(t *testing.T) {
	cipher := testCipher(t)
	store := &memorySettingsStore{}
	sender := &recordingSMTPSender{}
	service := Service{Store: store, Cipher: cipher, Sender: sender}
	if _, err := service.SaveSMTPConfig(t.Context(), SMTPConfigUpdate{
		Enabled:  true,
		Host:     "smtp.example.com",
		Port:     587,
		From:     "postflow@example.com",
		To:       "antonio@example.com",
		StartTLS: true,
	}); err != nil {
		t.Fatalf("save smtp config: %v", err)
	}

	err := service.NotifyPublishFailure(t.Context(), ports.PublishFailureNotification{
		Post: domain.Post{
			ID:          "pst_123",
			Platform:    domain.PlatformLinkedIn,
			Status:      domain.PostStatusFailed,
			Attempts:    3,
			MaxAttempts: 3,
			Text:        "hello failure",
		},
		Account: domain.SocialAccount{ID: "acc_1", DisplayName: "Antonio", Platform: domain.PlatformLinkedIn},
		Error:   errors.New("provider exploded"),
	})
	if err != nil {
		t.Fatalf("notify publish failure: %v", err)
	}
	if sender.calls != 1 {
		t.Fatalf("expected one send call, got %d", sender.calls)
	}
	if !strings.Contains(sender.message.Subject, "pst_123") || !strings.Contains(sender.message.Text, "provider exploded") {
		t.Fatalf("unexpected email message: %+v", sender.message)
	}
}

func TestServiceSendsSMTPTest(t *testing.T) {
	cipher := testCipher(t)
	store := &memorySettingsStore{}
	sender := &recordingSMTPSender{}
	service := Service{Store: store, Cipher: cipher, Sender: sender}
	if _, err := service.SaveSMTPConfig(t.Context(), SMTPConfigUpdate{
		Enabled:       false,
		Host:          "smtp.example.com",
		Port:          587,
		From:          "postflow@example.com",
		To:            "antonio@example.com",
		SubjectPrefix: "PostFlow failed",
		StartTLS:      true,
	}); err != nil {
		t.Fatalf("save smtp config: %v", err)
	}

	view, err := service.SendSMTPTest(t.Context())
	if err != nil {
		t.Fatalf("send smtp test: %v", err)
	}
	if !view.Configured {
		t.Fatalf("expected configured view, got %+v", view)
	}
	if sender.calls != 1 {
		t.Fatalf("expected one send call, got %d", sender.calls)
	}
	if sender.message.Subject != "PostFlow failed: SMTP test" {
		t.Fatalf("unexpected test subject: %q", sender.message.Subject)
	}
	if !strings.Contains(sender.message.Text, "SMTP test email") {
		t.Fatalf("unexpected test body: %q", sender.message.Text)
	}
}

func testCipher(t *testing.T) *secure.Cipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	cipher, err := secure.NewCipher(key, 1)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	return cipher
}
