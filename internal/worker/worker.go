package worker

import (
	"context"
	"log"
	"time"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/publisher"
)

type Worker struct {
	Store        *db.Store
	Client       publisher.Client
	Interval     time.Duration
	RetryBackoff time.Duration
}

func (w Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	w.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w Worker) runOnce(ctx context.Context) {
	posts, err := w.Store.ClaimDuePosts(ctx, 25)
	if err != nil {
		log.Printf("worker: claim due posts: %v", err)
		return
	}
	for _, p := range posts {
		externalID, err := w.Client.Publish(ctx, p)
		if err != nil {
			_ = w.Store.RecordPublishFailure(ctx, p.ID, err, w.RetryBackoff)
			log.Printf("worker: publish %s failed: %v", p.ID, err)
			continue
		}
		if err := w.Store.MarkPublished(ctx, p.ID, externalID); err != nil {
			log.Printf("worker: mark published %s failed: %v", p.ID, err)
		}
	}
}
