// Package identity provides a client for the Keelson Identity API (GET /__keelson/user).
package identity

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/keelsonhq/go-sdk/internal/httpclient"
	"github.com/keelsonhq/go-sdk/internal/localmode"
)

// UserIdentity holds the authenticated user's basic profile.
type UserIdentity struct {
	ID    string  `json:"id"`
	Email *string `json:"email"`
	Name  *string `json:"name"`
}

// TenantIdentity holds the tenant context of the current request.
type TenantIdentity struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

// AppIdentity holds the app context and authorization info.
type AppIdentity struct {
	ID          string   `json:"id"`
	Permissions []string `json:"permissions"`
	Roles       []string `json:"roles"`
}

// Attributes holds optional user attributes such as group memberships.
type Attributes struct {
	Groups []string `json:"groups"`
}

// CurrentIdentity is the full response from GET /__keelson/user.
type CurrentIdentity struct {
	User       UserIdentity   `json:"user"`
	Tenant     TenantIdentity `json:"tenant"`
	App        AppIdentity    `json:"app"`
	Authz      *authzInfo     `json:"authz,omitempty"`
	Attributes *Attributes    `json:"attributes,omitempty"`
}

type authzInfo struct {
	Version int `json:"version"`
}

type rawAuthzInfo struct {
	Version *int `json:"version"`
}

// rawIdentityResponse is used to detect missing top-level keys before
// unmarshalling into typed structs. json.RawMessage fields will be nil
// when the key is absent, vs empty when the value is {}.
type rawIdentityResponse struct {
	User       json.RawMessage `json:"user"`
	Tenant     json.RawMessage `json:"tenant"`
	App        json.RawMessage `json:"app"`
	Authz      json.RawMessage `json:"authz"`
	Attributes json.RawMessage `json:"attributes"`
}

// RequestOption configures how the Identity API request is made.
// User-Context APIs require cookie/authorization/host to be forwarded
// because the Keelson auth gateway removes those untrusted values before the
// request reaches the app.
type RequestOption func(*requestConfig)

type requestConfig struct {
	cookie        string
	authorization string
	host          string
	headers       http.Header
}

// WithCookie sets the Cookie header on the upstream request.
func WithCookie(cookie string) RequestOption {
	return func(c *requestConfig) { c.cookie = cookie }
}

// WithAuthorization sets the Authorization header on the upstream request.
func WithAuthorization(authorization string) RequestOption {
	return func(c *requestConfig) { c.authorization = authorization }
}

// WithHost sets the Host header on the upstream request.
func WithHost(host string) RequestOption {
	return func(c *requestConfig) { c.host = host }
}

// WithHeaders supplies incoming request headers for trusted current-user
// lookup. http.Header is accepted because its underlying type is
// map[string][]string.
func WithHeaders(headers map[string][]string) RequestOption {
	return func(c *requestConfig) {
		c.headers = http.Header{}
		for k, values := range headers {
			c.headers[k] = append([]string(nil), values...)
		}
	}
}

// WithAppToken runs the current identity lookup as the app itself, sending
// Authorization: Bearer <token>.
func WithAppToken(token string) RequestOption {
	return func(c *requestConfig) {
		if t := strings.TrimSpace(token); t != "" {
			c.authorization = "Bearer " + t
		}
	}
}

// Client provides access to the Keelson Identity API.
type Client struct {
	baseURL string
	hc      *httpclient.Client
	local   bool
}

// New creates an Identity client.
// baseURL is the upstream Keelson auth gateway base (for example,
// "http://localhost:8787").
// If empty, KEELSON_DIRECTORY_BASE_URL is used, falling back to the deprecated
// KEELSON_IDENTITY_BASE_URL.
//
// The base URL is validated lazily, at the first network call. GetCurrentUser
// reads trusted X-Keelson-User-* request headers and makes no HTTP call, so a
// header-only caller (identity.New("")) succeeds even when no base URL is
// configured. GetCurrentIdentity, which does call the network, returns a clear
// error if the base URL is still missing.
//
// When KEELSON_LOCAL_MODE is enabled the client returns fixture data
// without making HTTP calls, matching the Node and Python SDKs.
func New(baseURL string) (*Client, error) {
	if localmode.Enabled() {
		return &Client{local: true}, nil
	}
	if baseURL == "" {
		baseURL = os.Getenv("KEELSON_DIRECTORY_BASE_URL")
	}
	if baseURL == "" {
		baseURL = os.Getenv("KEELSON_IDENTITY_BASE_URL")
	}
	c := &Client{baseURL: baseURL}
	if baseURL != "" {
		// Token is empty — User-Context API uses cookie/authorization forwarding,
		// not a bearer token. hc is left nil when no base URL is configured; the
		// network-backed methods guard on it.
		c.hc = httpclient.New(baseURL, "", nil)
	}
	return c, nil
}

