package identity_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelsonhq/go-sdk/identity"
	"github.com/keelsonhq/go-sdk/internal/testfixtures"
)

// TestParity_CurrentUser parses the shared parity fixture and asserts
// the same semantic field values that Node and Python parity tests assert.
func TestParity_CurrentUser(t *testing.T) {
	fixture, err := testfixtures.ReadFile("identity_current_user.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture)
	}))
	defer ts.Close()

	t.Setenv("KEELSON_LOCAL_MODE", "")
	client, err := identity.New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	headers := http.Header{
		"X-Keelson-User-Id":    {"usr_parity01"},
		"X-Keelson-User-Email": {"taro@example.com"},
		"X-Keelson-User-Name":  {"Taro Yamada"},
	}
	user, err := client.GetCurrentUser(identity.WithHeaders(headers))
	if err != nil {
		t.Fatalf("GetCurrentUser: %v", err)
	}
	id, err := client.GetCurrentIdentity(
		identity.WithHeaders(headers),
		identity.WithAppToken("keelson_parity"),
	)
	if err != nil {
		t.Fatalf("GetCurrentIdentity: %v", err)
	}

	// --- Cross-language parity assertions ---
	// These values must match across Go, Node, and Python parity tests.

	if user.ID != "usr_parity01" {
		t.Errorf("user.id = %q, want %q", user.ID, "usr_parity01")
	}
	if user.Email == nil || *user.Email != "taro@example.com" {
		t.Errorf("user.email = %v, want %q", user.Email, "taro@example.com")
	}
	if user.Name == nil || *user.Name != "Taro Yamada" {
		t.Errorf("user.name = %v, want %q", user.Name, "Taro Yamada")
	}

	if id.User.ID != "usr_parity01" {
		t.Errorf("user.id = %q, want %q", id.User.ID, "usr_parity01")
	}
	if id.User.Email == nil || *id.User.Email != "taro@example.com" {
		t.Errorf("user.email = %v, want %q", id.User.Email, "taro@example.com")
	}
	if id.User.Name == nil || *id.User.Name != "Taro Yamada" {
		t.Errorf("user.name = %v, want %q", id.User.Name, "Taro Yamada")
	}

	if id.Workspace.ID != "workspace_001" {
		t.Errorf("workspace.id = %q, want %q", id.Workspace.ID, "workspace_001")
	}
	if id.Workspace.Role != "admin" {
		t.Errorf("workspace.role = %q, want %q", id.Workspace.Role, "admin")
	}
	if id.Workspace != id.Tenant {
		t.Errorf("workspace = %#v, deprecated tenant alias = %#v; want identical", id.Workspace, id.Tenant)
	}

	if id.App.ID != "app_xyz" {
		t.Errorf("app.id = %q, want %q", id.App.ID, "app_xyz")
	}
	if len(id.App.Permissions) != 2 || id.App.Permissions[0] != "manage" || id.App.Permissions[1] != "view" {
		t.Errorf("app.permissions = %v, want [manage view]", id.App.Permissions)
	}
	if len(id.App.Roles) != 1 || id.App.Roles[0] != "editor" {
		t.Errorf("app.roles = %v, want [editor]", id.App.Roles)
	}

	if id.Attributes == nil {
		t.Fatal("attributes is nil, want non-nil")
	}
	// attributes.groups contains every system group the user belongs to plus
	// custom groups bound to this app that include the user. Custom groups bound
	// to other apps are excluded, and the resulting keys are sorted.
	if len(id.Attributes.Groups) != 3 || id.Attributes.Groups[0] != "admins" || id.Attributes.Groups[1] != "everyone" || id.Attributes.Groups[2] != "sales" {
		t.Errorf("attributes.groups = %v, want [admins everyone sales]", id.Attributes.Groups)
	}

	// Verify the fixture is valid JSON (sanity check for shared fixture).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(fixture, &raw); err != nil {
		t.Errorf("fixture is not valid JSON: %v", err)
	}
}
