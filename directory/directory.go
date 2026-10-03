// Package directory provides a client for the Keelson Directory API
// (members, users, groups).
package directory

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/keelsonhq/go-sdk/internal/httpclient"
	"github.com/keelsonhq/go-sdk/internal/localmode"
)

// MemberItem represents a workspace member.
type MemberItem struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	// ImageURL is the Clerk-hosted profile image URL, or nil when the member
	// has no uploaded image. Resize with width / height query parameters.
	// Do not store it in your app's DB; re-fetch on display.
	ImageURL *string `json:"image_url"`
}

// PaginatedMembers is the response from GET /__keelson/members.
type PaginatedMembers struct {
	Items      []MemberItem `json:"items"`
	Limit      int          `json:"limit"`
	Offset     int          `json:"offset"`
	NextOffset *int         `json:"next_offset"`
}

// GroupItem represents a workspace group.
//
// Key is the human-readable identifier for referencing a group from code:
// it is always present (the server guarantees a non-null, normalized key),
// stable, and immutable — prefer Key for code references. ID is a stable
// UUID intended for machine integration / internal wiring. The Key field
// stays a pointer for backward compatibility, but in practice groups
// returned by the Directory API always carry a key.
type GroupItem struct {
	ID          string  `json:"id"`
	Key         *string `json:"key"`
	DisplayName string  `json:"display_name"`
	Kind        string  `json:"kind"`
	SystemKind  *string `json:"system_kind"`
}

// ListMembersParams controls filtering and pagination for ListMembers.
//
// GroupKey and GroupID both narrow members to a single group. Prefer
// GroupKey for code references: every group has a key, and keys are stable
// and immutable. GroupID is the UUID for machine integration / internal
// use. Setting both is rejected.
type ListMembersParams struct {
	Limit    *int
	Offset   *int
	Q        string
	Role     string
	GroupKey string
	GroupID  string
}

// RequestOption configures request headers for Directory API calls,
// covering both user-as-actor (WithCookie / WithAuthorization) and
// app-as-actor (WithAppToken) access.
type RequestOption func(*requestConfig)

type requestConfig struct {
	cookie        string
	authorization string
	host          string
	// appActor is true when the last credential option applied was
	// WithAppToken, i.e. the call runs as the app, not the logged-in user.
	appActor bool
}

// WithCookie sets the Cookie header.
func WithCookie(cookie string) RequestOption {
	return func(c *requestConfig) { c.cookie = cookie }
}

// WithAuthorization sets the Authorization header for user-as-actor access.
func WithAuthorization(authorization string) RequestOption {
	return func(c *requestConfig) {
		c.authorization = authorization
		c.appActor = false
	}
}

// WithHost sets the Host header.
func WithHost(host string) RequestOption {
	return func(c *requestConfig) { c.host = host }
}

// WithAppToken runs the call as the app itself (app-as-actor), sending
// Authorization: Bearer <token>. The app token is typically read from the
// KEELSON_DIRECTORY_TOKEN environment variable injected at deploy time.
//
// WithAppToken and WithAuthorization both set the credential, so passing both
// is not supported: the option applied last wins. WithAppToken also drops any
// Cookie set by WithCookie, because a user cookie alongside an app token would
// silently turn a user-context call into an app-as-actor one: the Keelson auth
// gateway resolves an app token before falling back to user credentials. An
// empty token is ignored.
func WithAppToken(token string) RequestOption {
	return func(c *requestConfig) {
		if t := strings.TrimSpace(token); t != "" {
			c.authorization = "Bearer " + t
			c.appActor = true
		}
	}
}

// Client provides access to the Keelson Directory API.
type Client struct {
	baseURL string
	hc      *httpclient.Client
	local   bool
}

// New creates a Directory client.
// If baseURL is empty, KEELSON_DIRECTORY_BASE_URL is used, falling back to
// KEELSON_IDENTITY_BASE_URL. KEELSON_DIRECTORY_BASE_URL is set for
// app-as-actor deployments; the fallback keeps existing user-as-actor
// configurations working.
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
	if baseURL == "" {
		return nil, fmt.Errorf("directory: base_url is required; pass it or set KEELSON_DIRECTORY_BASE_URL / KEELSON_IDENTITY_BASE_URL")
	}
	return &Client{
		baseURL: baseURL,
		hc:      httpclient.New(baseURL, "", nil),
	}, nil
}

