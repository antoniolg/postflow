package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpListAccountsOutput struct {
	Count int                    `json:"count"`
	Items []domain.SocialAccount `json:"items"`
}

type mcpCreateStaticAccountInput struct {
	Platform          string         `json:"platform" jsonschema:"Platform: x|linkedin|facebook|instagram."`
	DisplayName       string         `json:"display_name,omitempty" jsonschema:"Optional display name."`
	ExternalAccountID string         `json:"external_account_id" jsonschema:"Network account ID or handle."`
	Credentials       map[string]any `json:"credentials" jsonschema:"Credential payload. Must include access_token. X also requires access_token_secret."`
	XPremium          *bool          `json:"x_premium,omitempty" jsonschema:"Optional. Only for X accounts."`
}

type mcpCreateStaticAccountOutput struct {
	Account domain.SocialAccount `json:"account"`
}

type mcpAccountMutationInput struct {
	AccountID string `json:"account_id" jsonschema:"Target account ID."`
}

type mcpAccountStatusOutput struct {
	AccountID string `json:"account_id"`
	Status    string `json:"status"`
}

type mcpSetXPremiumInput struct {
	AccountID string `json:"account_id" jsonschema:"Target account ID."`
	XPremium  bool   `json:"x_premium" jsonschema:"Premium enabled for X account."`
}

type mcpSetXPremiumOutput struct {
	AccountID string `json:"account_id"`
	XPremium  bool   `json:"x_premium"`
}

type mcpDeleteAccountOutput struct {
	AccountID string `json:"account_id"`
	Deleted   bool   `json:"deleted"`
}

func (s Server) mcpListAccountsTool(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, mcpListAccountsOutput, error) {
	accounts, err := s.Store.ListAccounts(ctx)
	if err != nil {
		return nil, mcpListAccountsOutput{}, err
	}
	return nil, mcpListAccountsOutput{
		Count: len(accounts),
		Items: accounts,
	}, nil
}

func (s Server) mcpCreateStaticAccountTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpCreateStaticAccountInput) (*mcp.CallToolResult, mcpCreateStaticAccountOutput, error) {
	platform := normalizePlatform(in.Platform)
	if platform == "" {
		return nil, mcpCreateStaticAccountOutput{}, errors.New("platform is required")
	}
	if _, ok := s.providerRegistry().Get(platform); !ok {
		return nil, mcpCreateStaticAccountOutput{}, errors.New("provider is not configured for platform")
	}

	credentials, err := decodeCredentials(in.Credentials)
	if err != nil {
		return nil, mcpCreateStaticAccountOutput{}, err
	}
	if strings.TrimSpace(credentials.AccessToken) == "" {
		return nil, mcpCreateStaticAccountOutput{}, errors.New("credentials.access_token is required")
	}
	if platform == domain.PlatformX && strings.TrimSpace(credentials.AccessTokenSecret) == "" {
		return nil, mcpCreateStaticAccountOutput{}, errors.New("credentials.access_token_secret is required for x")
	}

	account, err := s.Store.UpsertAccount(ctx, db.UpsertAccountParams{
		Platform:          platform,
		DisplayName:       strings.TrimSpace(in.DisplayName),
		ExternalAccountID: strings.TrimSpace(in.ExternalAccountID),
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		return nil, mcpCreateStaticAccountOutput{}, err
	}
	if err := s.saveCredentials(ctx, account.ID, credentials); err != nil {
		return nil, mcpCreateStaticAccountOutput{}, err
	}
	if in.XPremium != nil {
		if err := s.Store.UpdateAccountXPremium(ctx, account.ID, *in.XPremium); err != nil {
			return nil, mcpCreateStaticAccountOutput{}, err
		}
		account, err = s.Store.GetAccount(ctx, account.ID)
		if err != nil {
			return nil, mcpCreateStaticAccountOutput{}, err
		}
	}

	return nil, mcpCreateStaticAccountOutput{Account: account}, nil
}

func (s Server) mcpConnectAccountTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpAccountMutationInput) (*mcp.CallToolResult, mcpAccountStatusOutput, error) {
	accountID := strings.TrimSpace(in.AccountID)
	if accountID == "" {
		return nil, mcpAccountStatusOutput{}, errors.New("account_id is required")
	}
	if _, err := s.Store.GetAccountCredentials(ctx, accountID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, mcpAccountStatusOutput{}, errors.New("account has no saved credentials")
		}
		return nil, mcpAccountStatusOutput{}, err
	}
	if err := s.Store.UpdateAccountStatus(ctx, accountID, domain.AccountStatusConnected, nil); err != nil {
		if errors.Is(err, db.ErrAccountNotFound) {
			return nil, mcpAccountStatusOutput{}, errors.New("account not found")
		}
		return nil, mcpAccountStatusOutput{}, err
	}
	return nil, mcpAccountStatusOutput{
		AccountID: accountID,
		Status:    string(domain.AccountStatusConnected),
	}, nil
}

func (s Server) mcpDisconnectAccountTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpAccountMutationInput) (*mcp.CallToolResult, mcpAccountStatusOutput, error) {
	accountID := strings.TrimSpace(in.AccountID)
	if accountID == "" {
		return nil, mcpAccountStatusOutput{}, errors.New("account_id is required")
	}
	if err := s.Store.DisconnectAccount(ctx, accountID); err != nil {
		if errors.Is(err, db.ErrAccountNotFound) {
			return nil, mcpAccountStatusOutput{}, errors.New("account not found")
		}
		return nil, mcpAccountStatusOutput{}, err
	}
	return nil, mcpAccountStatusOutput{
		AccountID: accountID,
		Status:    string(domain.AccountStatusDisconnected),
	}, nil
}

func (s Server) mcpSetXPremiumTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpSetXPremiumInput) (*mcp.CallToolResult, mcpSetXPremiumOutput, error) {
	accountID := strings.TrimSpace(in.AccountID)
	if accountID == "" {
		return nil, mcpSetXPremiumOutput{}, errors.New("account_id is required")
	}
	if err := s.Store.UpdateAccountXPremium(ctx, accountID, in.XPremium); err != nil {
		switch {
		case errors.Is(err, db.ErrAccountNotFound):
			return nil, mcpSetXPremiumOutput{}, errors.New("account not found")
		case errors.Is(err, db.ErrAccountNotXPlatform):
			return nil, mcpSetXPremiumOutput{}, errors.New("x premium setting is only available for x accounts")
		default:
			return nil, mcpSetXPremiumOutput{}, err
		}
	}
	return nil, mcpSetXPremiumOutput{
		AccountID: accountID,
		XPremium:  in.XPremium,
	}, nil
}

func (s Server) mcpDeleteAccountTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpAccountMutationInput) (*mcp.CallToolResult, mcpDeleteAccountOutput, error) {
	accountID := strings.TrimSpace(in.AccountID)
	if accountID == "" {
		return nil, mcpDeleteAccountOutput{}, errors.New("account_id is required")
	}
	if err := s.Store.DeleteAccount(ctx, accountID); err != nil {
		switch {
		case errors.Is(err, db.ErrAccountNotFound):
			return nil, mcpDeleteAccountOutput{}, errors.New("account not found")
		case errors.Is(err, db.ErrAccountNotDisconnect):
			return nil, mcpDeleteAccountOutput{}, errors.New("account must be disconnected first")
		case errors.Is(err, db.ErrAccountHasPosts):
			return nil, mcpDeleteAccountOutput{}, errors.New("account has pending posts")
		default:
			return nil, mcpDeleteAccountOutput{}, fmt.Errorf("failed to delete account: %w", err)
		}
	}
	return nil, mcpDeleteAccountOutput{
		AccountID: accountID,
		Deleted:   true,
	}, nil
}
