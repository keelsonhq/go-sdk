// Package localmode provides shared helpers for KEELSON_LOCAL_MODE.
//
// When KEELSON_LOCAL_MODE is set to "1", "true", or "yes", SDK packages
// bypass HTTP calls and return deterministic fixture data. This mirrors
// the behavior of the Node and Python SDKs.
package localmode

import (
	"os"
	"strings"
)

// Enabled reports whether local mode is active.
func Enabled() bool {
	v := strings.TrimSpace(os.Getenv("KEELSON_LOCAL_MODE"))
	return v == "1" || v == "true" || v == "yes"
}

// Env reads an env var with a fallback default, trimming whitespace.
func Env(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

// Fixture user env defaults — matches Node/Python SDKs.
const (
	DefaultUserID     = "local-user-001"
	DefaultUserEmail  = "dev@localhost"
	DefaultUserName   = "Local Developer"
	DefaultTenantID   = "local-tenant-001"
	DefaultTenantRole = "OWNER"
	DefaultAppID      = "local-app-001"
)

// UserID returns the configured local user ID.
func UserID() string { return Env("KEELSON_LOCAL_USER_ID", DefaultUserID) }

// UserEmail returns the configured local user email.
func UserEmail() string { return Env("KEELSON_LOCAL_USER_EMAIL", DefaultUserEmail) }

// UserName returns the configured local user name.
func UserName() string { return Env("KEELSON_LOCAL_USER_NAME", DefaultUserName) }

// TenantID returns the configured local workspace ID, falling back to its legacy alias.
func TenantID() string {
	if value := strings.TrimSpace(os.Getenv("KEELSON_LOCAL_WORKSPACE_ID")); value != "" {
		return value
	}
	return Env("KEELSON_LOCAL_TENANT_ID", DefaultTenantID)
}

// TenantRole returns the configured local workspace role, falling back to its legacy alias.
func TenantRole() string {
	if value := strings.TrimSpace(os.Getenv("KEELSON_LOCAL_WORKSPACE_ROLE")); value != "" {
		return value
	}
	return Env("KEELSON_LOCAL_TENANT_ROLE", DefaultTenantRole)
}

// AppID returns the configured local app ID.
func AppID() string { return Env("KEELSON_LOCAL_APP_ID", DefaultAppID) }

// RoleGroups returns the system groups for a given tenant role.
// Matches the Node/Python SDK role-to-group mapping.
var roleGroupMap = map[string][]string{
	"OWNER":    {"everyone", "developers", "admins", "owners"},
	"ADMIN":    {"everyone", "developers", "admins"},
	"BUILDER":  {"everyone", "developers"},
	"APP_USER": {"everyone"},
}

// GroupsForRole returns the system groups associated with a tenant role.
func GroupsForRole(role string) []string {
	if g, ok := roleGroupMap[role]; ok {
		return g
	}
	return []string{"everyone"}
}

// groupRoleMap maps a group key to the set of roles that belong to it.
var groupRoleMap = map[string]map[string]bool{
	"everyone":   {"OWNER": true, "ADMIN": true, "BUILDER": true, "APP_USER": true},
	"developers": {"OWNER": true, "ADMIN": true, "BUILDER": true},
	"admins":     {"OWNER": true, "ADMIN": true},
	"owners":     {"OWNER": true},
}

// RoleInGroup reports whether a role belongs to the given system group.
func RoleInGroup(role, groupKey string) bool {
	roles, ok := groupRoleMap[groupKey]
	if !ok {
		return false
	}
	return roles[role]
}

// FixtureMember is a local mode member.
type FixtureMember struct {
	ID    string
	Email string
	Name  string
	Role  string
}

// CompanionMembers are the built-in non-configurable local members.
var CompanionMembers = []FixtureMember{
	{ID: "local-user-002", Email: "alice@localhost", Name: "Alice (local)", Role: "ADMIN"},
	{ID: "local-user-003", Email: "bob@localhost", Name: "Bob (local)", Role: "BUILDER"},
	{ID: "local-user-004", Email: "carol@localhost", Name: "Carol (local)", Role: "APP_USER"},
}

// AllMembers returns the env-configured user plus companions (excluding
// companions whose ID collides with the env user).
func AllMembers() []FixtureMember {
	uid := UserID()
	primary := FixtureMember{
		ID:    uid,
		Email: UserEmail(),
		Name:  UserName(),
		Role:  TenantRole(),
	}
	members := []FixtureMember{primary}
	for _, c := range CompanionMembers {
		if c.ID != uid {
			members = append(members, c)
		}
	}
	return members
}

// FixtureGroup is a local mode group definition.
//
// Key is the canonical, immutable identifier referenced by application code.
// ID is an opaque identifier for machine integration.
type FixtureGroup struct {
	ID          string
	Key         string
	DisplayName string
	Kind        string
	SystemKind  *string
}

func strPtr(s string) *string { return &s }

// FixtureGroups are the built-in system groups.
var FixtureGroups = []FixtureGroup{
	{ID: "local-group-everyone", Key: "everyone", DisplayName: "Everyone", Kind: "SYSTEM", SystemKind: strPtr("everyone")},
	{ID: "local-group-developers", Key: "developers", DisplayName: "Developers", Kind: "SYSTEM", SystemKind: strPtr("developers")},
	{ID: "local-group-admins", Key: "admins", DisplayName: "Admins", Kind: "SYSTEM", SystemKind: strPtr("admins")},
	{ID: "local-group-owners", Key: "owners", DisplayName: "Owners", Kind: "SYSTEM", SystemKind: strPtr("owners")},
}

// GroupKeyByID resolves a fixture group ID to its key. Returns ("", false)
// when no fixture group has that ID.
func GroupKeyByID(id string) (string, bool) {
	for _, g := range FixtureGroups {
		if g.ID == id {
			return g.Key, true
		}
	}
	return "", false
}