// IsLocal reports whether the client is in local mode.
func (c *Client) IsLocal() bool {
	return c.local
}

// rawMembersResponse detects missing required keys in the members response.
type rawMembersResponse struct {
	Items      json.RawMessage `json:"items"`
	Limit      *int            `json:"limit"`
	Offset     *int            `json:"offset"`
	NextOffset *int            `json:"next_offset"`
}

// ListMembers calls GET /__keelson/members.
// In local mode, returns fixture members with filtering and pagination support.
func (c *Client) ListMembers(params *ListMembersParams, opts ...RequestOption) (*PaginatedMembers, error) {
	if params != nil && params.GroupID != "" && params.GroupKey != "" {
		// The Directory API rejects naming the same group two ways; fail
		// fast client-side so local mode behaves identically.
		return nil, fmt.Errorf("directory.ListMembers: specify only one of GroupID or GroupKey")
	}
	if c.local {
		return localListMembers(params), nil
	}

	cfg := buildConfig(opts)

	path := "/__keelson/members"
	if params != nil {
		q := url.Values{}
		if params.Limit != nil {
			q.Set("limit", strconv.Itoa(*params.Limit))
		}
		if params.Offset != nil {
			q.Set("offset", strconv.Itoa(*params.Offset))
		}
		if params.Q != "" {
			q.Set("q", params.Q)
		}
		if params.Role != "" {
			q.Set("role", params.Role)
		}
		if params.GroupKey != "" {
			q.Set("group_key", params.GroupKey)
		}
		if params.GroupID != "" {
			q.Set("group_id", params.GroupID)
		}
		if encoded := q.Encode(); encoded != "" {
			path = path + "?" + encoded
		}
	}

	var raw rawMembersResponse
	if err := c.hc.DoJSONWithHeaders("GET", path, nil, &raw, cfg.headers()); err != nil {
		return nil, fmt.Errorf("directory.ListMembers: %w", err)
	}

	// Pagination responses must explicitly include items, limit, and offset so
	// malformed responses cannot be mistaken for an empty first page.
	if raw.Items == nil {
		return nil, fmt.Errorf("directory.ListMembers: response missing 'items'")
	}
	if raw.Limit == nil {
		return nil, fmt.Errorf("directory.ListMembers: response missing 'limit'")
	}
	if raw.Offset == nil {
		return nil, fmt.Errorf("directory.ListMembers: response missing 'offset'")
	}

	var rawItems []json.RawMessage
	if err := json.Unmarshal(raw.Items, &rawItems); err != nil {
		return nil, fmt.Errorf("directory.ListMembers: invalid 'items': %w", err)
	}

	items := make([]MemberItem, len(rawItems))
	for i, ri := range rawItems {
		m, err := parseMemberItem(ri)
		if err != nil {
			return nil, fmt.Errorf("directory.ListMembers: items[%d]: %w", i, err)
		}
		items[i] = *m
	}

	return &PaginatedMembers{
		Items:      items,
		Limit:      *raw.Limit,
		Offset:     *raw.Offset,
		NextOffset: raw.NextOffset,
	}, nil
}

// GetUser calls GET /__keelson/users/{user_id}.
// In local mode, looks up the user in the fixture member list.
func (c *Client) GetUser(userID string, opts ...RequestOption) (*MemberItem, error) {
	if userID == "" {
		return nil, fmt.Errorf("directory.GetUser: user_id is required")
	}

	if c.local {
		return localGetUser(userID)
	}

	cfg := buildConfig(opts)

	path := "/__keelson/users/" + url.PathEscape(userID)
	var rawBody json.RawMessage
	if err := c.hc.DoJSONWithHeaders("GET", path, nil, &rawBody, cfg.headers()); err != nil {
		return nil, fmt.Errorf("directory.GetUser: %w", err)
	}

	result, err := parseMemberItem(rawBody)
	if err != nil {
		return nil, fmt.Errorf("directory.GetUser: %w", err)
	}

	return result, nil
}

