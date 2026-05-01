package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

func TestLocalAuthRedirectsLoginAndCreatesSession(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("super-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := store.UpsertLocalOwnerBootstrap(t.Context(), "owner@example.com", string(passwordHash)); err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3, LocalAuthEnabled: true}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/?view=settings", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected settings without session to redirect to login, got %d", w.Code)
	}
	loginURL := w.Header().Get("Location")
	if !strings.HasPrefix(loginURL, "/login?return_to=") {
		t.Fatalf("expected login redirect with return_to, got %q", loginURL)
	}

	form := url.Values{}
	form.Set("email", "owner@example.com")
	form.Set("password", "super-secret")
	form.Set("return_to", "/?view=settings")
	req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected login submit redirect, got %d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Location") != "/?view=settings" {
		t.Fatalf("expected login redirect back to settings, got %q", w.Header().Get("Location"))
	}
	sessionCookie := cookieValue(t, w.Result().Cookies(), localSessionCookieName)
	if sessionCookie == "" {
		t.Fatalf("expected session cookie after login")
	}

	req = httptest.NewRequest(http.MethodGet, "/?view=settings", nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(&http.Cookie{Name: localSessionCookieName, Value: sessionCookie})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected settings with session to render, got %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: localSessionCookieName, Value: sessionCookie})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected logout redirect, got %d", w.Code)
	}
	if got := cookieValue(t, w.Result().Cookies(), localSessionCookieName); got != "" {
		t.Fatalf("expected logout to clear session cookie, got %q", got)
	}
}