// IsLocal reports whether the client is in local mode.
func (c *Client) IsLocal() bool {
	return c.local
}

// GetCurrentUser reads trusted X-Keelson-User-* headers and returns the
// authenticated user's basic profile.
// In local mode, returns fixture data controlled by KEELSON_LOCAL_* env vars.
func (c *Client) GetCurrentUser(opts ...RequestOption) (*UserIdentity, error) {
	if c.local {
		return localGetCurrentUser(), nil
	}

	cfg := buildConfig(opts)
	return parseCurrentUserHeaders(cfg.headers)
}

// GetCurrentIdentity calls GET /__keelson/users/{id}/identity as the app
// actor and returns the full current identity.
func (c *Client) GetCurrentIdentity(opts ...RequestOption) (*CurrentIdentity, error) {
	if c.local {
		return localGetCurrentIdentity(), nil
	}
	if c.hc == nil {
		return nil, fmt.Errorf("identity.GetCurrentIdentity: base_url is required; pass it to identity.New or set KEELSON_DIRECTORY_BASE_URL")
	}

	cfg := buildConfig(opts)
	user, err := parseCurrentUserHeaders(cfg.headers)
	if err != nil {
		return nil, fmt.Errorf("identity.GetCurrentIdentity: %w", err)
	}
	headers, err := cfg.currentIdentityHeaders()
	if err != nil {
		return nil, fmt.Errorf("identity.GetCurrentIdentity: %w", err)
	}

	var raw rawIdentityResponse
	path := "/__keelson/users/" + url.PathEscape(user.ID) + "/identity"
	if err := c.hc.DoJSONWithHeaders("GET", path, nil, &raw, headers); err != nil {
		return nil, fmt.Errorf("identity.GetCurrentIdentity: %w", err)
	}

	return parseCurrentIdentity(raw, "identity.GetCurrentIdentity")
}

func parseCurrentIdentity(raw rawIdentityResponse, context string) (*CurrentIdentity, error) {
	// Validate required top-level keys are present in JSON.
	if raw.User == nil {
		return nil, fmt.Errorf("%s: response missing 'user'", context)
	}
	if raw.Tenant == nil {
		return nil, fmt.Errorf("%s: response missing 'tenant'", context)
	}
	if raw.App == nil {
		return nil, fmt.Errorf("%s: response missing 'app'", context)
	}

	var result CurrentIdentity

	if err := json.Unmarshal(raw.User, &result.User); err != nil {
		return nil, fmt.Errorf("%s: invalid 'user': %w", context, err)
	}
	if err := json.Unmarshal(raw.Tenant, &result.Tenant); err != nil {
		return nil, fmt.Errorf("%s: invalid 'tenant': %w", context, err)
	}
	if err := json.Unmarshal(raw.App, &result.App); err != nil {
		return nil, fmt.Errorf("%s: invalid 'app': %w", context, err)
	}

	// A full identity response must include authorization metadata and its
	// version so callers never accept an incomplete authorization context.
	if raw.Authz == nil {
		return nil, fmt.Errorf("%s: response missing 'authz'", context)
	}
	var rawA rawAuthzInfo
	if err := json.Unmarshal(raw.Authz, &rawA); err != nil {
		return nil, fmt.Errorf("%s: invalid 'authz': %w", context, err)
	}
	if rawA.Version == nil {
		return nil, fmt.Errorf("%s: response missing authz.version", context)
	}
	result.Authz = &authzInfo{Version: *rawA.Version}

	if raw.Attributes != nil {
		var attr Attributes
		if err := json.Unmarshal(raw.Attributes, &attr); err != nil {
			return nil, fmt.Errorf("%s: invalid 'attributes': %w", context, err)
		}
		result.Attributes = &attr
	}

	// Validate required fields within each section.
	if result.User.ID == "" {
		return nil, fmt.Errorf("%s: response missing user.id", context)
	}
	if result.Tenant.ID == "" {
		return nil, fmt.Errorf("%s: response missing tenant.id", context)
	}
	if result.Tenant.Role == "" {
		return nil, fmt.Errorf("%s: response missing tenant.role", context)
	}
	if result.App.ID == "" {
		return nil, fmt.Errorf("%s: response missing app.id", context)
	}
	// app.permissions and app.roles must be present, although either array may
	// be empty.
	if result.App.Permissions == nil {
		return nil, fmt.Errorf("%s: response missing app.permissions", context)
	}
	if result.App.Roles == nil {
		return nil, fmt.Errorf("%s: response missing app.roles", context)
	}

	return &result, nil
}

