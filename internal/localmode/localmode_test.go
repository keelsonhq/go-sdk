package localmode

import (
	"testing"
)

func TestEnabled(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"1", true},
		{"true", true},
		{"yes", true},
		{"  true  ", true},
		{"0", false},
		{"false", false},
		{"", false},
		{"TRUE", false}, // case-sensitive, matching Node/Python
		{"YES", false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv("KEELSON_LOCAL_MODE", tt.value)
			if got := Enabled(); got != tt.want {
				t.Errorf("Enabled() = %v, want %v for %q", got, tt.want, tt.value)
			}
		})
	}
}

func TestEnabled_Unset(t *testing.T) {
	// Don't set the env var at all.
	t.Setenv("KEELSON_LOCAL_MODE", "")
	if Enabled() {
		t.Error("Enabled() = true when KEELSON_LOCAL_MODE is empty")
	}
}

func TestGroupsForRole(t *testing.T) {
	tests := []struct {
		role  string
		count int
	}{
		{"OWNER", 4},
		{"ADMIN", 3},
		{"BUILDER", 2},
		{"APP_USER", 1},
		{"UNKNOWN", 1}, // fallback to ["everyone"]
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			groups := GroupsForRole(tt.role)
			if len(groups) != tt.count {
				t.Errorf("GroupsForRole(%q) = %v (len %d), want len %d", tt.role, groups, len(groups), tt.count)
			}
		})
	}
}

func TestRoleInGroup(t *testing.T) {
	if !RoleInGroup("OWNER", "owners") {
		t.Error("OWNER should be in owners")
	}
	if RoleInGroup("ADMIN", "owners") {
		t.Error("ADMIN should not be in owners")
	}
	if !RoleInGroup("BUILDER", "developers") {
		t.Error("BUILDER should be in developers")
	}
	if RoleInGroup("APP_USER", "developers") {
		t.Error("APP_USER should not be in developers")
	}
	if !RoleInGroup("APP_USER", "everyone") {
		t.Error("APP_USER should be in everyone")
	}
	if RoleInGroup("OWNER", "nonexistent") {
		t.Error("no role should be in nonexistent group")
	}
}

func TestAllMembers_Default(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_USER_ID", "")
	members := AllMembers()
	if len(members) != 4 {
		t.Fatalf("AllMembers() len = %d, want 4", len(members))
	}
	if members[0].ID != DefaultUserID {
		t.Errorf("first member ID = %q, want %q", members[0].ID, DefaultUserID)
	}
}

func TestAllMembers_CustomID_NoCollision(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_USER_ID", "custom-user")
	members := AllMembers()
	if len(members) != 4 {
		t.Fatalf("AllMembers() len = %d, want 4", len(members))
	}
	if members[0].ID != "custom-user" {
		t.Errorf("first member ID = %q, want %q", members[0].ID, "custom-user")
	}
}

func TestAllMembers_CustomID_Collision(t *testing.T) {
	// Set the custom user ID to collide with a companion member.
	t.Setenv("KEELSON_LOCAL_USER_ID", "local-user-002")
	members := AllMembers()
	// The companion "local-user-002" should be excluded, leaving 3 total.
	if len(members) != 3 {
		t.Fatalf("AllMembers() len = %d, want 3 (collision excluded)", len(members))
	}
	if members[0].ID != "local-user-002" {
		t.Errorf("first member ID = %q, want %q", members[0].ID, "local-user-002")
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_USER_ID", "uid")
	t.Setenv("KEELSON_LOCAL_USER_EMAIL", "e@e.com")
	t.Setenv("KEELSON_LOCAL_USER_NAME", "Emu")
	t.Setenv("KEELSON_LOCAL_TENANT_ID", "tid")
	t.Setenv("KEELSON_LOCAL_TENANT_ROLE", "ADMIN")
	t.Setenv("KEELSON_LOCAL_APP_ID", "aid")

	if UserID() != "uid" {
		t.Errorf("UserID() = %q", UserID())
	}
	if UserEmail() != "e@e.com" {
		t.Errorf("UserEmail() = %q", UserEmail())
	}
	if UserName() != "Emu" {
		t.Errorf("UserName() = %q", UserName())
	}
	if TenantID() != "tid" {
		t.Errorf("TenantID() = %q", TenantID())
	}
	if TenantRole() != "ADMIN" {
		t.Errorf("TenantRole() = %q", TenantRole())
	}
	if AppID() != "aid" {
		t.Errorf("AppID() = %q", AppID())
	}
}

func TestWorkspaceEnvOverridesTenantAlias(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_WORKSPACE_ID", "workspace-id")
	t.Setenv("KEELSON_LOCAL_TENANT_ID", "tenant-id")
	t.Setenv("KEELSON_LOCAL_WORKSPACE_ROLE", "OWNER")
	t.Setenv("KEELSON_LOCAL_TENANT_ROLE", "BUILDER")

	if TenantID() != "workspace-id" {
		t.Errorf("TenantID() = %q", TenantID())
	}
	if TenantRole() != "OWNER" {
		t.Errorf("TenantRole() = %q", TenantRole())
	}
}