func TestLocalAuthDoesNotBreakLegacyAPIToken(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("super-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := store.UpsertLocalOwnerBootstrap(t.Context(), "owner@example.com", string(passwordHash)); err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}

	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		LocalAuthEnabled:  true,
		APIToken:          "legacy-secret",
	}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/schedule", nil)
	req.Header.Set("Authorization", "Bearer legacy-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected legacy api token to keep working, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestLocalOAuthAuthorizationCodeFlowUnlocksMCP(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("super-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := store.UpsertLocalOwnerBootstrap(t.Context(), "owner@example.com", string(passwordHash)); err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}

	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		LocalAuthEnabled:  true,
		PublicBaseURL:     "https://postflow.example",
	}
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	registerBody, _ := json.Marshal(map[string]any{
		"redirect_uris": []string{"https://chatgpt.example/callback"},
		"client_name":   "chatgpt-test",
	})
	registerReq, err := http.NewRequest(http.MethodPost, httpServer.URL+"/oauth/register", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatalf("build register request: %v", err)
	}
	registerReq.Header.Set("Content-Type", "application/json")
	registerResp, err := client.Do(registerReq)
	if err != nil {
		t.Fatalf("register oauth client: %v", err)
	}
	defer registerResp.Body.Close()
	if registerResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(registerResp.Body)
		t.Fatalf("expected oauth client registration 201, got %d body=%s", registerResp.StatusCode, string(body))
	}
	var registered struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(registerResp.Body).Decode(&registered); err != nil {
		t.Fatalf("decode registered client: %v", err)
	}
	if registered.ClientID == "" {
		t.Fatalf("expected client_id in registration response")
	}

	verifier := "local-oauth-verifier"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	authorizePath := "/authorize?response_type=code&client_id=" + url.QueryEscape(registered.ClientID) + "&redirect_uri=" + url.QueryEscape("https://chatgpt.example/callback") + "&code_challenge=" + url.QueryEscape(challenge) + "&code_challenge_method=S256&scope=mcp&state=state123"

	authReq, err := http.NewRequest(http.MethodGet, httpServer.URL+authorizePath, nil)
	if err != nil {
		t.Fatalf("build initial authorize request: %v", err)
	}
	authResp, err := client.Do(authReq)
	if err != nil {
		t.Fatalf("authorize without session: %v", err)
	}
	defer authResp.Body.Close()
	if authResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected authorize without session to redirect to login, got %d", authResp.StatusCode)
	}
	if !strings.HasPrefix(authResp.Header.Get("Location"), "/login?return_to=") {
		t.Fatalf("expected authorize redirect to login, got %q", authResp.Header.Get("Location"))
	}

	loginForm := url.Values{}
	loginForm.Set("email", "owner@example.com")
	loginForm.Set("password", "super-secret")
	loginForm.Set("return_to", authorizePath)
	loginReq, err := http.NewRequest(http.MethodPost, httpServer.URL+"/login", strings.NewReader(loginForm.Encode()))
	if err != nil {
		t.Fatalf("build login request: %v", err)
	}
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResp, err := client.Do(loginReq)
	if err != nil {
		t.Fatalf("login owner: %v", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("expected login redirect, got %d body=%s", loginResp.StatusCode, string(body))
	}
	sessionCookie := cookieValue(t, loginResp.Cookies(), localSessionCookieName)
	if sessionCookie == "" {
		t.Fatalf("expected login session cookie")
	}

	authorizeReq, err := http.NewRequest(http.MethodGet, httpServer.URL+authorizePath, nil)
	if err != nil {
		t.Fatalf("build authorize request: %v", err)
	}
	authorizeReq.AddCookie(&http.Cookie{Name: localSessionCookieName, Value: sessionCookie})
	authorizeResp, err := client.Do(authorizeReq)
	if err != nil {
		t.Fatalf("authorize with session: %v", err)
	}
	defer authorizeResp.Body.Close()
	if authorizeResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(authorizeResp.Body)
		t.Fatalf("expected authorize consent page, got %d body=%s", authorizeResp.StatusCode, string(body))
	}
	consentBody, _ := io.ReadAll(authorizeResp.Body)
	approvalToken := hiddenInputValue(string(consentBody), "approval_token")
	if approvalToken == "" {
		t.Fatalf("expected authorization approval token in consent page")
	}
	approveForm := url.Values{}
	approveForm.Set("response_type", "code")
	approveForm.Set("client_id", registered.ClientID)
	approveForm.Set("redirect_uri", "https://chatgpt.example/callback")
	approveForm.Set("code_challenge", challenge)
	approveForm.Set("code_challenge_method", "S256")
	approveForm.Set("scope", "mcp")
	approveForm.Set("state", "state123")
	approveForm.Set("approval_token", approvalToken)
	approveReq, err := http.NewRequest(http.MethodPost, httpServer.URL+"/authorize", strings.NewReader(approveForm.Encode()))
	if err != nil {
		t.Fatalf("build approve request: %v", err)
	}
	approveReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	approveReq.AddCookie(&http.Cookie{Name: localSessionCookieName, Value: sessionCookie})
	authorizeResp, err = client.Do(approveReq)
	if err != nil {
		t.Fatalf("approve authorization: %v", err)
	}
	defer authorizeResp.Body.Close()
	if authorizeResp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(authorizeResp.Body)
		t.Fatalf("expected authorize redirect with code, got %d body=%s", authorizeResp.StatusCode, string(body))
	}
	callbackURL, err := url.Parse(authorizeResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse callback redirect: %v", err)
	}
	code := callbackURL.Query().Get("code")
	if code == "" || callbackURL.Query().Get("state") != "state123" {
		t.Fatalf("expected callback redirect to include code and state, got %q", authorizeResp.Header.Get("Location"))
	}

	tokenForm := url.Values{}
	tokenForm.Set("grant_type", "authorization_code")
	tokenForm.Set("client_id", registered.ClientID)
	tokenForm.Set("redirect_uri", "https://chatgpt.example/callback")
	tokenForm.Set("code", code)
	tokenForm.Set("code_verifier", verifier)
	tokenReq, err := http.NewRequest(http.MethodPost, httpServer.URL+"/token", strings.NewReader(tokenForm.Encode()))
	if err != nil {
		t.Fatalf("build token request: %v", err)
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenResp, err := client.Do(tokenReq)
	if err != nil {
		t.Fatalf("exchange token: %v", err)
	}
	defer tokenResp.Body.Close()
	if tokenResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(tokenResp.Body)
		t.Fatalf("expected token exchange 200, got %d body=%s", tokenResp.StatusCode, string(body))
	}
	var tokenPayload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenPayload); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if tokenPayload.AccessToken == "" || tokenPayload.RefreshToken == "" {
		t.Fatalf("expected access and refresh tokens, got %+v", tokenPayload)
	}

	initializeBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"chatgpt-test","version":"1.0.0"}}}`
	req, err := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(initializeBody))
	if err != nil {
		t.Fatalf("build mcp initialize request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+tokenPayload.AccessToken)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("call mcp initialize with oauth bearer: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected mcp initialize over oauth bearer to succeed, got %d body=%s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "postflow-mcp") {
		t.Fatalf("expected initialize response to include postflow server info")
	}

	refreshForm := url.Values{}
	refreshForm.Set("grant_type", "refresh_token")
	refreshForm.Set("client_id", registered.ClientID)
	refreshForm.Set("refresh_token", tokenPayload.RefreshToken)
	refreshReq, err := http.NewRequest(http.MethodPost, httpServer.URL+"/token", strings.NewReader(refreshForm.Encode()))
	if err != nil {
		t.Fatalf("build refresh request: %v", err)
	}
	refreshReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	refreshResp, err := client.Do(refreshReq)
	if err != nil {
		t.Fatalf("refresh oauth token: %v", err)
	}
	defer refreshResp.Body.Close()
	if refreshResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(refreshResp.Body)
		t.Fatalf("expected refresh 200, got %d body=%s", refreshResp.StatusCode, string(body))
	}
}

func TestLocalOAuthMetadataAdvertisesOfflineAccess(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		LocalAuthEnabled:  true,
		PublicBaseURL:     "https://postflow.example",
	}
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()

	resp, err := http.Get(httpServer.URL + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("get openid configuration: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected metadata 200, got %d body=%s", resp.StatusCode, string(body))
	}

	var payload struct {
		ScopesSupported []string `json:"scopes_supported"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if !containsString(payload.ScopesSupported, "mcp") {
		t.Fatalf("expected metadata to advertise mcp scope, got %v", payload.ScopesSupported)
	}
	if !containsString(payload.ScopesSupported, "offline_access") {
		t.Fatalf("expected metadata to advertise offline_access scope, got %v", payload.ScopesSupported)
	}
}

