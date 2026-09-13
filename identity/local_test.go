package identity_test

import (
	"testing"

	"github.com/keelsonhq/go-sdk/identity"
)

func TestNew_LocalMode(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "true")
	t.Setenv("KEELSON_IDENTITY_BASE_URL", "") // not needed in local mode

	client, err := identity.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !client.IsLocal() {
		t.Error("expected local mode")
	}
}

func TestNew_LocalMode_IgnoresBaseURL(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	// Even without KEELSON_IDENTITY_BASE_URL, local mode should work.
	t.Setenv("KEELSON_IDENTITY_BASE_URL", "")

	_, err := identity.New("")
	if err != nil {
		t.Fatalf("New should not fail in local mode: %v", err)
	}
}

func TestGetCurrentUser_LocalMode_Defaults(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "yes")
	t.Setenv("KEELSON_LOCAL_USER_ID", "")
	t.Setenv("KEELSON_LOCAL_USER_EMAIL", "")
	t.Setenv("KEELSON_LOCAL_USER_NAME", "")
	t.Setenv("KEELSON_LOCAL_TENANT_ID", "")
	t.Setenv("KEELSON_LOCAL_TENANT_ROLE", "")
	t.Setenv("KEELSON_LOCAL_APP_ID", "")

	client, err := identity.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	me, err := client.GetCurrentUser()
	if err != nil {
		t.Fatalf("GetCurrentUser: %v", err)
	}

	if me.ID != "local-user-001" {
		t.Errorf("ID = %q, want %q", me.ID, "local-user-001")
	}
	if me.Email == nil || *me.Email != "dev@localhost" {
		t.Errorf("Email = %v, want %q", me.Email, "dev@localhost")
	}
	if me.Name == nil || *me.Name != "Local Developer" {
		t.Errorf("Name = %v, want %q", me.Name, "Local Developer")
	}
}

func TestGetCurrentIdentity_LocalMode_Defaults(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "yes")
	t.Setenv("KEELSON_LOCAL_USER_ID", "")
	t.Setenv("KEELSON_LOCAL_USER_EMAIL", "")
	t.Setenv("KEELSON_LOCAL_USER_NAME", "")
	t.Setenv("KEELSON_LOCAL_TENANT_ID", "")
	t.Setenv("KEELSON_LOCAL_TENANT_ROLE", "")
	t.Setenv("KEELSON_LOCAL_APP_ID", "")

	client, err := identity.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	me, err := client.GetCurrentIdentity()
	if err != nil {
		t.Fatalf("GetCurrentIdentity: %v", err)
	}

	if me.User.ID != "local-user-001" {
		t.Errorf("User.ID = %q, want %q", me.User.ID, "local-user-001")
	}
	if me.Tenant.ID != "local-tenant-001" {
		t.Errorf("Tenant.ID = %q, want %q", me.Tenant.ID, "local-tenant-001")
	}
	if me.Tenant.Role != "OWNER" {
		t.Errorf("Tenant.Role = %q, want %q", me.Tenant.Role, "OWNER")
	}
	if me.Workspace != me.Tenant {
		t.Errorf("Workspace = %#v, Tenant alias = %#v", me.Workspace, me.Tenant)
	}
	if me.App.ID != "local-app-001" {
		t.Errorf("App.ID = %q, want %q", me.App.ID, "local-app-001")
	}
	if len(me.App.Permissions) != 2 {
		t.Errorf("App.Permissions = %v, want [manage, view]", me.App.Permissions)
	}
	if len(me.App.Roles) != 0 {
		t.Errorf("App.Roles = %v, want []", me.App.Roles)
	}
	if me.Attributes == nil || len(me.Attributes.Groups) != 4 {
		t.Errorf("Attributes.Groups = %v, want 4 groups for OWNER", me.Attributes)
	}
}

func TestGetCurrentUser_LocalMode_CustomEnv(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	t.Setenv("KEELSON_LOCAL_USER_ID", "custom-id")
	t.Setenv("KEELSON_LOCAL_USER_EMAIL", "custom@test.com")
	t.Setenv("KEELSON_LOCAL_USER_NAME", "Custom User")
	t.Setenv("KEELSON_LOCAL_TENANT_ID", "tenant-42")
	t.Setenv("KEELSON_LOCAL_TENANT_ROLE", "ADMIN")
	t.Setenv("KEELSON_LOCAL_APP_ID", "app-99")

	client, err := identity.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	me, err := client.GetCurrentUser()
	if err != nil {
		t.Fatalf("GetCurrentUser: %v", err)
	}

	if me.ID != "custom-id" {
		t.Errorf("ID = %q, want %q", me.ID, "custom-id")
	}
	if *me.Email != "custom@test.com" {
		t.Errorf("Email = %q, want %q", *me.Email, "custom@test.com")
	}
}

func TestGetCurrentIdentity_LocalMode_CustomEnv(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	t.Setenv("KEELSON_LOCAL_USER_ID", "custom-id")
	t.Setenv("KEELSON_LOCAL_USER_EMAIL", "custom@test.com")
	t.Setenv("KEELSON_LOCAL_USER_NAME", "Custom User")
	t.Setenv("KEELSON_LOCAL_TENANT_ID", "tenant-42")
	t.Setenv("KEELSON_LOCAL_TENANT_ROLE", "ADMIN")
	t.Setenv("KEELSON_LOCAL_APP_ID", "app-99")

	client, err := identity.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	me, err := client.GetCurrentIdentity()
	if err != nil {
		t.Fatalf("GetCurrentIdentity: %v", err)
	}

	if me.User.ID != "custom-id" {
		t.Errorf("User.ID = %q, want %q", me.User.ID, "custom-id")
	}
	if *me.User.Email != "custom@test.com" {
		t.Errorf("User.Email = %q, want %q", *me.User.Email, "custom@test.com")
	}
	if me.Tenant.Role != "ADMIN" {
		t.Errorf("Tenant.Role = %q, want %q", me.Tenant.Role, "ADMIN")
	}
	// ADMIN should have 3 groups: everyone, developers, admins
	if me.Attributes == nil || len(me.Attributes.Groups) != 3 {
		t.Errorf("Attributes.Groups = %v, want 3 groups for ADMIN", me.Attributes)
	}
}

func TestGetCurrentUser_LocalMode_IgnoresRequestOptions(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	t.Setenv("KEELSON_LOCAL_USER_ID", "")

	client, err := identity.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Request options are ignored in local mode — no HTTP call is made.
	me, err := client.GetCurrentUser(
		identity.WithCookie("session=abc"),
		identity.WithHost("app.example.com"),
	)
	if err != nil {
		t.Fatalf("GetCurrentUser: %v", err)
	}
	if me.ID != "local-user-001" {
		t.Errorf("ID = %q, want default", me.ID)
	}
}
