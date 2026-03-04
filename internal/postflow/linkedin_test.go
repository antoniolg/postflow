package postflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestLinkedInPublishUploadsImageAsset(t *testing.T) {
	var uploadedBytes int
	var sawImageCategory bool
	var baseURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/assets" && r.URL.Query().Get("action") == "registerUpload":
			_, _ = w.Write([]byte(`{
				"value": {
					"asset": "urn:li:digitalmediaAsset:123",
					"uploadMechanism": {
						"com.linkedin.digitalmedia.uploading.MediaUploadHttpRequest": {
							"uploadUrl": "` + baseURL + `/upload"
						}
					}
				}
			}`))
		case r.URL.Path == "/upload":
			body, _ := io.ReadAll(r.Body)
			uploadedBytes = len(body)
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/v2/ugcPosts":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			sc, _ := payload["specificContent"].(map[string]any)
			ugc, _ := sc["com.linkedin.ugc.ShareContent"].(map[string]any)
			if category, _ := ugc["shareMediaCategory"].(string); strings.TrimSpace(category) == "IMAGE" {
				sawImageCategory = true
			}
			w.Header().Set("x-restli-id", "li_post_1")
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	baseURL = server.URL

	imagePath := filepath.Join(t.TempDir(), "upload.jpg")
	if err := os.WriteFile(imagePath, []byte("image-binary"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	provider := NewLinkedInProvider(LinkedInProviderConfig{
		APIBaseURL: server.URL,
	})
	externalID, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}, Credentials{
		AccessToken: "token-1",
	}, domain.Post{
		Text: "hello linkedin",
		Media: []domain.Media{{
			ID:           "med_li_1",
			Kind:         "image",
			OriginalName: "upload.jpg",
			StoragePath:  imagePath,
			MimeType:     "image/jpeg",
		}},
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if externalID != "li_post_1" {
		t.Fatalf("unexpected external id %q", externalID)
	}
	if uploadedBytes == 0 {
		t.Fatalf("expected upload body to be sent")
	}
	if !sawImageCategory {
		t.Fatalf("expected shareMediaCategory IMAGE")
	}
}

func TestLinkedInPublishUploadsVideoAsset(t *testing.T) {
	var uploadedBytes int
	var sawVideoCategory bool
	var sawVideoRecipe bool
	var baseURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/assets" && r.URL.Query().Get("action") == "registerUpload":
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "feedshare-video") {
				sawVideoRecipe = true
			}
			_, _ = w.Write([]byte(`{
				"value": {
					"asset": "urn:li:digitalmediaAsset:video123",
					"uploadMechanism": {
						"com.linkedin.digitalmedia.uploading.MediaUploadHttpRequest": {
							"uploadUrl": "` + baseURL + `/upload-video"
						}
					}
				}
			}`))
		case r.URL.Path == "/upload-video":
			body, _ := io.ReadAll(r.Body)
			uploadedBytes = len(body)
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/v2/ugcPosts":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			sc, _ := payload["specificContent"].(map[string]any)
			ugc, _ := sc["com.linkedin.ugc.ShareContent"].(map[string]any)
			if category, _ := ugc["shareMediaCategory"].(string); strings.TrimSpace(category) == "VIDEO" {
				sawVideoCategory = true
			}
			w.Header().Set("x-restli-id", "li_post_video_1")
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	baseURL = server.URL

	videoPath := filepath.Join(t.TempDir(), "upload.mp4")
	if err := os.WriteFile(videoPath, []byte("video-binary"), 0o644); err != nil {
		t.Fatalf("write video: %v", err)
	}

	provider := NewLinkedInProvider(LinkedInProviderConfig{
		APIBaseURL: server.URL,
	})
	externalID, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}, Credentials{
		AccessToken: "token-1",
	}, domain.Post{
		Text: "hello linkedin video",
		Media: []domain.Media{{
			ID:           "med_li_v_1",
			Kind:         "video",
			OriginalName: "upload.mp4",
			StoragePath:  videoPath,
			MimeType:     "video/mp4",
		}},
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if externalID != "li_post_video_1" {
		t.Fatalf("unexpected external id %q", externalID)
	}
	if uploadedBytes == 0 {
		t.Fatalf("expected upload body to be sent")
	}
	if !sawVideoRecipe {
		t.Fatalf("expected register upload to use feedshare-video recipe")
	}
	if !sawVideoCategory {
		t.Fatalf("expected shareMediaCategory VIDEO")
	}
}
