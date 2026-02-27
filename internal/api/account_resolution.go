package api

import (
	"context"
	"strings"

	"github.com/antoniolg/publisher/internal/domain"
)

func (s Server) resolveTargetAccount(ctx context.Context, accountIDRaw string) (domain.SocialAccount, error) {
	return s.Store.GetAccount(ctx, strings.TrimSpace(accountIDRaw))
}