// rawGroupsResponse detects missing 'items' key.
type rawGroupsResponse struct {
	Items json.RawMessage `json:"items"`
}

// ListGroups calls GET /__keelson/groups.
// In local mode, returns the built-in system groups.
func (c *Client) ListGroups(opts ...RequestOption) ([]GroupItem, error) {
	if c.local {
		return localListGroups(), nil
	}

	cfg := buildConfig(opts)

	var raw rawGroupsResponse
	if err := c.hc.DoJSONWithHeaders("GET", "/__keelson/groups", nil, &raw, cfg.headers()); err != nil {
		return nil, fmt.Errorf("directory.ListGroups: %w", err)
	}

	if raw.Items == nil {
		return nil, fmt.Errorf("directory.ListGroups: response missing 'items'")
	}

	var rawItems []json.RawMessage
	if err := json.Unmarshal(raw.Items, &rawItems); err != nil {
		return nil, fmt.Errorf("directory.ListGroups: invalid 'items': %w", err)
	}

	items := make([]GroupItem, len(rawItems))
	for i, ri := range rawItems {
		g, err := parseGroupItem(ri)
		if err != nil {
			return nil, fmt.Errorf("directory.ListGroups: items[%d]: %w", i, err)
		}
		items[i] = *g
	}

	return items, nil
}

// rawMemberItem uses pointers to detect missing keys vs empty strings.
type rawMemberItem struct {
	ID    *string `json:"id"`
	Email *string `json:"email"`
	Name  *string `json:"name"`
	Role  *string `json:"role"`
	// ImageURL stays nil for both null and an absent key (older gateways
	// omit it), so it is optional unlike role.
	ImageURL *string `json:"image_url"`
}

func parseMemberItem(data json.RawMessage) (*MemberItem, error) {
	var raw rawMemberItem
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid member item: %w", err)
	}
	if raw.ID == nil || *raw.ID == "" {
		return nil, fmt.Errorf("missing 'id'")
	}
	if raw.Email == nil {
		return nil, fmt.Errorf("missing 'email'")
	}
	if raw.Name == nil {
		return nil, fmt.Errorf("missing 'name'")
	}
	// role may be an empty string when the stored value is null, but the response
	// must still include the key.
	if raw.Role == nil {
		return nil, fmt.Errorf("missing 'role'")
	}
	return &MemberItem{
		ID:       *raw.ID,
		Email:    *raw.Email,
		Name:     *raw.Name,
		Role:     *raw.Role,
		ImageURL: raw.ImageURL,
	}, nil
}

// rawGroupItem uses pointers to detect missing keys.
type rawGroupItem struct {
	ID          *string `json:"id"`
	Key         *string `json:"key"`
	DisplayName *string `json:"display_name"`
	Kind        *string `json:"kind"`
	SystemKind  *string `json:"system_kind"`
}

func parseGroupItem(data json.RawMessage) (*GroupItem, error) {
	var raw rawGroupItem
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid group item: %w", err)
	}
	if raw.ID == nil || *raw.ID == "" {
		return nil, fmt.Errorf("missing 'id'")
	}
	if raw.DisplayName == nil {
		return nil, fmt.Errorf("missing 'display_name'")
	}
	if raw.Kind == nil {
		return nil, fmt.Errorf("missing 'kind'")
	}
	// key is the group's code-facing identifier and is present on every
	// group the server returns. The pointer type and this null-tolerance are
	// kept for backward compatibility; a non-empty validated key or null is
	// the contract, so an empty/blank string is a malformed response.
	if raw.Key != nil && strings.TrimSpace(*raw.Key) == "" {
		return nil, fmt.Errorf("invalid 'key': empty string (use null for keyless groups)")
	}
	return &GroupItem{
		ID:          *raw.ID,
		Key:         raw.Key,
		DisplayName: *raw.DisplayName,
		Kind:        *raw.Kind,
		SystemKind:  raw.SystemKind,
	}, nil
}

// ---------------------------------------------------------------------------
// Local mode helpers
// ---------------------------------------------------------------------------

