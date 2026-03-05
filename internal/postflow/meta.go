package postflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

type MetaProviderConfig struct {
	AppID                 string
	AppSecret             string
	GraphURL              string
	DialogURL             string
	APIVersion            string
	MediaURLBuilder       func(media domain.Media) (string, error)
	ContainerPollInterval time.Duration
	ContainerReadyTimeout time.Duration
}

type FacebookProvider struct {
	cfg    MetaProviderConfig
	client *http.Client
}

type InstagramProvider struct {
	cfg    MetaProviderConfig
	client *http.Client
}

func normalizeMetaConfig(cfg MetaProviderConfig) MetaProviderConfig {
	if strings.TrimSpace(cfg.GraphURL) == "" {
		cfg.GraphURL = "https://graph.facebook.com"
	}
	if strings.TrimSpace(cfg.DialogURL) == "" {
		cfg.DialogURL = "https://www.facebook.com"
	}
	if strings.TrimSpace(cfg.APIVersion) == "" {
		cfg.APIVersion = "v22.0"
	}
	if cfg.ContainerPollInterval <= 0 {
		cfg.ContainerPollInterval = 5 * time.Second
	}
	if cfg.ContainerReadyTimeout <= 0 {
		cfg.ContainerReadyTimeout = 10 * time.Minute
	}
	return cfg
}

func NewFacebookProvider(cfg MetaProviderConfig) *FacebookProvider {
	cfg = normalizeMetaConfig(cfg)
	return &FacebookProvider{cfg: cfg, client: &http.Client{Timeout: 60 * time.Second}}
}

func NewInstagramProvider(cfg MetaProviderConfig) *InstagramProvider {
	cfg = normalizeMetaConfig(cfg)
	return &InstagramProvider{cfg: cfg, client: &http.Client{Timeout: 60 * time.Second}}
}

func (p *FacebookProvider) Platform() domain.Platform {
	return domain.PlatformFacebook
}

func (p *InstagramProvider) Platform() domain.Platform {
	return domain.PlatformInstagram
}

func (p *FacebookProvider) ValidateDraft(_ context.Context, _ domain.SocialAccount, draft Draft) ([]string, error) {
	if len(draft.Media) > 10 {
		return nil, fmt.Errorf("facebook supports up to 10 media attachments per post")
	}
	imageCount := 0
	videoCount := 0
	for _, item := range draft.Media {
		if isImageMedia(item) {
			imageCount++
			continue
		}
		if isVideoMedia(item) {
			videoCount++
			continue
		}
		return nil, fmt.Errorf("facebook requires image or video media")
	}
	if videoCount > 1 {
		return nil, fmt.Errorf("facebook supports a single video per post in this release")
	}
	if videoCount > 0 && imageCount > 0 {
		return nil, fmt.Errorf("facebook does not support mixing images and video in this release")
	}
	return nil, nil
}

func (p *InstagramProvider) ValidateDraft(_ context.Context, _ domain.SocialAccount, draft Draft) ([]string, error) {
	if len(draft.Media) == 0 {
		return nil, fmt.Errorf("instagram publish requires one media item")
	}
	if len(draft.Media) > 1 {
		return nil, fmt.Errorf("instagram supports a single image or video per post in this release")
	}
	if err := validateInstagramMediaConstraints(draft.Media[0]); err != nil {
		return nil, err
	}
	return nil, nil
}

