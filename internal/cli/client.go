package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type APIClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewAPIClient(baseURL, token string, timeout time.Duration) *APIClient {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	baseURL = strings.TrimSpace(baseURL)
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	return &APIClient{
		baseURL: baseURL,
		token:   strings.TrimSpace(token),
		client:  &http.Client{Timeout: timeout},
	}
}

func (c *APIClient) Get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out, nil)
}

func (c *APIClient) Post(ctx context.Context, path string, in any, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, in, out, nil)
}

func (c *APIClient) PostWithHeaders(ctx context.Context, path string, in any, out any, headers map[string]string) error {
	return c.do(ctx, http.MethodPost, path, nil, in, out, headers)
}

func (c *APIClient) Delete(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodDelete, path, nil, nil, out, nil)
}

func (c *APIClient) PostMultipartFile(ctx context.Context, path, fileField, filePath string, fields map[string]string, out any) error {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return fmt.Errorf("file path is required")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}

	mimeType, err := detectUploadFileMimeType(file, filePath)
	if err != nil {
		_ = file.Close()
		return err
	}

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	contentType := writer.FormDataContentType()
	go func() {
		err := writeMultipartFile(writer, file, fileField, filepath.Base(filePath), mimeType, fields)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, pr)
	if err != nil {
		_ = pr.Close()
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	return decodeResponse(http.MethodPost, path, resp.StatusCode, respBody, out)
}

func writeMultipartFile(writer *multipart.Writer, file *os.File, fileField, fileName, mimeType string, fields map[string]string) error {
	defer file.Close()
	for key, value := range fields {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if err := writer.WriteField(key, strings.TrimSpace(value)); err != nil {
			return fmt.Errorf("write multipart field %q: %w", key, err)
		}
	}
	part, err := createMultipartFilePart(writer, fileField, fileName, mimeType)
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("copy form file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}
	return nil
}

func createMultipartFilePart(writer *multipart.Writer, fileField, fileName, mimeType string) (io.Writer, error) {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", multipartFormDataContentDisposition(fileField, fileName))
	header.Set("Content-Type", strings.TrimSpace(mimeType))
	return writer.CreatePart(header)
}

func multipartFormDataContentDisposition(fieldName, fileName string) string {
	value := mime.FormatMediaType("form-data", map[string]string{
		"name":     strings.TrimSpace(fieldName),
		"filename": strings.TrimSpace(fileName),
	})
	if strings.TrimSpace(value) != "" {
		return value
	}
	escape := strings.NewReplacer("\\", "\\\\", `"`, "\\\"")
	return fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escape.Replace(strings.TrimSpace(fieldName)), escape.Replace(strings.TrimSpace(fileName)))
}

func detectUploadFileMimeType(file *os.File, filePath string) (string, error) {
	var sniff [512]byte
	n, err := file.Read(sniff[:])
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("detect file mime: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewind file after mime detection: %w", err)
	}
	if n > 0 {
		mimeType := strings.TrimSpace(http.DetectContentType(sniff[:n]))
		if !isGenericMIMEType(mimeType) {
			return mimeType, nil
		}
	}
	if mimeType := strings.TrimSpace(mime.TypeByExtension(strings.ToLower(filepath.Ext(filePath)))); mimeType != "" {
		return mimeType, nil
	}
	return "application/octet-stream", nil
}

func isGenericMIMEType(raw string) bool {
	mimeType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		mimeType = strings.TrimSpace(raw)
		if i := strings.Index(mimeType, ";"); i >= 0 {
			mimeType = strings.TrimSpace(mimeType[:i])
		}
	}
	return strings.EqualFold(mimeType, "application/octet-stream")
}

func (c *APIClient) do(ctx context.Context, method, path string, query url.Values, in any, out any, headers map[string]string) error {
	var body io.Reader
	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	fullURL := c.baseURL + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for key, value := range headers {
		if strings.TrimSpace(key) == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	return decodeResponse(method, path, resp.StatusCode, respBody, out)
}

func extractErrorMessage(raw []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Error)
}

func decodeResponse(method, path string, statusCode int, raw []byte, out any) error {
	if statusCode < 200 || statusCode > 299 {
		msg := extractErrorMessage(raw)
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		if msg == "" {
			msg = http.StatusText(statusCode)
		}
		return fmt.Errorf("%s %s: status %d: %s", method, path, statusCode, msg)
	}

	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}