func TestLocalOAuthAuthorizeRequiresConsentPost(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("super-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	owner, err := store.UpsertLocalOwnerBootstrap(t.Context(), "owner@example.com", string(passwordHash))
	if err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}
	sessionToken, _, err := store.CreateWebSession(t.Context(), owner.ID, localSessionTTL)
	if err != nil {
		t.Fatalf("create web session: %v", err)
	}
	clientRec, err := store.RegisterOAuthClient(t.Context(), []string{"https://attacker.example/callback"})
	if err != nil {
		t.Fatalf("register oauth client: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, LocalAuthEnabled: true}
	h := srv.Handler()
	verifier := "csrf-verifier"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	path := "/authorize?response_type=code&client_id=" + url.QueryEscape(clientRec.ClientID) + "&redirect_uri=" + url.QueryEscape("https://attacker.example/callback") + "&code_challenge=" + url.QueryEscape(challenge) + "&code_challenge_method=S256&scope=mcp&state=csrf"

	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: localSessionCookieName, Value: sessionToken})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected consent page instead of redirect, got %d body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Fatalf("did not expect authorization code redirect on GET, got %q", loc)
	}
	if !strings.Contains(w.Body.String(), "Authorize MCP access") {
		t.Fatalf("expected consent UI, got %s", w.Body.String())
	}
}

