package postflow

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/antoniolg/postflow/internal/domain"
)

func validateInstagramMediaConstraints(media domain.Media) error {
	if !isImageMedia(media) && !isVideoMedia(media) {
		return fmt.Errorf("instagram requires image or video media")
	}
	if isImageMedia(media) && !isInstagramSupportedImage(media) {
		return fmt.Errorf("instagram image media must be JPEG or PNG (.jpg, .jpeg, or .png)")
	}
	if isVideoMedia(media) && !isInstagramSupportedVideo(media) {
		return fmt.Errorf("instagram video media must be MP4 or MOV")
	}
	return nil
}

func isInstagramSupportedImage(media domain.Media) bool {
	mimeType := normalizedMediaMIME(media.MimeType)
	switch mimeType {
	case "image/jpeg", "image/jpg", "image/pjpeg", "image/png":
		return true
	}
	switch mediaExtension(media) {
	case ".jpg", ".jpeg", ".png":
		return true
	default:
		return false
	}
}

func isInstagramSupportedVideo(media domain.Media) bool {
	mimeType := normalizedMediaMIME(media.MimeType)
	switch mimeType {
	case "video/mp4", "video/quicktime":
		return true
	}
	switch mediaExtension(media) {
	case ".mp4", ".mov":
		return true
	default:
		return false
	}
}

func normalizedMediaMIME(raw string) string {
	mimeType := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.Index(mimeType, ";"); i >= 0 {
		mimeType = strings.TrimSpace(mimeType[:i])
	}
	return mimeType
}

func mediaExtension(media domain.Media) string {
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(strings.TrimSpace(media.OriginalName))))
	if ext != "" {
		return ext
	}
	return strings.ToLower(strings.TrimSpace(filepath.Ext(strings.TrimSpace(media.StoragePath))))
}

func resolveInstagramMediaURL(
	media domain.Media,
	credentials Credentials,
	mediaURLKey string,
	builder func(media domain.Media) (string, error),
) (string, error) {
	mediaURLKey = strings.TrimSpace(mediaURLKey)
	if mediaURLKey == "" {
		return "", fmt.Errorf("media url key is required")
	}
	var lastErr error
	tryCandidate := func(source, raw string) string {
		candidate := strings.TrimSpace(raw)
		if candidate == "" {
			return ""
		}
		publicURL, err := normalizeInstagramPublicMediaURL(candidate)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", strings.TrimSpace(source), err)
			return ""
		}
		return publicURL
	}

	if mediaURL := tryCandidate("account credentials "+mediaURLKey, credentials.Extra[mediaURLKey]); mediaURL != "" {
		return mediaURL, nil
	}

	storage := strings.TrimSpace(media.StoragePath)
	lowerStorage := strings.ToLower(storage)
	if strings.HasPrefix(lowerStorage, "http://") || strings.HasPrefix(lowerStorage, "https://") {
		if mediaURL := tryCandidate("media storage path", storage); mediaURL != "" {
			return mediaURL, nil
		}
	}

	if builder != nil {
		builtURL, err := builder(media)
		if err != nil {
			return "", err
		}
		if mediaURL := tryCandidate("media url builder", builtURL); mediaURL != "" {
			return mediaURL, nil
		}
	}

	if lastErr != nil {
		return "", lastErr
	}
	return "", nil
}

func normalizeInstagramPublicMediaURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("media url is empty")
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return "", fmt.Errorf("media url is invalid")
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("media url must use http or https")
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return "", fmt.Errorf("media url host is missing")
	}
	if isNonPublicHost(host) {
		return "", fmt.Errorf("media url host %q is not publicly reachable", host)
	}
	return parsed.String(), nil
}

func isNonPublicHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return true
		}
		return false
	}
	// Hostnames without dot are usually internal service names.
	return !strings.Contains(host, ".")
}

func instagramMediaURLForError(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "<empty>"
	}
	parsed, err := url.Parse(raw)
	if err != nil || strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return "<invalid>"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
