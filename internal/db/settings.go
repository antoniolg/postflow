package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
)

const (
	SettingUITimezone           = "ui.timezone"
	SettingBootstrapXAccountOff = "bootstrap.x_account.disabled"
)

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("setting key is required")
	}
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("setting key is required")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("setting value is required")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

func (s *Store) GetUITimezone(ctx context.Context) (string, error) {
	value, err := s.GetSetting(ctx, SettingUITimezone)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func (s *Store) SetUITimezone(ctx context.Context, timezone string) error {
	return s.SetSetting(ctx, SettingUITimezone, timezone)
}

func (s *Store) GetBootstrapXAccountDisabled(ctx context.Context) (bool, error) {
	value, err := s.GetSetting(ctx, SettingBootstrapXAccountOff)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	disabled, parseErr := strconv.ParseBool(strings.TrimSpace(value))
	if parseErr != nil {
		return false, nil
	}
	return disabled, nil
}

func (s *Store) SetBootstrapXAccountDisabled(ctx context.Context, disabled bool) error {
	return s.SetSetting(ctx, SettingBootstrapXAccountOff, strconv.FormatBool(disabled))
}
