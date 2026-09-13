package identity_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelsonhq/go-sdk/identity"
	"github.com/keelsonhq/go-sdk/internal/httpclient"
)

func TestGetCurrentUser(t *testing.T) {
	client, err := identity.New("http://test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	user, err := client.GetCurrentUser(identity.WithHeaders(http.Header{
		"X-Keelson-User-Id":    {"u-1"},
		"X-Keelson-User-Email": {"a@b.com"},
		"X-Keelson-User-Name":  {"Alice"},
	}))
	if err != nil {
		t.Fatalf("GetCurrentUser: %v", err)
	}
	if user.ID != "u-1" {
		t.Errorf("ID = %q, want %q", user.ID, "u-1")
	}
	if user.Email == nil || *user.Email != "a@b.com" {
		t.Errorf("Email = %v, want %q", user.Email, "a@b.com")
	}
	if user.Name == nil || *user.Name != "Alice" {
		t.Errorf("Name = %v, want %q", user.Name, "Alice")
	}
}

func TestGetCurrentUser_CaseInsensitiveHeaders(t *testing.T) {
	client, _ := identity.New("http://test")

	user, err := client.GetCurrentUser(identity.WithHeaders(map[string][]string{
		"x-keelson-user-id":    {"", "u-2"},
		"x-keelson-user-email": {"case@example.com"},
	}))
	if err != nil {
		t.Fatalf("GetCurrentUser: %v", err)
	}
	if user.ID != "u-2" {
		t.Errorf("ID = %q, want u-2", user.ID)
	}
	if user.Email == nil || *user.Email != "case@example.com" {
		t.Errorf("Email = %v, want case@example.com", user.Email)
	}
	if user.Name != nil {
		t.Errorf("Name = %v, want nil", user.Name)
	}
}

func TestGetCurrentUser_MissingHeader(t *testing.T) {
	client, _ := identity.New("http://test")
	_, err := client.GetCurrentUser(identity.WithHeaders(http.Header{}))
	if err == nil {
		t.Fatal("expected missing header error")
	}
	if !strings.Contains(err.Error(), "x-keelson-user-id") {
		t.Fatalf("error = %q, want missing x-keelson-user-id", err)
	}
}

