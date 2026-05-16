package api

import (
	"context"
	"errors"
	"strings"
	"time"

	notificationsapp "github.com/antoniolg/postflow/internal/application/notifications"
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

type mcpSetSMTPNotificationsInput struct {
	Enabled       bool   `json:"enabled" jsonschema:"Whether publish failure emails are enabled."`
	Host          string `json:"host" jsonschema:"SMTP server host."`
	Port          int    `json:"port" jsonschema:"SMTP server port, usually 587 for STARTTLS or 465 for TLS."`
	Username      string `json:"username,omitempty" jsonschema:"SMTP auth username, if required."`
	Password      string `json:"password,omitempty" jsonschema:"SMTP auth password. Omit to keep the current password."`
	KeepPassword  bool   `json:"keep_password,omitempty" jsonschema:"Keep the existing password when password is omitted."`
	From          string `json:"from" jsonschema:"Sender email address."`
	To            string `json:"to" jsonschema:"Recipient email address."`
	SubjectPrefix string `json:"subject_prefix,omitempty" jsonschema:"Email subject prefix."`
	UseTLS        bool   `json:"use_tls,omitempty" jsonschema:"Use implicit TLS, usually port 465."`
	StartTLS      bool   `json:"start_tls,omitempty" jsonschema:"Use STARTTLS, usually port 587."`
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

func (s Server) mcpSetSMTPNotificationsTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpSetSMTPNotificationsInput) (*mcp.CallToolResult, notificationsapp.SMTPConfigView, error) {
	service := notificationsapp.Service{Store: s.Store, Cipher: s.credentialsCipher()}
	view, err := service.SaveSMTPConfig(ctx, notificationsapp.SMTPConfigUpdate{
		Enabled:       in.Enabled,
		Host:          in.Host,
		Port:          in.Port,
		Username:      in.Username,
		Password:      in.Password,
		KeepPassword:  in.KeepPassword || strings.TrimSpace(in.Password) == "",
		From:          in.From,
		To:            in.To,
		SubjectPrefix: in.SubjectPrefix,
		UseTLS:        in.UseTLS,
		StartTLS:      in.StartTLS,
	})
	if err != nil {
		return nil, notificationsapp.SMTPConfigView{}, err
	}
	return nil, view, nil
}
