package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/antoniolg/postflow/internal/api"
	"github.com/antoniolg/postflow/internal/config"
	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/observability"
	"github.com/antoniolg/postflow/internal/postflow"
	"github.com/antoniolg/postflow/internal/secure"
	"github.com/antoniolg/postflow/internal/worker"
)

var Version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(fmt.Sprintf("load config: %v", err))
	}
	observability.Setup(cfg.LogLevel)

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		slog.Error("mkdir data dir", "error", err, "data_dir", cfg.DataDir)
		os.Exit(1)
	}

	store, err := db.Open(cfg.DatabasePath)
	if err != nil {
		slog.Error("open database", "error", err, "database_path", cfg.DatabasePath)
		os.Exit(1)
	}
	defer store.Close()

	cipher, err := secure.NewCipherFromBase64(cfg.MasterKeyBase64, 1)
	if err != nil {
		slog.Error("build credentials cipher", "error", err)
		os.Exit(1)
	}

	registry, err := buildProviderRegistry(cfg, cipher)
	if err != nil {
		slog.Error("build provider registry", "error", err)
		os.Exit(1)
	}

	apiServer := api.Server{
		Store:             store,
		DataDir:           cfg.DataDir,
		DefaultMaxRetries: cfg.DefaultMaxRetries,
		RateLimitRPM:      cfg.RateLimitRPM,
		APIToken:          cfg.APIToken,
		UIBasicUser:       cfg.UIBasicUser,
		UIBasicPass:       cfg.UIBasicPass,
		Registry:          registry,
		Cipher:            cipher,
		PublicBaseURL:     cfg.PublicBaseURL,
		AppVersion:        Version,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	w := worker.Worker{
		Store:        store,
		Registry:     registry,
		Cipher:       cipher,
		Interval:     cfg.WorkerInterval,
		RetryBackoff: cfg.RetryBackoff,
	}
	go w.Start(ctx)

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      apiServer.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	slog.Info("postflow listening", "addr", ":"+cfg.Port, "log_level", cfg.LogLevel)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("http server failed", "error", err)
		os.Exit(1)
	}
}

func buildProviderRegistry(cfg config.Config, cipher *secure.Cipher) (*postflow.ProviderRegistry, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.PostflowDriver))
	switch driver {
	case "", "mock":
		return postflow.NewProviderRegistry(
			postflow.NewMockProvider(domain.PlatformX),
			postflow.NewMockProvider(domain.PlatformLinkedIn),
			postflow.NewMockProvider(domain.PlatformFacebook),
			postflow.NewMockProvider(domain.PlatformInstagram),
		), nil
	case "live", "real", "x":
		xProvider := postflow.NewXProvider(postflow.XConfig{
			APIBaseURL:    cfg.X.APIBaseURL,
			UploadBaseURL: cfg.X.UploadBaseURL,
			AuthBaseURL:   cfg.X.AuthBaseURL,
			TokenURL:      cfg.X.TokenURL,
			ClientID:      cfg.X.ClientID,
			ClientSecret:  cfg.X.ClientSecret,
		})
		linkedinProvider := postflow.NewLinkedInProvider(postflow.LinkedInProviderConfig{
			ClientID:     cfg.LinkedIn.ClientID,
			ClientSecret: cfg.LinkedIn.ClientSecret,
		})
		metaCfg := postflow.MetaProviderConfig{
			AppID:           cfg.Meta.AppID,
			AppSecret:       cfg.Meta.AppSecret,
			MediaURLBuilder: buildSignedMediaURLBuilder(cfg.PublicBaseURL, cipher),
		}
		facebookProvider := postflow.NewFacebookProvider(metaCfg)
		instagramProvider := postflow.NewInstagramProvider(metaCfg)
		return postflow.NewProviderRegistry(xProvider, linkedinProvider, facebookProvider, instagramProvider), nil
	default:
		return nil, fmt.Errorf("unsupported POSTFLOW_DRIVER=%q (valid: mock, live)", cfg.PostflowDriver)
	}
}

func buildSignedMediaURLBuilder(baseURL string, cipher *secure.Cipher) func(media domain.Media) (string, error) {
	if cipher == nil {
		return nil
	}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil
	}
	return func(media domain.Media) (string, error) {
		mediaID := strings.TrimSpace(media.ID)
		if mediaID == "" {
			return "", fmt.Errorf("media id is required")
		}
		expiration := time.Now().UTC().Add(20 * time.Minute).Unix()
		payload := fmt.Sprintf("%s:%d", mediaID, expiration)
		signature := cipher.SignString(payload)
		if signature == "" {
			return "", fmt.Errorf("unable to sign media url")
		}
		query := url.Values{}
		query.Set("exp", strconv.FormatInt(expiration, 10))
		query.Set("sig", signature)
		return fmt.Sprintf("%s/media/%s/content?%s", base, url.PathEscape(mediaID), query.Encode()), nil
	}
}
