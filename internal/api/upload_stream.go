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

	"github.com/antoniolg/publisher/internal/db"
)

const (
	maxUploadFieldBytes  = 8 << 10
	uploadCopyBufferSize = 256 << 10
)

type mediaUpload struct {
	MediaID      string
	Platform     string
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
	platformSet := false
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
		case "platform":
			platform, readErr := readMultipartField(part, maxUploadFieldBytes)
			_ = part.Close()
			if readErr != nil {
				return mediaUpload{}, http.StatusBadRequest, fmt.Errorf("invalid platform field: %w", readErr)
			}
			if !platformSet {
				upload.Platform = platform
				platformSet = true
			}
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

			out, err = os.Create(upload.StoragePath)
			if err != nil {
				_ = part.Close()
				return mediaUpload{}, http.StatusInternalServerError, err
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

			upload.SizeBytes = size
			upload.MimeType = detectUploadedMimeType(part.Header.Get("Content-Type"), upload.OriginalName)
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

func detectUploadedMimeType(contentType, originalName string) string {
	mimeType := strings.TrimSpace(contentType)
	if mimeType != "" {
		return mimeType
	}
	mimeType = mime.TypeByExtension(filepath.Ext(originalName))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return mimeType
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