func parseCurrentUserHeaders(headers http.Header) (*UserIdentity, error) {
	id := readHeader(headers, "x-keelson-user-id")
	if id == "" {
		return nil, fmt.Errorf("current user headers missing 'x-keelson-user-id'")
	}
	user := &UserIdentity{ID: id}
	if email := readHeader(headers, "x-keelson-user-email"); email != "" {
		user.Email = &email
	}
	if name := readHeader(headers, "x-keelson-user-name"); name != "" {
		user.Name = &name
	}
	return user, nil
}

func readHeader(headers http.Header, name string) string {
	if headers == nil {
		return ""
	}
	if value := strings.TrimSpace(headers.Get(name)); value != "" {
		return value
	}
	for key, values := range headers {
		if strings.EqualFold(key, name) {
			for _, value := range values {
				if text := strings.TrimSpace(value); text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func buildConfig(opts []RequestOption) *requestConfig {
	cfg := &requestConfig{}
	for _, o := range opts {
		o(cfg)
	}
	return cfg
}

func (cfg *requestConfig) currentIdentityHeaders() (map[string]string, error) {
	if cfg.cookie != "" {
		return nil, fmt.Errorf("requires an app token; cookie is not supported")
	}

	auth := strings.TrimSpace(cfg.authorization)
	if auth == "" {
		if envToken := strings.TrimSpace(os.Getenv("KEELSON_DIRECTORY_TOKEN")); envToken != "" {
			auth = "Bearer " + envToken
		}
	}
	if auth == "" {
		return nil, fmt.Errorf("requires app_token or KEELSON_DIRECTORY_TOKEN")
	}
	if !strings.HasPrefix(strings.ToLower(auth), "bearer keelson_") {
		return nil, fmt.Errorf("requires a Bearer app token authorization")
	}
	headers := map[string]string{"Accept": "application/json", "Authorization": auth}
	if host := strings.TrimSpace(cfg.host); host != "" {
		headers["Host"] = host
	}
	return headers, nil
}

// localGetCurrentUser builds a fixture UserIdentity from env vars.
func localGetCurrentUser() *UserIdentity {
	email := localmode.UserEmail()
	name := localmode.UserName()
	return &UserIdentity{
		ID:    localmode.UserID(),
		Email: &email,
		Name:  &name,
	}
}

// localGetCurrentIdentity builds a fixture CurrentIdentity from env vars.
func localGetCurrentIdentity() *CurrentIdentity {
	email := localmode.UserEmail()
	name := localmode.UserName()
	role := localmode.TenantRole()
	return &CurrentIdentity{
		User: UserIdentity{
			ID:    localmode.UserID(),
			Email: &email,
			Name:  &name,
		},
		Tenant: TenantIdentity{
			ID:   localmode.TenantID(),
			Role: role,
		},
		App: AppIdentity{
			ID:          localmode.AppID(),
			Permissions: []string{"manage", "view"},
			Roles:       []string{},
		},
		Authz: &authzInfo{Version: 1},
		Attributes: &Attributes{
			Groups: localmode.GroupsForRole(role),
		},
	}
}
