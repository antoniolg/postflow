package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadReadsDotEnvFile(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, "publisher.env")
	envContent := "PORT=9090\nWORKER_INTERVAL_SECONDS=45\nPUBLISHER_DRIVER=x\nX_API_KEY=abc123\n"
	if err := os.WriteFile(envPath, []byte(envContent), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	t.Setenv("ENV_FILE", envPath)
	unsetEnvForTest(t, "PORT")
	unsetEnvForTest(t, "WORKER_INTERVAL_SECONDS")
	unsetEnvForTest(t, "PUBLISHER_DRIVER")
	unsetEnvForTest(t, "X_API_KEY")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Port != "9090" {
		t.Fatalf("Port = %q, want %q", cfg.Port, "9090")
	}
	if cfg.WorkerInterval != 45*time.Second {
		t.Fatalf("WorkerInterval = %v, want %v", cfg.WorkerInterval, 45*time.Second)
	}
	if cfg.PublisherDriver != "x" {
		t.Fatalf("PublisherDriver = %q, want %q", cfg.PublisherDriver, "x")
	}
	if cfg.X.APIKey != "abc123" {
		t.Fatalf("X.APIKey = %q, want %q", cfg.X.APIKey, "abc123")
	}
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	previous, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if !existed {
			_ = os.Unsetenv(key)
			return
		}
		_ = os.Setenv(key, previous)
	})
}

func TestLoadEnvVarsOverrideDotEnvFile(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, "publisher.env")
	if err := os.WriteFile(envPath, []byte("PORT=9999\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	t.Setenv("ENV_FILE", envPath)
	t.Setenv("PORT", "7777")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Port != "7777" {
		t.Fatalf("Port = %q, want %q", cfg.Port, "7777")
	}
}

func TestLoadMissingDotEnvFileDoesNotFail(t *testing.T) {
	t.Setenv("ENV_FILE", filepath.Join(t.TempDir(), "missing.env"))

	if _, err := Load(); err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
}
