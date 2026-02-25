package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
)

const (
	uploadChunkSize = 5 * 1024 * 1024
)

type XConfig struct {
	APIBaseURL        string
	UploadBaseURL     string
	APIKey            string
	APIKeySecret      string
	AccessToken       string
	AccessTokenSecret string
}

type XClient struct {
	httpClient *http.Client
	apiBaseURL string
	uploadBase string
	signer     oauth1Signer
}

func NewXClient(cfg XConfig) (*XClient, error) {
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = "https://api.twitter.com"
	}
	if cfg.UploadBaseURL == "" {
		cfg.UploadBaseURL = "https://upload.twitter.com"
	}
	if cfg.APIKey == "" || cfg.APIKeySecret == "" || cfg.AccessToken == "" || cfg.AccessTokenSecret == "" {
		return nil, errors.New("missing X credentials")
	}
	return &XClient{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		apiBaseURL: strings.TrimRight(cfg.APIBaseURL, "/"),
		uploadBase: strings.TrimRight(cfg.UploadBaseURL, "/"),
		signer: newOAuth1Signer(oauth1Credentials{
			ConsumerKey:    cfg.APIKey,
			ConsumerSecret: cfg.APIKeySecret,
			Token:          cfg.AccessToken,
			TokenSecret:    cfg.AccessTokenSecret,
		}),
	}, nil
}

func (c *XClient) Publish(ctx context.Context, post domain.Post) (string, error) {
	mediaIDs := make([]string, 0, len(post.Media))
	for _, m := range post.Media {
		mediaID, err := c.uploadChunked(ctx, m)
		if err != nil {
			return "", fmt.Errorf("upload media %s: %w", m.ID, err)
		}
		mediaIDs = append(mediaIDs, mediaID)
	}
	id, err := c.createStatus(ctx, post.Text, mediaIDs)
	if err != nil {
		return "", fmt.Errorf("create post: %w", err)
	}
	return id, nil
}

func (c *XClient) uploadChunked(ctx context.Context, media domain.Media) (string, error) {
	f, err := os.Open(media.StoragePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	mediaCategory := mediaCategoryFor(media)
	initParams := map[string]string{
		"command":        "INIT",
		"total_bytes":    fmt.Sprintf("%d", media.SizeBytes),
		"media_type":     media.MimeType,
		"media_category": mediaCategory,
	}

	initResp, err := c.uploadCommand(ctx, initParams)
	if err != nil {
		return "", err
	}
	mediaID := initResp.MediaIDString
	if mediaID == "" {
		return "", errors.New("x upload INIT returned empty media_id_string")
	}

	buf := make([]byte, uploadChunkSize)
	segment := 0
	for {
		n, readErr := io.ReadFull(f, buf)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return "", readErr
		}
		if n > 0 {
			if err := c.uploadAppend(ctx, mediaID, segment, buf[:n]); err != nil {
				return "", err
			}
			segment++
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
	}

	finalResp, err := c.uploadCommand(ctx, map[string]string{"command": "FINALIZE", "media_id": mediaID})
	if err != nil {
		return "", err
	}
	if err := c.waitForProcessing(ctx, mediaID, finalResp.ProcessingInfo); err != nil {
		return "", err
	}
	return mediaID, nil
}

type uploadResponse struct {
	MediaIDString  string          `json:"media_id_string"`
	ProcessingInfo *processingInfo `json:"processing_info"`
}

type processingInfo struct {
	State         string `json:"state"`
	CheckAfterSec int    `json:"check_after_secs"`
	Error         *struct {
		Code    int    `json:"code"`
		Name    string `json:"name"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *XClient) uploadCommand(ctx context.Context, params map[string]string) (uploadResponse, error) {
	u, err := url.Parse(c.uploadBase + "/1.1/media/upload.json")
	if err != nil {
		return uploadResponse{}, err
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return uploadResponse{}, err
	}
	if err := signRequest(req, c.signer, nil); err != nil {
		return uploadResponse{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return uploadResponse{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return uploadResponse{}, fmt.Errorf("x upload command %s failed: status=%d body=%s", params["command"], resp.StatusCode, string(body))
	}
	var out uploadResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return uploadResponse{}, err
	}
	return out, nil
}

func (c *XClient) uploadAppend(ctx context.Context, mediaID string, segment int, chunk []byte) error {
	u, err := url.Parse(c.uploadBase + "/1.1/media/upload.json")
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("command", "APPEND")
	q.Set("media_id", mediaID)
	q.Set("segment_index", fmt.Sprintf("%d", segment))
	u.RawQuery = q.Encode()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("media", "chunk_"+filepath.Base(mediaID)+".bin")
	if err != nil {
		return err
	}
	if _, err := part.Write(chunk); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if err := signRequest(req, c.signer, nil); err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		return fmt.Errorf("x upload APPEND failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *XClient) waitForProcessing(ctx context.Context, mediaID string, info *processingInfo) error {
	if info == nil {
		return nil
	}
	current := info
	for {
		switch current.State {
		case "succeeded":
			return nil
		case "failed":
			if current.Error != nil {
				return fmt.Errorf("x processing failed: %s (%s)", current.Error.Message, current.Error.Name)
			}
			return errors.New("x processing failed")
		case "pending", "in_progress":
			wait := current.CheckAfterSec
			if wait <= 0 {
				wait = 2
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(wait) * time.Second):
			}
			statusResp, err := c.uploadCommand(ctx, map[string]string{"command": "STATUS", "media_id": mediaID})
			if err != nil {
				return err
			}
			if statusResp.ProcessingInfo == nil {
				return nil
			}
			current = statusResp.ProcessingInfo
		default:
			return fmt.Errorf("unknown processing state: %s", current.State)
		}
	}
}

func (c *XClient) createStatus(ctx context.Context, text string, mediaIDs []string) (string, error) {
	form := url.Values{}
	form.Set("status", text)
	if len(mediaIDs) > 0 {
		form.Set("media_ids", strings.Join(mediaIDs, ","))
	}

	endpoint := c.apiBaseURL + "/1.1/statuses/update.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	sigParams := map[string]string{}
	for k, v := range form {
		if len(v) > 0 {
			sigParams[k] = v[0]
		}
	}
	if err := signRequest(req, c.signer, sigParams); err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("x create status failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	var out struct {
		IDStr string `json:"id_str"`
		ID    any    `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.IDStr != "" {
		return out.IDStr, nil
	}
	if out.ID != nil {
		return fmt.Sprintf("%v", out.ID), nil
	}
	return "", errors.New("x create status response missing id")
}

func mediaCategoryFor(m domain.Media) string {
	if strings.HasPrefix(m.MimeType, "video/") {
		return "tweet_video"
	}
	return "tweet_image"
}
