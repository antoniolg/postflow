package postflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

const (
	uploadChunkSize = 5 * 1024 * 1024
)

type XConfig struct {
	APIBaseURL        string
	UploadBaseURL     string
	AuthBaseURL       string
	TokenURL          string
	APIKey            string
	APIKeySecret      string
	ClientID          string
	ClientSecret      string
	AccessToken       string
	AccessTokenSecret string
}

type xAuthMode string

const (
	xAuthModeOAuth1 xAuthMode = "oauth1"
	xAuthModeBearer xAuthMode = "bearer"
)

type XClient struct {
	httpClient  *http.Client
	apiBaseURL  string
	uploadBase  string
	authMode    xAuthMode
	signer      *oauth1Signer
	bearerToken string
}

func NewXClient(cfg XConfig) (*XClient, error) {
	apiBaseURL := strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	if apiBaseURL == "" {
		apiBaseURL = "https://api.x.com"
	}
	uploadBase := strings.TrimRight(strings.TrimSpace(cfg.UploadBaseURL), "/")
	if uploadBase == "" {
		uploadBase = "https://upload.twitter.com"
	}
	accessToken := strings.TrimSpace(cfg.AccessToken)
	accessTokenSecret := strings.TrimSpace(cfg.AccessTokenSecret)
	if accessToken == "" {
		return nil, errors.New("missing X access token")
	}

	client := &XClient{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		apiBaseURL: apiBaseURL,
		uploadBase: uploadBase,
	}
	if accessTokenSecret != "" {
		if strings.TrimSpace(cfg.APIKey) == "" || strings.TrimSpace(cfg.APIKeySecret) == "" {
			return nil, errors.New("missing X oauth1 credentials")
		}
		signer := newOAuth1Signer(oauth1Credentials{
			ConsumerKey:    strings.TrimSpace(cfg.APIKey),
			ConsumerSecret: strings.TrimSpace(cfg.APIKeySecret),
			Token:          accessToken,
			TokenSecret:    accessTokenSecret,
		})
		client.authMode = xAuthModeOAuth1
		client.signer = &signer
		return client, nil
	}

	client.authMode = xAuthModeBearer
	client.bearerToken = accessToken
	return client, nil
}

func (c *XClient) Publish(ctx context.Context, post domain.Post, opts PublishOptions) (string, error) {
	postText := formatPostTextForPublish(post.Text)
	mediaIDs := make([]string, 0, len(post.Media))
	for _, media := range post.Media {
		mediaID, err := c.uploadChunked(ctx, media)
		if err != nil {
			return "", fmt.Errorf("upload media %s: %w", media.ID, err)
		}
		mediaIDs = append(mediaIDs, mediaID)
	}
	id, err := c.createStatus(ctx, postText, mediaIDs, opts)
	if err != nil {
		return "", fmt.Errorf("create post: %w", err)
	}
	return id, nil
}

func (c *XClient) uploadChunked(ctx context.Context, media domain.Media) (string, error) {
	if c.usesOAuth1() {
		return c.uploadChunkedOAuth1(ctx, media)
	}
	return c.uploadChunkedOAuth2(ctx, media)
}

func (c *XClient) createStatus(ctx context.Context, text string, mediaIDs []string, opts PublishOptions) (string, error) {
	payload := map[string]any{
		"text": text,
	}
	if len(mediaIDs) > 0 {
		payload["media"] = map[string]any{
			"media_ids": mediaIDs,
		}
	}
	if strings.TrimSpace(opts.ParentExternalID) != "" && opts.Mode == PublishModeReply {
		payload["reply"] = map[string]any{
			"in_reply_to_tweet_id": strings.TrimSpace(opts.ParentExternalID),
		}
	}
	bodyJSON, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBaseURL+"/2/tweets", bytes.NewReader(bodyJSON))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.authorizeRequest(req); err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("x create tweet failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
		IDStr string `json:"id_str"`
		ID    any    `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.Data.ID != "" {
		return out.Data.ID, nil
	}
	if out.IDStr != "" {
		return out.IDStr, nil
	}
	if out.ID != nil {
		return fmt.Sprintf("%v", out.ID), nil
	}
	return "", errors.New("x create tweet response missing id")
}

func (c *XClient) usesOAuth1() bool {
	return c != nil && c.authMode == xAuthModeOAuth1 && c.signer != nil
}

func (c *XClient) authorizeRequest(req *http.Request) error {
	if c.usesOAuth1() {
		return signRequest(req, *c.signer, nil)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.bearerToken))
	return nil
}

func mediaCategoryFor(m domain.Media) string {
	switch {
	case strings.EqualFold(strings.TrimSpace(m.MimeType), "image/gif"):
		return "tweet_gif"
	case strings.HasPrefix(strings.TrimSpace(m.MimeType), "video/"):
		return "tweet_video"
	default:
		return "tweet_image"
	}
}
