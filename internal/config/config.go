package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port              string
	DatabasePath      string
	DataDir           string
	WorkerInterval    time.Duration
	RetryBackoff      time.Duration
	DefaultMaxRetries int
	RateLimitRPM      int
	APIToken          string
	UIBasicUser       string
	UIBasicPass       string
	OwnerEmail        string
	OwnerPasswordHash string
	LogLevel          string
	PostflowDriver    string
	PublicBaseURL     string
	MasterKeyBase64   string
	X                 XConfig
	LinkedIn          LinkedInConfig
	Meta              MetaConfig
}

type XConfig struct {
	APIBaseURL    string
	UploadBaseURL string
	AuthBaseURL   string
	TokenURL      string
	ClientID      string
	ClientSecret  string
}

type LinkedInConfig struct {
	ClientID     string
	ClientSecret string
}

type MetaConfig struct {
	AppID     string
	AppSecret string
}

func Load() (Config, error) {
	if err := loadDotEnv(); err != nil {
		return Config{}, err
	}

	interval := 30 * time.Second
	if raw := os.Getenv("WORKER_INTERVAL_SECONDS"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return Config{}, fmt.Errorf("invalid WORKER_INTERVAL_SECONDS: %q", raw)
		}
		interval = time.Duration(v) * time.Second
	}

	retryBackoff := 30 * time.Second
	if raw := os.Getenv("RETRY_BACKOFF_SECONDS"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return Config{}, fmt.Errorf("invalid RETRY_BACKOFF_SECONDS: %q", raw)
		}
		retryBackoff = time.Duration(v) * time.Second
	}

	defaultMaxRetries := 3
	if raw := os.Getenv("DEFAULT_MAX_RETRIES"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return Config{}, fmt.Errorf("invalid DEFAULT_MAX_RETRIES: %q", raw)
		}
		defaultMaxRetries = v
	}
	rateLimitRPM := 120
	if raw := os.Getenv("RATE_LIMIT_RPM"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			return Config{}, fmt.Errorf("invalid RATE_LIMIT_RPM: %q", raw)
		}
		rateLimitRPM = v
	}

	cfg := Config{
		Port:              getenv("PORT", "8080"),
		DatabasePath:      getenv("DATABASE_PATH", "postflow.db"),
		DataDir:           getenv("DATA_DIR", "data"),
		WorkerInterval:    interval,
		RetryBackoff:      retryBackoff,
		DefaultMaxRetries: defaultMaxRetries,
		RateLimitRPM:      rateLimitRPM,
		APIToken:          os.Getenv("API_TOKEN"),
		UIBasicUser:       os.Getenv("UI_BASIC_USER"),
		UIBasicPass:       os.Getenv("UI_BASIC_PASS"),
		OwnerEmail:        strings.TrimSpace(os.Getenv("OWNER_EMAIL")),
		OwnerPasswordHash: strings.TrimSpace(os.Getenv("OWNER_PASSWORD_HASH")),
		LogLevel:          getenv("LOG_LEVEL", "info"),
		PostflowDriver:    getenv("POSTFLOW_DRIVER", "mock"),
		PublicBaseURL:     strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/"),
		MasterKeyBase64:   strings.TrimSpace(os.Getenv("POSTFLOW_MASTER_KEY")),
		X: XConfig{
			APIBaseURL:    getenv("X_API_BASE_URL", "https://api.x.com"),
			UploadBaseURL: getenv("X_UPLOAD_BASE_URL", "https://upload.twitter.com"),
			AuthBaseURL:   getenv("X_AUTH_BASE_URL", "https://x.com"),
			TokenURL:      getenv("X_TOKEN_URL", "https://api.x.com/2/oauth2/token"),
			ClientID:      strings.TrimSpace(os.Getenv("X_CLIENT_ID")),
			ClientSecret:  strings.TrimSpace(os.Getenv("X_CLIENT_SECRET")),
		},
		LinkedIn: LinkedInConfig{
			ClientID:     strings.TrimSpace(os.Getenv("LINKEDIN_CLIENT_ID")),
			ClientSecret: strings.TrimSpace(os.Getenv("LINKEDIN_CLIENT_SECRET")),
		},
		Meta: MetaConfig{
			AppID:     strings.TrimSpace(os.Getenv("META_APP_ID")),
			AppSecret: strings.TrimSpace(os.Getenv("META_APP_SECRET")),
		},
	}
	if cfg.MasterKeyBase64 == "" {
		return Config{}, errors.New("POSTFLOW_MASTER_KEY is required")
	}
	if cfg.PublicBaseURL == "" {
		cfg.PublicBaseURL = "http://localhost:" + cfg.Port
	}
	return cfg, nil
}

func loadDotEnv() error {
	envFile := strings.TrimSpace(os.Getenv("ENV_FILE"))
	if envFile == "" {
		envFile = ".env"
	}

	if err := godotenv.Load(envFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("load %s: %w", envFile, err)
	}
	return nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