func TestGetCurrentIdentity(t *testing.T) {
	fullResponse := map[string]any{
		"user":      map[string]any{"id": "u-1", "email": "a@b.com", "name": "Alice"},
		"workspace": map[string]any{"id": "w-1", "role": "OWNER"},
		"tenant":    map[string]any{"id": "ignored", "role": "APP_USER"},
		"app":       map[string]any{"id": "app-1", "permissions": []string{"manage"}, "roles": []string{"editor"}},
		"authz":     map[string]any{"version": 3},
		"attributes": map[string]any{
			"groups": []string{"developers", "everyone"},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__keelson/users/u-1/identity" {
			t.Errorf("path = %q, want /__keelson/users/u-1/identity", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer keelson_xyz" {
			t.Errorf("Authorization = %q, want Bearer keelson_xyz", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Errorf("Cookie = %q, want empty", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		if got := r.Host; got != "app.example.test" {
			t.Errorf("Host = %q, want app.example.test", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fullResponse)
	}))
	defer ts.Close()

	client, err := identity.New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ci, err := client.GetCurrentIdentity(
		identity.WithHeaders(http.Header{"X-Keelson-User-Id": {"u-1"}}),
		identity.WithAppToken("keelson_xyz"),
		identity.WithHost("app.example.test"),
	)
	if err != nil {
		t.Fatalf("GetCurrentIdentity: %v", err)
	}
	if ci.User.ID != "u-1" {
		t.Errorf("User.ID = %q, want u-1", ci.User.ID)
	}
	if ci.Workspace.ID != "w-1" || ci.Workspace.Role != "OWNER" {
		t.Errorf("Workspace = %#v, want w-1/OWNER", ci.Workspace)
	}
	if ci.Tenant != ci.Workspace {
		t.Errorf("Tenant = %#v, want workspace alias %#v", ci.Tenant, ci.Workspace)
	}
	if len(ci.App.Permissions) != 1 || ci.App.Permissions[0] != "manage" {
		t.Errorf("App.Permissions = %v, want [manage]", ci.App.Permissions)
	}
	if ci.Attributes == nil || len(ci.Attributes.Groups) != 2 {
		t.Errorf("Attributes.Groups = %v, want 2 groups", ci.Attributes)
	}
	wire, err := json.Marshal(ci)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var aliases struct {
		Workspace identity.WorkspaceIdentity `json:"workspace"`
		Tenant    identity.TenantIdentity    `json:"tenant"`
	}
	if err := json.Unmarshal(wire, &aliases); err != nil {
		t.Fatalf("Unmarshal marshaled identity: %v", err)
	}
	if aliases.Workspace != aliases.Tenant || aliases.Workspace.ID != "w-1" {
		t.Errorf("marshaled aliases = %#v, want identical canonical value", aliases)
	}
}

func TestGetCurrentIdentity_EnvToken(t *testing.T) {
	t.Setenv("KEELSON_DIRECTORY_TOKEN", "keelson_env")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer keelson_env" {
			t.Errorf("Authorization = %q, want Bearer keelson_env", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(validIdentityResponse())
	}))
	defer ts.Close()

	client, _ := identity.New(ts.URL)
	_, err := client.GetCurrentIdentity(identity.WithHeaders(http.Header{"X-Keelson-User-Id": {"u-env"}}))
	if err != nil {
		t.Fatalf("GetCurrentIdentity: %v", err)
	}
}

func TestGetCurrentIdentity_IgnoresCookieAndAuthorizationInSubjectHeaders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer keelson_xyz" {
			t.Errorf("Authorization = %q, want Bearer keelson_xyz", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Errorf("Cookie = %q, want empty", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(validIdentityResponse())
	}))
	defer ts.Close()

	client, _ := identity.New(ts.URL)
	_, err := client.GetCurrentIdentity(
		identity.WithHeaders(http.Header{
			"X-Keelson-User-Id": {"u-auth"},
			"Cookie":            {"sid=browser"},
			"Authorization":     {"Bearer ey.user.jwt"},
		}),
		identity.WithAppToken("keelson_xyz"),
	)
	if err != nil {
		t.Fatalf("GetCurrentIdentity: %v", err)
	}
}

func TestGetCurrentIdentity_RejectsCookie(t *testing.T) {
	client, _ := identity.New("http://test")
	_, err := client.GetCurrentIdentity(
		identity.WithHeaders(http.Header{"X-Keelson-User-Id": {"u-1"}}),
		identity.WithCookie("session=abc"),
	)
	if err == nil || !strings.Contains(err.Error(), "cookie is not supported") {
		t.Fatalf("error = %v, want cookie rejection", err)
	}

}

func TestGetCurrentIdentity_RequiresCredential(t *testing.T) {
	t.Setenv("KEELSON_DIRECTORY_TOKEN", "")
	client, _ := identity.New("http://test")
	_, err := client.GetCurrentIdentity(identity.WithHeaders(http.Header{"X-Keelson-User-Id": {"u-1"}}))
	if err == nil || !strings.Contains(err.Error(), "requires app_token") {
		t.Fatalf("error = %v, want missing credential", err)
	}
}

func TestGetCurrentIdentity_RejectsExplicitUserBearer(t *testing.T) {
	t.Setenv("KEELSON_DIRECTORY_TOKEN", "")
	client, _ := identity.New("http://test")
	_, err := client.GetCurrentIdentity(
		identity.WithHeaders(http.Header{"X-Keelson-User-Id": {"u-1"}}),
		identity.WithAuthorization("Bearer ey.jwt"),
	)
	if err == nil || !strings.Contains(err.Error(), "Bearer app token") {
		t.Fatalf("error = %v, want app token bearer rejection", err)
	}
}