func (p *FacebookProvider) Publish(ctx context.Context, account domain.SocialAccount, credentials Credentials, post domain.Post, opts PublishOptions) (string, error) {
	postText := formatPostTextForPublish(post.Text)
	pageID := strings.TrimSpace(account.ExternalAccountID)
	if pageID == "" {
		pageID = strings.TrimSpace(credentials.Extra["page_id"])
	}
	if pageID == "" {
		return "", fmt.Errorf("facebook page id is required")
	}
	if strings.TrimSpace(credentials.AccessToken) == "" {
		return "", fmt.Errorf("facebook access token missing")
	}
	if opts.Mode == PublishModeComment {
		if len(post.Media) > 0 {
			return "", fmt.Errorf("facebook thread comments do not support media in this release")
		}
		parentExternalID := strings.TrimSpace(opts.ParentExternalID)
		if parentExternalID == "" {
			return "", fmt.Errorf("facebook parent external id is required for comment mode")
		}
		values := url.Values{}
		values.Set("message", strings.TrimSpace(postText))
		values.Set("access_token", strings.TrimSpace(credentials.AccessToken))
		reqURL := fmt.Sprintf("%s/%s/%s/comments", strings.TrimRight(p.cfg.GraphURL, "/"), p.cfg.APIVersion, parentExternalID)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(values.Encode()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := p.client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		if resp.StatusCode >= 300 {
			return "", fmt.Errorf("facebook comment failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return "", err
		}
		if strings.TrimSpace(out.ID) == "" {
			return "", fmt.Errorf("facebook comment response missing id")
		}
		return strings.TrimSpace(out.ID), nil
	}
	if len(post.Media) > 10 {
		return "", fmt.Errorf("facebook supports up to 10 media attachments per post")
	}
	imageCount := 0
	videoCount := 0
	for _, item := range post.Media {
		if isImageMedia(item) {
			imageCount++
			continue
		}
		if isVideoMedia(item) {
			videoCount++
			continue
		}
		return "", fmt.Errorf("facebook requires image or video media")
	}
	if videoCount > 1 {
		return "", fmt.Errorf("facebook supports a single video per post in this release")
	}
	if videoCount > 0 && imageCount > 0 {
		return "", fmt.Errorf("facebook does not support mixing images and video in this release")
	}
	if videoCount == 1 {
		for _, media := range post.Media {
			if !isVideoMedia(media) {
				continue
			}
			videoID, err := p.uploadFacebookVideo(ctx, pageID, strings.TrimSpace(credentials.AccessToken), strings.TrimSpace(postText), media)
			if err != nil {
				return "", err
			}
			return videoID, nil
		}
	}
	attachmentIDs := make([]string, 0, len(post.Media))
	for _, media := range post.Media {
		if !isImageMedia(media) {
			return "", fmt.Errorf("facebook requires image media for multi-attachment posts")
		}
		photoID, err := p.uploadFacebookPhoto(ctx, pageID, strings.TrimSpace(credentials.AccessToken), media)
		if err != nil {
			return "", err
		}
		attachmentIDs = append(attachmentIDs, photoID)
	}
	values := url.Values{}
	values.Set("message", strings.TrimSpace(postText))
	values.Set("access_token", strings.TrimSpace(credentials.AccessToken))
	for i, photoID := range attachmentIDs {
		values.Set(fmt.Sprintf("attached_media[%d]", i), fmt.Sprintf(`{"media_fbid":"%s"}`, strings.TrimSpace(photoID)))
	}
	reqURL := fmt.Sprintf("%s/%s/%s/feed", strings.TrimRight(p.cfg.GraphURL, "/"), p.cfg.APIVersion, pageID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("facebook publish failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.ID) == "" {
		return "", fmt.Errorf("facebook publish response missing id")
	}
	return strings.TrimSpace(out.ID), nil
}

func (p *InstagramProvider) Publish(ctx context.Context, account domain.SocialAccount, credentials Credentials, post domain.Post, opts PublishOptions) (string, error) {
	postText := formatPostTextForPublish(post.Text)
	igUserID := strings.TrimSpace(account.ExternalAccountID)
	if igUserID == "" {
		igUserID = strings.TrimSpace(credentials.Extra["ig_user_id"])
	}
	if igUserID == "" {
		return "", fmt.Errorf("instagram user id is required")
	}
	if strings.TrimSpace(credentials.AccessToken) == "" {
		return "", fmt.Errorf("instagram access token missing")
	}
	if opts.Mode == PublishModeComment {
		if len(post.Media) > 0 {
			return "", fmt.Errorf("instagram thread comments do not support media in this release")
		}
		parentExternalID := strings.TrimSpace(opts.ParentExternalID)
		if parentExternalID == "" {
			return "", fmt.Errorf("instagram parent external id is required for comment mode")
		}
		values := url.Values{}
		values.Set("message", strings.TrimSpace(postText))
		values.Set("access_token", strings.TrimSpace(credentials.AccessToken))
		commentURL := fmt.Sprintf("%s/%s/%s/comments", strings.TrimRight(p.cfg.GraphURL, "/"), p.cfg.APIVersion, parentExternalID)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, commentURL, strings.NewReader(values.Encode()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := p.client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		if resp.StatusCode >= 300 {
			return "", fmt.Errorf("instagram comment failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return "", err
		}
		if strings.TrimSpace(out.ID) == "" {
			return "", fmt.Errorf("instagram comment response missing id")
		}
		return strings.TrimSpace(out.ID), nil
	}
	if len(post.Media) == 0 {
		return "", fmt.Errorf("instagram requires at least one media item")
	}
	if len(post.Media) > 1 {
		return "", fmt.Errorf("instagram supports a single image or video per post in this release")
	}
	if err := validateInstagramMediaConstraints(post.Media[0]); err != nil {
		return "", err
	}
	isVideo := isVideoMedia(post.Media[0])
	mediaURLKey := "image_url"
	mediaLabel := "image"
	if isVideo {
		mediaURLKey = "video_url"
		mediaLabel = "video"
	}
	mediaURL, err := resolveInstagramMediaURL(post.Media[0], credentials, mediaURLKey, p.cfg.MediaURLBuilder)
	if err != nil {
		return "", fmt.Errorf("instagram requires a public %s URL: %w", mediaLabel, err)
	}
	if mediaURL == "" {
		return "", fmt.Errorf("instagram requires a public %s URL for media %s", mediaLabel, post.Media[0].ID)
	}
	createValues := url.Values{}
	createValues.Set(mediaURLKey, mediaURL)
	if isVideo {
		createValues.Set("media_type", "REELS")
	}
	createValues.Set("caption", strings.TrimSpace(postText))
	createValues.Set("access_token", strings.TrimSpace(credentials.AccessToken))
	createURL := fmt.Sprintf("%s/%s/%s/media", strings.TrimRight(p.cfg.GraphURL, "/"), p.cfg.APIVersion, igUserID)
	createReq, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, strings.NewReader(createValues.Encode()))
	if err != nil {
		return "", err
	}
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createResp, err := p.client.Do(createReq)
	if err != nil {
		return "", err
	}
	defer createResp.Body.Close()
	createBody, _ := io.ReadAll(io.LimitReader(createResp.Body, 2<<20))
	if createResp.StatusCode >= 300 {
		return "", fmt.Errorf(
			"instagram create media failed: status=%d media_url=%s body=%s",
			createResp.StatusCode,
			instagramMediaURLForError(mediaURL),
			strings.TrimSpace(string(createBody)),
		)
	}
	var container struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(createBody, &container); err != nil {
		return "", err
	}
	if strings.TrimSpace(container.ID) == "" {
		return "", fmt.Errorf("instagram create media missing container id")
	}
	if isVideo {
		if err := p.waitForInstagramContainerReady(ctx, strings.TrimSpace(container.ID), strings.TrimSpace(credentials.AccessToken)); err != nil {
			return "", err
		}
	}

	publishValues := url.Values{}
	publishValues.Set("creation_id", strings.TrimSpace(container.ID))
	publishValues.Set("access_token", strings.TrimSpace(credentials.AccessToken))
	publishURL := fmt.Sprintf("%s/%s/%s/media_publish", strings.TrimRight(p.cfg.GraphURL, "/"), p.cfg.APIVersion, igUserID)
	publishReq, err := http.NewRequestWithContext(ctx, http.MethodPost, publishURL, strings.NewReader(publishValues.Encode()))
	if err != nil {
		return "", err
	}
	publishReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	publishResp, err := p.client.Do(publishReq)
	if err != nil {
		return "", err
	}
	defer publishResp.Body.Close()
	publishBody, _ := io.ReadAll(io.LimitReader(publishResp.Body, 2<<20))
	if publishResp.StatusCode >= 300 {
		return "", fmt.Errorf("instagram publish failed: status=%d body=%s", publishResp.StatusCode, strings.TrimSpace(string(publishBody)))
	}
	var publishOut struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(publishBody, &publishOut); err != nil {
		return "", err
	}
	if strings.TrimSpace(publishOut.ID) == "" {
		return "", fmt.Errorf("instagram publish response missing id")
	}
	return strings.TrimSpace(publishOut.ID), nil
}

func (p *InstagramProvider) waitForInstagramContainerReady(ctx context.Context, containerID, accessToken string) error {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return fmt.Errorf("instagram container id is required")
	}
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return fmt.Errorf("instagram access token missing")
	}
	deadline := time.Now().UTC().Add(p.cfg.ContainerReadyTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		values := url.Values{}
		values.Set("fields", "status_code,status")
		values.Set("access_token", accessToken)
		statusURL := fmt.Sprintf("%s/%s/%s?%s", strings.TrimRight(p.cfg.GraphURL, "/"), p.cfg.APIVersion, containerID, values.Encode())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
		if err != nil {
			return err
		}
		resp, err := p.client.Do(req)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("instagram container status failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var out struct {
			StatusCode string `json:"status_code"`
			Status     string `json:"status"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return err
		}
		statusCode := strings.ToUpper(strings.TrimSpace(out.StatusCode))
		status := strings.ToUpper(strings.TrimSpace(out.Status))
		switch {
		case statusCode == "FINISHED":
			return nil
		case statusCode == "ERROR" || statusCode == "EXPIRED" || status == "ERROR" || status == "EXPIRED":
			return fmt.Errorf("instagram container not publishable: status_code=%s status=%s", statusCode, status)
		case statusCode == "" && status == "":
			return fmt.Errorf("instagram container status missing")
		}
		if time.Now().UTC().After(deadline) {
			return fmt.Errorf("instagram container was not ready before timeout: status_code=%s status=%s", statusCode, status)
		}
		timer := time.NewTimer(p.cfg.ContainerPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
