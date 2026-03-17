package postflow

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
	"strconv"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

type xUploadResponse struct {
	MediaIDString  string          `json:"media_id_string"`
	ProcessingInfo *processingInfo `json:"processing_info"`
	Data           struct {
		ID             string          `json:"id"`
		ProcessingInfo *processingInfo `json:"processing_info"`
	} `json:"data"`
}

func (r xUploadResponse) mediaID() string {
	return firstNonEmpty(strings.TrimSpace(r.Data.ID), strings.TrimSpace(r.MediaIDString))
}

func (r xUploadResponse) processing() *processingInfo {
	if r.Data.ProcessingInfo != nil {
		return r.Data.ProcessingInfo
	}
	return r.ProcessingInfo
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

func (c *XClient) uploadChunkedOAuth2(ctx context.Context, media domain.Media) (string, error) {
	mediaID, err := c.uploadInitializeOAuth2(ctx, media)
	if err != nil {
		return "", err
	}
	if err := c.uploadAppendOAuth2(ctx, mediaID, media); err != nil {
		return "", err
	}
	finalResp, err := c.uploadFinalizeOAuth2(ctx, mediaID)
	if err != nil {
		return "", err
	}
	if err := waitForXProcessing(ctx, finalResp.processing(), func(ctx context.Context) (*processingInfo, error) {
		statusResp, err := c.uploadStatusOAuth2(ctx, mediaID)
		if err != nil {
			return nil, err
		}
		return statusResp.processing(), nil
	}); err != nil {
		return "", err
	}
	return mediaID, nil
}

func (c *XClient) uploadInitializeOAuth2(ctx context.Context, media domain.Media) (string, error) {
	payload := map[string]any{
		"media_type":     strings.TrimSpace(media.MimeType),
		"total_bytes":    media.SizeBytes,
		"media_category": mediaCategoryFor(media),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBaseURL+"/2/media/upload/initialize", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorizeRequest(req)
	resp, err := c.doUploadRequest(req, "x upload initialize failed")
	if err != nil {
		return "", err
	}
	mediaID := resp.mediaID()
	if mediaID == "" {
		return "", errors.New("x upload initialize returned empty media id")
	}
	return mediaID, nil
}

func (c *XClient) uploadAppendOAuth2(ctx context.Context, mediaID string, media domain.Media) error {
	f, err := os.Open(media.StoragePath)
	if err != nil {
		return err
	}
	defer f.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("segment_index", "0"); err != nil {
		return err
	}
	part, err := mw.CreateFormFile("media", filepath.Base(firstNonEmpty(strings.TrimSpace(media.OriginalName), media.StoragePath, mediaID+".bin")))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, f); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}

	appendURL := fmt.Sprintf("%s/2/media/upload/%s/append", c.apiBaseURL, url.PathEscape(mediaID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, appendURL, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	c.authorizeRequest(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		return fmt.Errorf("x upload append failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *XClient) uploadFinalizeOAuth2(ctx context.Context, mediaID string) (xUploadResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/2/media/upload/%s/finalize", c.apiBaseURL, url.PathEscape(mediaID)), nil)
	if err != nil {
		return xUploadResponse{}, err
	}
	c.authorizeRequest(req)
	return c.doUploadRequest(req, "x upload finalize failed")
}

func (c *XClient) uploadStatusOAuth2(ctx context.Context, mediaID string) (xUploadResponse, error) {
	values := url.Values{}
	values.Set("command", "STATUS")
	values.Set("media_id", mediaID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBaseURL+"/2/media/upload?"+values.Encode(), nil)
	if err != nil {
		return xUploadResponse{}, err
	}
	c.authorizeRequest(req)
	return c.doUploadRequest(req, "x upload status failed")
}

func (c *XClient) doUploadRequest(req *http.Request, errPrefix string) (xUploadResponse, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return xUploadResponse{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return xUploadResponse{}, fmt.Errorf("%s: status=%d body=%s", errPrefix, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return xUploadResponse{}, nil
	}
	var out xUploadResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return xUploadResponse{}, err
	}
	return out, nil
}

func waitForXProcessing(ctx context.Context, info *processingInfo, poll func(context.Context) (*processingInfo, error)) error {
	if info == nil {
		return nil
	}
	current := info
	for {
		switch strings.TrimSpace(current.State) {
		case "succeeded":
			return nil
		case "failed":
			if current.Error != nil {
				return fmt.Errorf("x processing failed: %s (%s)", current.Error.Message, current.Error.Name)
			}
			return errors.New("x processing failed")
		case "pending", "in_progress":
			waitSeconds := current.CheckAfterSec
			if waitSeconds <= 0 {
				waitSeconds = 2
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(waitSeconds) * time.Second):
			}
			next, err := poll(ctx)
			if err != nil {
				return err
			}
			if next == nil {
				return nil
			}
			current = next
		default:
			return fmt.Errorf("unknown processing state: %s", current.State)
		}
	}
}

func parseXUploadSegment(value string) int {
	segment, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || segment < 0 {
		return 0
	}
	return segment
}