func TestLocalAuthAllowsAnonymousMCPDiscoveryButProtectsToolCalls(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("super-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := store.UpsertLocalOwnerBootstrap(t.Context(), "owner@example.com", string(passwordHash)); err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}

	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		LocalAuthEnabled:  true,
	}
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()

	client := &http.Client{}

	initializeBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"chatgpt-test","version":"1.0.0"}}}`
	initializeReq, err := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(initializeBody))
	if err != nil {
		t.Fatalf("build initialize request: %v", err)
	}
	initializeReq.Header.Set("Content-Type", "application/json")
	initializeReq.Header.Set("Accept", "application/json, text/event-stream")
	initializeResp, err := client.Do(initializeReq)
	if err != nil {
		t.Fatalf("call initialize without auth: %v", err)
	}
	defer initializeResp.Body.Close()
	if initializeResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(initializeResp.Body)
		t.Fatalf("expected initialize without auth to succeed, got %d body=%s", initializeResp.StatusCode, string(body))
	}

	listToolsBody := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	listToolsReq, err := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(listToolsBody))
	if err != nil {
		t.Fatalf("build tools/list request: %v", err)
	}
	listToolsReq.Header.Set("Content-Type", "application/json")
	listToolsReq.Header.Set("Accept", "application/json, text/event-stream")
	listToolsResp, err := client.Do(listToolsReq)
	if err != nil {
		t.Fatalf("call tools/list without auth: %v", err)
	}
	defer listToolsResp.Body.Close()
	if listToolsResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listToolsResp.Body)
		t.Fatalf("expected tools/list without auth to succeed, got %d body=%s", listToolsResp.StatusCode, string(body))
	}

	toolCallBody := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"postflow_health","arguments":{}}}`
	toolCallReq, err := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(toolCallBody))
	if err != nil {
		t.Fatalf("build tools/call request: %v", err)
	}
	toolCallReq.Header.Set("Content-Type", "application/json")
	toolCallReq.Header.Set("Accept", "application/json, text/event-stream")
	toolCallResp, err := client.Do(toolCallReq)
	if err != nil {
		t.Fatalf("call tools/call without auth: %v", err)
	}
	defer toolCallResp.Body.Close()
	if toolCallResp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(toolCallResp.Body)
		t.Fatalf("expected tools/call without auth to stay protected, got %d body=%s", toolCallResp.StatusCode, string(body))
	}
}

func TestLocalAuthSessionAllowsMediaPreviewWithoutBasicPrompt(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("super-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	owner, err := store.UpsertLocalOwnerBootstrap(t.Context(), "owner@example.com", string(passwordHash))
	if err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}
	sessionToken, _, err := store.CreateWebSession(t.Context(), owner.ID, localSessionTTL)
	if err != nil {
		t.Fatalf("create web session: %v", err)
	}

	mediaPath := filepath.Join(tempDir, "preview.png")
	if err := os.WriteFile(mediaPath, []byte("preview-bytes"), 0o644); err != nil {
		t.Fatalf("seed media file: %v", err)
	}
	createdMedia, err := store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: "preview.png",
		StoragePath:  mediaPath,
		MimeType:     "image/png",
		SizeBytes:    int64(len("preview-bytes")),
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		LocalAuthEnabled:  true,
		UIBasicUser:       "legacy-user",
		UIBasicPass:       "legacy-pass",
	}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/media/"+createdMedia.ID+"/content", nil)
	req.AddCookie(&http.Cookie{Name: localSessionCookieName, Value: sessionToken})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected media preview to load with local session, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("expected no basic auth challenge for media preview, got %q", got)
	}
	if body := w.Body.String(); body != "preview-bytes" {
		t.Fatalf("expected media bytes, got %q", body)
	}
}

func cookieValue(t *testing.T, cookies []*http.Cookie, name string) string {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func hiddenInputValue(html, name string) string {
	marker := `name="` + name + `"`
	idx := strings.Index(html, marker)
	if idx < 0 {
		return ""
	}
	valueMarker := `value="`
	valueIdx := strings.Index(html[idx:], valueMarker)
	if valueIdx < 0 {
		return ""
	}
	start := idx + valueIdx + len(valueMarker)
	end := strings.Index(html[start:], `"`)
	if end < 0 {
		return ""
	}
	return html[start : start+end]
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
