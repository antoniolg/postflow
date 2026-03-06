package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/antoniolg/postflow/internal/db"
)

const (
	maxUploadFieldBytes  = 8 << 10
	uploadCopyBufferSize = 256 << 10
)

type mediaUpload struct {
	MediaID      string
	Kind         string
	OriginalName string
	StoragePath  string
	MimeType     string
	SizeBytes    int64
}

func (s Server) saveUploadToDisk(r *http.Request) (_ mediaUpload, status int, err error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return mediaUpload{}, http.StatusBadRequest, fmt.Errorf("invalid multipart form: %w", err)
	}

	storageDir := filepath.Join(s.DataDir, "media")
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		return mediaUpload{}, http.StatusInternalServerError, err
	}

	mediaID, err := db.NewID("med")
	if err != nil {
		return mediaUpload{}, http.StatusInternalServerError, err
	}
	upload := mediaUpload{MediaID: mediaID}
	kindSet := false

	var out *os.File
	defer func() {
		if out != nil {
			_ = out.Close()
		}
		if err != nil {
			_ = removeFileQuiet(upload.StoragePath)
		}
	}()

	copyBuffer := make([]byte, uploadCopyBufferSize)
	for {
		part, nextErr := mr.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return mediaUpload{}, http.StatusBadRequest, fmt.Errorf("invalid multipart form: %w", nextErr)
		}

		switch part.FormName() {
		case "kind":
			kind, readErr := readMultipartField(part, maxUploadFieldBytes)
			_ = part.Close()
			if readErr != nil {
				return mediaUpload{}, http.StatusBadRequest, fmt.Errorf("invalid kind field: %w", readErr)
			}
			if !kindSet {
				upload.Kind = kind
				kindSet = true
			}
		case "file":
			if upload.StoragePath != "" {
				_, _ = io.Copy(io.Discard, part)
				_ = part.Close()
				continue
			}

			upload.OriginalName = part.FileName()
			name := sanitizeName(upload.OriginalName)
			if name == "" {
				name = "upload.bin"
			}
			upload.StoragePath = filepath.Join(storageDir, mediaID+"_"+name)

			sniffed, readErr := readPartPrefix(part, 512)
			if readErr != nil {
				_ = part.Close()
				return mediaUpload{}, http.StatusInternalServerError, readErr
			}
			out, err = os.Create(upload.StoragePath)
			if err != nil {
				_ = part.Close()
				return mediaUpload{}, http.StatusInternalServerError, err
			}
			if len(sniffed) > 0 {
				if _, err := out.Write(sniffed); err != nil {
					_ = part.Close()
					return mediaUpload{}, http.StatusInternalServerError, err
				}
			}
			size, copyErr := io.CopyBuffer(out, part, copyBuffer)
			_ = part.Close()
			if copyErr != nil {
				return mediaUpload{}, http.StatusInternalServerError, copyErr
			}
			if closeErr := out.Close(); closeErr != nil {
				return mediaUpload{}, http.StatusInternalServerError, closeErr
			}
			out = nil

			upload.SizeBytes = int64(len(sniffed)) + size
			upload.MimeType = detectUploadedMimeType(part.Header.Get("Content-Type"), upload.OriginalName, sniffed)
		default:
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
		}
	}

	if upload.StoragePath == "" {
		return mediaUpload{}, http.StatusBadRequest, errors.New("missing file field")
	}

	return upload, http.StatusOK, nil
}

func readMultipartField(part *multipart.Part, maxBytes int64) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(part, maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(raw)) > maxBytes {
		return "", fmt.Errorf("value exceeds %d bytes", maxBytes)
	}
	return strings.TrimSpace(string(raw)), nil
}

func readPartPrefix(part *multipart.Part, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, nil
	}
	buf := make([]byte, maxBytes)
	n, err := io.ReadFull(part, buf)
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), err == nil:
		return buf[:n], nil
	default:
		return nil, err
	}
}

func detectUploadedMimeType(contentType, originalName string, content []byte) string {
	mimeType := strings.TrimSpace(contentType)
	if !isGenericUploadMimeType(mimeType) && mimeType != "" {
		return mimeType
	}
	if extMimeType := strings.TrimSpace(mime.TypeByExtension(strings.ToLower(filepath.Ext(originalName)))); extMimeType != "" {
		return extMimeType
	}
	if len(content) > 0 {
		if sniffed := strings.TrimSpace(http.DetectContentType(content)); sniffed != "" {
			return sniffed
		}
	}
	return "application/octet-stream"
}

func isGenericUploadMimeType(raw string) bool {
	mimeType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		mimeType = strings.TrimSpace(raw)
		if i := strings.Index(mimeType, ";"); i >= 0 {
			mimeType = strings.TrimSpace(mimeType[:i])
		}
	}
	return strings.EqualFold(mimeType, "application/octet-stream")
}

func removeFileQuiet(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
