package publisher

import (
	"context"
	"fmt"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
)

type Client interface {
	Publish(ctx context.Context, post domain.Post) (string, error)
}

type MockClient struct{}

func (m MockClient) Publish(_ context.Context, post domain.Post) (string, error) {
	return fmt.Sprintf("mock_%s_%d", post.Platform, time.Now().Unix()), nil
}
