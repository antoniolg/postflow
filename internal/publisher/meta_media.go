package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/antoniolg/publisher/internal/domain"
)

func (p *FacebookProvider) uploadFacebookPhoto(ctx context.Context, pageID, accessToken string, media domain.Media) (string, error) {
	storage := strings.TrimSpace(media.StoragePath)
	if storage == "" {
		return "", fmt.Errorf("facebook media %s has empty storage path", strings.TrimSpace(media.ID))
	}
	lowerStorage := strings.ToLower(storage)
	var req *http.Request
	var err error
	endpoint := fmt.Sprintf("%s/%s/%s/photos", strings.TrimRight(p.cfg.GraphURL, "/"), p.cfg.APIVersion, strings.TrimSpace(pageID))
	if strings.HasPrefix(lowerStorage, "http://") || strings.HasPrefix(lowerStorage, "https://") {
		values := url.Values{}
		values.Set("url", storage)
		values.Set("published", "false")
		values.Set("access_token", strings.TrimSpace(accessToken))
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		file, openErr := os.Open(storage)
		if openErr != nil {
			return "", openErr
		}
		defer file.Close()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("published", "false")
		_ = writer.WriteField("access_token", strings.TrimSpace(accessToken))
		part, createErr := writer.CreateFormFile("source", firstNonEmpty(strings.TrimSpace(media.OriginalName), filepath.Base(storage)))
		if createErr != nil {
			return "", createErr
		}
		if _, copyErr := io.Copy(part, file); copyErr != nil {
			return "", copyErr
		}
		if closeErr := writer.Close(); closeErr != nil {
			return "", closeErr
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body.Bytes()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", writer.FormDataContentType())
		if mimeType := strings.TrimSpace(media.MimeType); mimeType != "" {
			req.Header.Set("Accept", mimeType)
		}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("facebook photo upload failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.ID) == "" {
		return "", fmt.Errorf("facebook photo upload response missing id")
	}
	return strings.TrimSpace(out.ID), nil
}

func (p *FacebookProvider) uploadFacebookVideo(ctx context.Context, pageID, accessToken, message string, media domain.Media) (string, error) {
	storage := strings.TrimSpace(media.StoragePath)
	if storage == "" {
		return "", fmt.Errorf("facebook video media %s has empty storage path", strings.TrimSpace(media.ID))
	}
	lowerStorage := strings.ToLower(storage)
	var req *http.Request
	var err error
	endpoint := fmt.Sprintf("%s/%s/%s/videos", strings.TrimRight(p.cfg.GraphURL, "/"), p.cfg.APIVersion, strings.TrimSpace(pageID))
	if strings.HasPrefix(lowerStorage, "http://") || strings.HasPrefix(lowerStorage, "https://") {
		values := url.Values{}
		values.Set("file_url", storage)
		if strings.TrimSpace(message) != "" {
			values.Set("description", strings.TrimSpace(message))
		}
		values.Set("access_token", strings.TrimSpace(accessToken))
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		file, openErr := os.Open(storage)
		if openErr != nil {
			return "", openErr
		}
		defer file.Close()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("access_token", strings.TrimSpace(accessToken))
		if strings.TrimSpace(message) != "" {
			_ = writer.WriteField("description", strings.TrimSpace(message))
		}
		part, createErr := writer.CreateFormFile("source", firstNonEmpty(strings.TrimSpace(media.OriginalName), filepath.Base(storage)))
		if createErr != nil {
			return "", createErr
		}
		if _, copyErr := io.Copy(part, file); copyErr != nil {
			return "", copyErr
		}
		if closeErr := writer.Close(); closeErr != nil {
			return "", closeErr
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body.Bytes()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", writer.FormDataContentType())
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("facebook video upload failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.ID) == "" {
		return "", fmt.Errorf("facebook video upload response missing id")
	}
	return strings.TrimSpace(out.ID), nil
}

func isImageMedia(media domain.Media) bool {
	mimeType := strings.ToLower(strings.TrimSpace(media.MimeType))
	if strings.HasPrefix(mimeType, "image/") {
		return true
	}
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(media.OriginalName)))
	if ext == "" {
		ext = strings.ToLower(strings.TrimSpace(filepath.Ext(media.StoragePath)))
	}
	if ext == "" {
		return false
	}
	detected := mime.TypeByExtension(ext)
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(detected)), "image/")
}

func isVideoMedia(media domain.Media) bool {
	mimeType := strings.ToLower(strings.TrimSpace(media.MimeType))
	if strings.HasPrefix(mimeType, "video/") {
		return true
	}
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(media.OriginalName)))
	if ext == "" {
		ext = strings.ToLower(strings.TrimSpace(filepath.Ext(media.StoragePath)))
	}
	if ext == "" {
		return false
	}
	detected := mime.TypeByExtension(ext)
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(detected)), "video/")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func marshalBody(v any) io.Reader {
	raw, _ := json.Marshal(v)
	return bytes.NewReader(raw)
}
