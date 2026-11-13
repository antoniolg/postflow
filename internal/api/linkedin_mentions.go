package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	mentionsapp "github.com/antoniolg/postflow/internal/application/mentions"
	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/postflow"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type linkedInMentionsResponse struct {
	AccountID string             `json:"account_id"`
	Count     int                `json:"count"`
	Items     []postflow.Mention `json:"items"`
}

// apiCredentialsStore gives application services access to the encrypted
// account credentials that the API stores.
type apiCredentialsStore struct {
	server Server
}

func (c apiCredentialsStore) LoadCredentials(ctx context.Context, accountID string) (postflow.Credentials, error) {
	encrypted, err := c.server.Store.GetAccountCredentials(ctx, accountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return postflow.Credentials{}, nil
		}
		return postflow.Credentials{}, err
	}
	var credentials postflow.Credentials
	if err := c.server.credentialsCipher().DecryptJSON(encrypted.Ciphertext, encrypted.Nonce, &credentials); err != nil {
		return postflow.Credentials{}, err
	}
	return credentials, nil
}

func (c apiCredentialsStore) SaveCredentials(ctx context.Context, accountID string, credentials postflow.Credentials) error {
	return c.server.saveCredentials(ctx, accountID, credentials)
}

func (s Server) mentionsService() mentionsapp.Service {
	return mentionsapp.Service{
		Store:       s.Store,
		Registry:    s.providerRegistry(),
		Credentials: apiCredentialsStore{server: s},
	}
}

func (s Server) listLinkedInMentions(ctx context.Context, accountID string, limit int) (linkedInMentionsResponse, error) {
	items, err := s.mentionsService().List(ctx, accountID, limit)
	if err != nil {
		return linkedInMentionsResponse{}, err
	}
	return linkedInMentionsResponse{
		AccountID: strings.TrimSpace(accountID),
		Count:     len(items),
		Items:     items,
	}, nil
}

func linkedInMentionsErrorStatus(err error) int {
	switch {
	case errors.Is(err, mentionsapp.ErrAccountIDRequired), errors.Is(err, mentionsapp.ErrUnsupported):
		return http.StatusBadRequest
	case errors.Is(err, db.ErrAccountNotFound):
		return http.StatusNotFound
	case errors.Is(err, mentionsapp.ErrNoCredentials):
		return http.StatusConflict
	default:
		return http.StatusBadGateway
	}
}

func (s Server) handleListLinkedInMentions(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, errors.New("limit must be a positive integer"))
			return
		}
		limit = parsed
	}
	out, err := s.listLinkedInMentions(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		writeError(w, linkedInMentionsErrorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type mcpListLinkedInMentionsInput struct {
	AccountID string `json:"account_id" jsonschema:"LinkedIn organization account ID (acc_...)."`
	Limit     int    `json:"limit,omitempty" jsonschema:"How many recent posts to read. Default 20, max 100."`
}

func (s Server) mcpListLinkedInMentionsTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpListLinkedInMentionsInput) (*mcp.CallToolResult, linkedInMentionsResponse, error) {
	out, err := s.listLinkedInMentions(ctx, in.AccountID, in.Limit)
	if err != nil {
		return nil, linkedInMentionsResponse{}, err
	}
	return nil, out, nil
}
