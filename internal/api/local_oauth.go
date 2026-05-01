package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/db"
)

const (
	localOAuthAccessTTL  = time.Hour
	localOAuthRefreshTTL = 30 * 24 * time.Hour
	localOAuthCodeTTL    = 10 * time.Minute
)

type authorizePageData struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	Scope               string
	State               string
	ApprovalToken       string
}

func (s Server) handleOAuthAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.publicBaseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/authorize",
		"token_endpoint":                        base + "/token",
		"registration_endpoint":                 base + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"mcp", "offline_access"},
	})
}

func (s Server) handleOAuthProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.publicBaseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":              base + "/mcp",
		"authorization_servers": []string{base},
		"bearer_methods_supported": []string{
			"header",
		},
	})
}

func (s Server) handleOAuthRegisterClient(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              string   `json:"client_name"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid json body")
		return
	}
	if method := strings.TrimSpace(payload.TokenEndpointAuthMethod); method != "" && method != "none" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "only public clients without secret are supported")
		return
	}
	client, err := s.Store.RegisterOAuthClient(r.Context(), payload.RedirectURIs)
	if err != nil {
		if errors.Is(err, db.ErrOAuthClientRedirectsEmpty) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
		writeOAuthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  client.ClientID,
		"client_name":                strings.TrimSpace(payload.ClientName),
		"redirect_uris":              client.RedirectURIs,
		"token_endpoint_auth_method": client.TokenEndpointAuthMethod,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"client_id_issued_at":        client.CreatedAt.Unix(),
	})
}

func (s Server) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	if !s.LocalAuthEnabled {
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "local owner auth is not configured")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "method is not allowed")
		return
	}
	owner, session, err := s.currentOwnerFromSession(r)
	if err != nil {
		http.Redirect(w, r, "/login?return_to="+url.QueryEscape(sanitizeReturnTo(r.URL.RequestURI())), http.StatusSeeOther)
		return
	}
	values := r.URL.Query()
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
			return
		}
		values = r.Form
	}
	if strings.TrimSpace(values.Get("response_type")) != "code" {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_response_type", "response_type must be code")
		return
	}
	clientID := strings.TrimSpace(values.Get("client_id"))
	redirectURI := strings.TrimSpace(values.Get("redirect_uri"))
	codeChallenge := strings.TrimSpace(values.Get("code_challenge"))
	codeChallengeMethod := strings.TrimSpace(values.Get("code_challenge_method"))
	scope := normalizeOAuthScope(strings.TrimSpace(values.Get("scope")))
	state := strings.TrimSpace(values.Get("state"))
	client, err := s.Store.GetOAuthClientByClientID(r.Context(), clientID)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "unknown client_id")
		return
	}
	if !redirectURIAllowed(client.RedirectURIs, redirectURI) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri is not registered")
		return
	}
	if strings.ToUpper(codeChallengeMethod) != "S256" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code_challenge_method must be S256")
		return
	}
	if codeChallenge == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code_challenge is required")
		return
	}
	approval := authorizePageData{
		ResponseType:        "code",
		ClientID:            client.ClientID,
		RedirectURI:         redirectURI,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: "S256",
		Scope:               scope,
		State:               state,
	}
	approval.ApprovalToken = s.authorizeApprovalToken(session.ID, approval)
	if r.Method == http.MethodGet {
		s.renderAuthorizePage(w, approval)
		return
	}
	if !s.authorizeApprovalTokenValid(session.ID, approval, strings.TrimSpace(values.Get("approval_token"))) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "authorization approval is invalid")
		return
	}
	rawCode, _, err := s.Store.CreateOAuthAuthorizationCode(r.Context(), db.CreateOAuthAuthorizationCodeParams{
		ClientID:            client.ClientID,
		OwnerID:             owner.ID,
		RedirectURI:         redirectURI,
		Scope:               scope,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: "S256",
		TTL:                 localOAuthCodeTTL,
	})
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	target, err := url.Parse(redirectURI)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri is invalid")
		return
	}
	q := target.Query()
	q.Set("code", rawCode)
	if state != "" {
		q.Set("state", state)
	}
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s Server) renderAuthorizePage(w http.ResponseWriter, data authorizePageData) {
	t, err := template.New("authorize").Parse(authorizeHTMLTemplate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.Execute(w, data)
}

func (s Server) authorizeApprovalToken(sessionID string, data authorizePageData) string {
	return s.credentialsCipher().SignString(authorizeApprovalMessage(sessionID, data))
}

func (s Server) authorizeApprovalTokenValid(sessionID string, data authorizePageData, token string) bool {
	return s.credentialsCipher().VerifyString(authorizeApprovalMessage(sessionID, data), token)
}

func authorizeApprovalMessage(sessionID string, data authorizePageData) string {
	parts := []string{
		strings.TrimSpace(sessionID),
		strings.TrimSpace(data.ResponseType),
		strings.TrimSpace(data.ClientID),
		strings.TrimSpace(data.RedirectURI),
		strings.TrimSpace(data.CodeChallenge),
		strings.TrimSpace(data.CodeChallengeMethod),
		strings.TrimSpace(data.Scope),
		strings.TrimSpace(data.State),
	}
	return strings.Join(parts, "\n")
}

func (s Server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	switch strings.TrimSpace(r.FormValue("grant_type")) {
	case "authorization_code":
		s.handleOAuthAuthorizationCodeExchange(w, r)
	case "refresh_token":
		s.handleOAuthRefreshTokenExchange(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type is not supported")
	}
}

func (s Server) handleOAuthAuthorizationCodeExchange(w http.ResponseWriter, r *http.Request) {
	clientID := strings.TrimSpace(r.FormValue("client_id"))
	redirectURI := strings.TrimSpace(r.FormValue("redirect_uri"))
	code := strings.TrimSpace(r.FormValue("code"))
	codeVerifier := strings.TrimSpace(r.FormValue("code_verifier"))
	if clientID == "" || redirectURI == "" || code == "" || codeVerifier == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id, redirect_uri, code and code_verifier are required")
		return
	}
	client, err := s.Store.GetOAuthClientByClientID(r.Context(), clientID)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "unknown client_id")
		return
	}
	if !redirectURIAllowed(client.RedirectURIs, redirectURI) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri is not registered")
		return
	}
	authCode, err := s.Store.ConsumeOAuthAuthorizationCode(r.Context(), code, client.ClientID, redirectURI)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrOAuthCodeAlreadyUsed):
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
		case errors.Is(err, db.ErrOAuthRedirectURIMismatch):
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
		default:
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid")
		}
		return
	}
	if !pkceMatches(codeVerifier, authCode.CodeChallenge, authCode.CodeChallengeMethod) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "pkce validation failed")
		return
	}
	accessToken, refreshToken, token, err := s.Store.CreateOAuthToken(r.Context(), db.CreateOAuthTokenParams{
		ClientID:   client.ClientID,
		OwnerID:    authCode.OwnerID,
		Scope:      authCode.Scope,
		AccessTTL:  localOAuthAccessTTL,
		RefreshTTL: localOAuthRefreshTTL,
	})
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse(accessToken, refreshToken, token))
}

func (s Server) handleOAuthRefreshTokenExchange(w http.ResponseWriter, r *http.Request) {
	clientID := strings.TrimSpace(r.FormValue("client_id"))
	refreshToken := strings.TrimSpace(r.FormValue("refresh_token"))
	if clientID == "" || refreshToken == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id and refresh_token are required")
		return
	}
	accessToken, nextRefreshToken, token, err := s.Store.RotateOAuthRefreshToken(r.Context(), refreshToken, clientID, localOAuthAccessTTL, localOAuthRefreshTTL)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse(accessToken, nextRefreshToken, token))
}

func (s Server) oauthAccessTokenMatches(r *http.Request) bool {
	if s.Store == nil {
		return false
	}
	token := bearerTokenFromRequest(r)
	if token == "" {
		return false
	}
	tokenRec, err := s.Store.GetOAuthTokenByAccessToken(r.Context(), token)
	if err != nil {
		return false
	}
	return oauthScopeContains(tokenRec.Scope, "mcp")
}

func (s Server) publicBaseURL(r *http.Request) string {
	base := strings.TrimRight(strings.TrimSpace(s.PublicBaseURL), "/")
	if base != "" {
		return base
	}
	base = strings.TrimRight(requestBaseURL(r), "/")
	if base == "" {
		return "http://localhost:8080"
	}
	return base
}

func bearerTokenFromRequest(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return ""
	}
	return strings.TrimSpace(auth[7:])
}

func redirectURIAllowed(allowed []string, redirectURI string) bool {
	redirectURI = strings.TrimSpace(redirectURI)
	if redirectURI == "" {
		return false
	}
	for _, candidate := range allowed {
		if strings.TrimSpace(candidate) == redirectURI {
			return true
		}
	}
	return false
}

func pkceMatches(verifier, challenge, method string) bool {
	verifier = strings.TrimSpace(verifier)
	challenge = strings.TrimSpace(challenge)
	method = strings.ToUpper(strings.TrimSpace(method))
	if verifier == "" || challenge == "" {
		return false
	}
	if method != "S256" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}

func tokenResponse(accessToken, refreshToken string, token db.OAuthToken) map[string]any {
	return map[string]any{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"token_type":    "Bearer",
		"expires_in":    int(time.Until(token.AccessExpiresAt).Seconds()),
		"scope":         token.Scope,
	}
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	payload := map[string]any{
		"error": strings.TrimSpace(code),
	}
	if desc := strings.TrimSpace(description); desc != "" {
		payload["error_description"] = desc
	}
	writeJSON(w, status, payload)
}

func normalizeOAuthScope(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "mcp"
	}
	return strings.Join(strings.Fields(raw), " ")
}

func oauthScopeContains(scope, required string) bool {
	required = strings.TrimSpace(required)
	if required == "" {
		return false
	}
	for _, item := range strings.Fields(scope) {
		if item == required {
			return true
		}
	}
	return false
}

func oauthWWWAuthenticateHeader(r *http.Request, base string) string {
	params := []string{`Bearer realm="postflow"`}
	resource := strings.TrimRight(strings.TrimSpace(base), "/") + "/mcp"
	if resource != "/mcp" {
		params = append(params, `resource="`+resource+`"`)
	}
	params = append(params, `scope="mcp"`)
	return strings.Join(params, ", ")
}
