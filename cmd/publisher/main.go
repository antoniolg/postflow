package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/antoniolg/publisher/internal/api"
	"github.com/antoniolg/publisher/internal/config"
	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/publisher"
	"github.com/antoniolg/publisher/internal/worker"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatalf("mkdir data dir: %v", err)
	}

	store, err := db.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer store.Close()

	apiServer := api.Server{
		Store:             store,
		DataDir:           cfg.DataDir,
		DefaultMaxRetries: cfg.DefaultMaxRetries,
		RateLimitRPM:      cfg.RateLimitRPM,
		APIToken:          cfg.APIToken,
		UIBasicUser:       cfg.UIBasicUser,
		UIBasicPass:       cfg.UIBasicPass,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := buildPublisherClient(cfg)
	if err != nil {
		log.Fatalf("publisher client: %v", err)
	}

	w := worker.Worker{
		Store:        store,
		Client:       client,
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

	log.Printf("publisher listening on http://localhost:%s", cfg.Port)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("http server: %v", err)
	}
}

func buildPublisherClient(cfg config.Config) (publisher.Client, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.PublisherDriver)) {
	case "", "mock":
		return publisher.MockClient{}, nil
	case "x":
		client, err := publisher.NewXClient(publisher.XConfig{
			APIBaseURL:        cfg.X.APIBaseURL,
			UploadBaseURL:     cfg.X.UploadBaseURL,
			APIKey:            cfg.X.APIKey,
			APIKeySecret:      cfg.X.APIKeySecret,
			AccessToken:       cfg.X.AccessToken,
			AccessTokenSecret: cfg.X.AccessTokenSecret,
		})
		if err != nil {
			return nil, err
		}
		return client, nil
	default:
		return nil, fmt.Errorf("unsupported PUBLISHER_DRIVER=%q (valid: mock, x)", cfg.PublisherDriver)
	}
}
