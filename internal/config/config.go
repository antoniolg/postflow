package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port            string
	DatabasePath    string
	DataDir         string
	WorkerInterval  time.Duration
	PublisherDriver string
	X               XConfig
}

type XConfig struct {
	APIBaseURL        string
	UploadBaseURL     string
	APIKey            string
	APIKeySecret      string
	AccessToken       string
	AccessTokenSecret string
}

func Load() (Config, error) {
	interval := 30 * time.Second
	if raw := os.Getenv("WORKER_INTERVAL_SECONDS"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return Config{}, fmt.Errorf("invalid WORKER_INTERVAL_SECONDS: %q", raw)
		}
		interval = time.Duration(v) * time.Second
	}

	cfg := Config{
		Port:            getenv("PORT", "8080"),
		DatabasePath:    getenv("DATABASE_PATH", "publisher.db"),
		DataDir:         getenv("DATA_DIR", "data"),
		WorkerInterval:  interval,
		PublisherDriver: getenv("PUBLISHER_DRIVER", "mock"),
		X: XConfig{
			APIBaseURL:        getenv("X_API_BASE_URL", "https://api.twitter.com"),
			UploadBaseURL:     getenv("X_UPLOAD_BASE_URL", "https://upload.twitter.com"),
			APIKey:            os.Getenv("X_API_KEY"),
			APIKeySecret:      os.Getenv("X_API_SECRET"),
			AccessToken:       os.Getenv("X_ACCESS_TOKEN"),
			AccessTokenSecret: os.Getenv("X_ACCESS_TOKEN_SECRET"),
		},
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
