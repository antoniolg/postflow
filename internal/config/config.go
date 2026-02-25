package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port           string
	DatabasePath   string
	DataDir        string
	WorkerInterval time.Duration
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
		Port:           getenv("PORT", "8080"),
		DatabasePath:   getenv("DATABASE_PATH", "publisher.db"),
		DataDir:        getenv("DATA_DIR", "data"),
		WorkerInterval: interval,
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
