package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
)

func runSettings(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: postflow settings <set-timezone|set-smtp> ...")
		return 2
	}
	if args[0] == "set-smtp" {
		return runSettingsSetSMTP(ctx, client, cfg, args[1:], stdout, stderr)
	}
	if args[0] != "set-timezone" {
		fmt.Fprintln(stderr, "usage: postflow settings <set-timezone|set-smtp> ...")
		return 2
	}
	fs := flag.NewFlagSet("settings set-timezone", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timezone := fs.String("timezone", "", "IANA timezone, e.g. Europe/Madrid")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if strings.TrimSpace(*timezone) == "" {
		fmt.Fprintln(stderr, "--timezone is required")
		return 2
	}
	var out map[string]any
	if err := client.Post(ctx, "/settings/timezone", map[string]any{"timezone": strings.TrimSpace(*timezone)}, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "timezone updated: %s\n", strings.TrimSpace(*timezone))
	})
	return 0
}

func runSettingsSetSMTP(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("settings set-smtp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	enabled := fs.Bool("enabled", true, "enable publish failure emails")
	host := fs.String("host", "", "SMTP server host")
	port := fs.Int("port", 587, "SMTP server port")
	username := fs.String("username", "", "SMTP username")
	password := fs.String("password", "", "SMTP password")
	from := fs.String("from", "", "sender email address")
	to := fs.String("to", "", "recipient email address")
	subjectPrefix := fs.String("subject-prefix", "PostFlow publish failed", "email subject prefix")
	useTLS := fs.Bool("tls", false, "use implicit TLS, usually port 465")
	startTLS := fs.Bool("starttls", true, "use STARTTLS, usually port 587")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	payload := map[string]any{
		"enabled":        *enabled,
		"host":           strings.TrimSpace(*host),
		"port":           *port,
		"username":       strings.TrimSpace(*username),
		"password":       strings.TrimSpace(*password),
		"keep_password":  strings.TrimSpace(*password) == "",
		"from":           strings.TrimSpace(*from),
		"to":             strings.TrimSpace(*to),
		"subject_prefix": strings.TrimSpace(*subjectPrefix),
		"use_tls":        *useTLS,
		"start_tls":      *startTLS,
	}
	var out map[string]any
	if err := client.Post(ctx, "/settings/smtp", payload, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		status := "disabled"
		if *enabled {
			status = "enabled"
		}
		fmt.Fprintf(stdout, "smtp notifications %s: %s -> %s\n", status, strings.TrimSpace(*from), strings.TrimSpace(*to))
	})
	return 0
}