func localListMembers(params *ListMembersParams) *PaginatedMembers {
	all := localmode.AllMembers()

	// Resolve the group filter once: GroupID is mapped to its key, an
	// unknown id matches no group (groupUnmatched), and GroupKey is used
	// directly. The HTTP path and ListMembers reject GroupID+GroupKey
	// together, so at most one is set here.
	groupKey := ""
	groupUnmatched := false
	if params != nil {
		groupKey = params.GroupKey
		if params.GroupID != "" {
			if k, ok := localmode.GroupKeyByID(params.GroupID); ok {
				groupKey = k
			} else {
				groupUnmatched = true
			}
		}
	}

	// Apply filters.
	var filtered []MemberItem
	for _, m := range all {
		if params != nil {
			if params.Q != "" {
				q := strings.ToLower(params.Q)
				if !strings.Contains(strings.ToLower(m.Name), q) &&
					!strings.Contains(strings.ToLower(m.Email), q) {
					continue
				}
			}
			if params.Role != "" && m.Role != params.Role {
				continue
			}
			if groupUnmatched {
				continue
			}
			if groupKey != "" && !localmode.RoleInGroup(m.Role, groupKey) {
				continue
			}
		}
		filtered = append(filtered, MemberItem{
			ID: m.ID, Email: m.Email, Name: m.Name, Role: m.Role,
		})
	}

	limit := 25
	offset := 0
	if params != nil {
		if params.Limit != nil && *params.Limit >= 1 {
			limit = *params.Limit
		}
		if params.Offset != nil && *params.Offset >= 0 {
			offset = *params.Offset
		}
	}

	// Paginate — clamp to valid slice bounds.
	start := offset
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := filtered[start:end]
	if page == nil {
		page = []MemberItem{}
	}

	var nextOffset *int
	if end < len(filtered) {
		n := end
		nextOffset = &n
	}

	return &PaginatedMembers{
		Items:      page,
		Limit:      limit,
		Offset:     offset,
		NextOffset: nextOffset,
	}
}

func localGetUser(userID string) (*MemberItem, error) {
	for _, m := range localmode.AllMembers() {
		if m.ID == userID {
			return &MemberItem{
				ID: m.ID, Email: m.Email, Name: m.Name, Role: m.Role,
			}, nil
		}
	}
	return nil, fmt.Errorf("directory.GetUser: %w", &httpclient.APIError{
		StatusCode: 404,
		Body:       fmt.Sprintf(`{"detail":"User not found: %s"}`, userID),
		Method:     "GET",
		URL:        "/__keelson/users/" + userID,
	})
}

func localListGroups() []GroupItem {
	groups := make([]GroupItem, len(localmode.FixtureGroups))
	for i, g := range localmode.FixtureGroups {
		var key *string
		if g.Key != "" {
			k := g.Key
			key = &k
		}
		groups[i] = GroupItem{
			ID:          g.ID,
			Key:         key,
			DisplayName: g.DisplayName,
			Kind:        g.Kind,
			SystemKind:  g.SystemKind,
		}
	}
	return groups
}

func buildConfig(opts []RequestOption) *requestConfig {
	cfg := &requestConfig{}
	for _, o := range opts {
		o(cfg)
	}
	return cfg
}

func (cfg *requestConfig) headers() map[string]string {
	h := make(map[string]string)
	if cfg.host != "" {
		h["Host"] = cfg.host
	}

	// WithAppToken makes the app the actor: send only the bearer token and
	// drop any Cookie so a user-context cookie cannot ride along. The Keelson
	// auth gateway resolves an app token before falling back to user credentials.
	if cfg.appActor {
		if cfg.authorization != "" {
			h["Authorization"] = cfg.authorization
		}
		return h
	}

	if cfg.cookie != "" {
		h["Cookie"] = cfg.cookie
	}
	auth := cfg.authorization
	if auth == "" && cfg.cookie == "" {
		// No explicit credential and no user cookie: fall back to the app
		// token injected at deploy time for app-as-actor access. A cookie
		// signals user-as-actor intent, so the fallback is skipped to avoid
		// silently switching the call to app-as-actor.
		if envToken := strings.TrimSpace(os.Getenv("KEELSON_DIRECTORY_TOKEN")); envToken != "" {
			auth = "Bearer " + envToken
		}
	}
	if auth != "" {
		h["Authorization"] = auth
	}
	return h
}
