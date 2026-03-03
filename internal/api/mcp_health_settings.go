package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpHealthOutput struct {
	Status string `json:"status"`
}

type mcpSetTimezoneInput struct {
	Timezone string `json:"timezone" jsonschema:"IANA timezone, e.g. Europe/Madrid."`
}

type mcpSetTimezoneOutput struct {
	Timezone string `json:"timezone"`
}

func (s Server) mcpHealthTool(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, mcpHealthOutput, error) {
	return nil, mcpHealthOutput{Status: "ok"}, nil
}

func (s Server) mcpSetTimezoneTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpSetTimezoneInput) (*mcp.CallToolResult, mcpSetTimezoneOutput, error) {
	timezone := strings.TrimSpace(in.Timezone)
	if timezone == "" {
		return nil, mcpSetTimezoneOutput{}, errors.New("timezone is required")
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return nil, mcpSetTimezoneOutput{}, err
	}
	if err := s.Store.SetUITimezone(ctx, timezone); err != nil {
		return nil, mcpSetTimezoneOutput{}, err
	}
	return nil, mcpSetTimezoneOutput{Timezone: timezone}, nil
}