func TestGetCurrentIdentity_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"detail":"not found"}`))
	}))
	defer ts.Close()

	client, _ := identity.New(ts.URL)
	_, err := client.GetCurrentIdentity(
		identity.WithHeaders(http.Header{"X-Keelson-User-Id": {"missing"}}),
		identity.WithAppToken("keelson_xyz"),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *httpclient.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusNotFound)
	}
}

func TestGetCurrentIdentity_MalformedResponses(t *testing.T) {
	tests := []struct {
		name     string
		response any
		wantMsg  string
	}{
		{name: "empty object", response: map[string]any{}, wantMsg: "missing 'user'"},
		{
			name: "missing workspace aliases",
			response: map[string]any{
				"user":  map[string]any{"id": "u-1", "email": "a@b.com", "name": "A"},
				"app":   map[string]any{"id": "app-1", "permissions": []string{}, "roles": []string{}},
				"authz": map[string]any{"version": 1},
			},
			wantMsg: "missing 'workspace'",
		},
		{
			name: "missing app.permissions",
			response: map[string]any{
				"user":   map[string]any{"id": "u-1", "email": "a@b.com", "name": "A"},
				"tenant": map[string]any{"id": "t-1", "role": "OWNER"},
				"app":    map[string]any{"id": "app-1", "roles": []string{}},
				"authz":  map[string]any{"version": 1},
			},
			wantMsg: "missing app.permissions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer ts.Close()

			client, _ := identity.New(ts.URL)
			_, err := client.GetCurrentIdentity(
				identity.WithHeaders(http.Header{"X-Keelson-User-Id": {"u-1"}}),
				identity.WithAppToken("keelson_xyz"),
			)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got := err.Error(); !strings.Contains(got, tt.wantMsg) {
				t.Errorf("error = %q, want to contain %q", got, tt.wantMsg)
			}
		})
	}
}

func TestGetCurrentIdentity_TrailingSlashBaseURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__keelson/users/u-1/identity" {
			t.Errorf("path = %q, want /__keelson/users/u-1/identity", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(validIdentityResponse())
	}))
	defer ts.Close()

	client, err := identity.New(ts.URL + "/")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.GetCurrentIdentity(
		identity.WithHeaders(http.Header{"X-Keelson-User-Id": {"u-1"}}),
		identity.WithAppToken("keelson_xyz"),
	)
	if err != nil {
		t.Fatalf("GetCurrentIdentity: %v", err)
	}
}

// TestNew_NoBaseURL_HeaderOnlyWorks verifies that a header-only caller can
// construct the client and read the current user with no base URL configured.
// The base URL is only required for the network-backed GetCurrentIdentity.
func TestNew_NoBaseURL_HeaderOnlyWorks(t *testing.T) {
	t.Setenv("KEELSON_DIRECTORY_BASE_URL", "")
	t.Setenv("KEELSON_IDENTITY_BASE_URL", "")
	client, err := identity.New("")
	if err != nil {
		t.Fatalf("New(\"\") with no base URL should succeed for header-only use: %v", err)
	}
	user, err := client.GetCurrentUser(
		identity.WithHeaders(http.Header{"X-Keelson-User-Id": {"u-1"}}),
	)
	if err != nil {
		t.Fatalf("GetCurrentUser: %v", err)
	}
	if user.ID != "u-1" {
		t.Fatalf("GetCurrentUser id = %q, want u-1", user.ID)
	}
}

// TestGetCurrentIdentity_RequiresBaseURL: the network-backed call still fails
// clearly when no base URL is configured.
func TestGetCurrentIdentity_RequiresBaseURL(t *testing.T) {
	t.Setenv("KEELSON_DIRECTORY_BASE_URL", "")
	t.Setenv("KEELSON_IDENTITY_BASE_URL", "")
	client, err := identity.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.GetCurrentIdentity(
		identity.WithHeaders(http.Header{"X-Keelson-User-Id": {"u-1"}}),
		identity.WithAppToken("keelson_xyz"),
	)
	if err == nil {
		t.Fatal("expected base_url required error from GetCurrentIdentity, got nil")
	}
	if !strings.Contains(err.Error(), "base_url is required") {
		t.Fatalf("error = %q, want base_url is required", err.Error())
	}
}

func TestNew_EnvFallback(t *testing.T) {
	t.Setenv("KEELSON_DIRECTORY_BASE_URL", "http://directory-host:9999")
	t.Setenv("KEELSON_IDENTITY_BASE_URL", "http://identity-host:9999")
	client, err := identity.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func validIdentityResponse() map[string]any {
	return map[string]any{
		"user":   map[string]any{"id": "u-1", "email": "a@b.com", "name": "Alice"},
		"tenant": map[string]any{"id": "t-1", "role": "OWNER"},
		"app":    map[string]any{"id": "app-1", "permissions": []string{}, "roles": []string{}},
		"authz":  map[string]any{"version": 1},
	}
}
