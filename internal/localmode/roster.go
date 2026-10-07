package localmode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// DefaultUsersFile is the users file read in local mode when
// KEELSON_LOCAL_USERS_FILE is not set and the file exists.
const DefaultUsersFile = ".keelson/dev-users.json"

// ProductionMarks returns the names of the Keelson deployment marks present
// in the environment, in the canonical order. KEELSON_MODE is reported as
// "KEELSON_MODE=keelson". Values are never returned.
func ProductionMarks() []string {
	var marks []string
	if strings.EqualFold(strings.TrimSpace(os.Getenv("KEELSON_MODE")), "keelson") {
		marks = append(marks, "KEELSON_MODE=keelson")
	}
	for _, name := range []string{
		"KEELSON_APP_ID",
		"KEELSON_WORKSPACE_ID",
		"KEELSON_TENANT_ID",
		"KEELSON_DEPLOY_ID",
		"KEELSON_APP_URL",
	} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			marks = append(marks, name)
		}
	}
	return marks
}

// RosterUser is one entry of the local users file, with perms normalized to
// ["view"] or ["view", "manage"].
type RosterUser struct {
	ID       string
	Email    string
	Name     string
	Perms    []string
	ImageURL *string
}

// CanManage reports whether the user has the manage permission.
func (u RosterUser) CanManage() bool {
	return len(u.Perms) == 2
}

// Role is the workspace role derived from perms.
func (u RosterUser) Role() string {
	if u.CanManage() {
		return "ADMIN"
	}
	return "APP_USER"
}

// Groups returns the system group keys of the user, sorted.
func (u RosterUser) Groups() []string {
	if u.CanManage() {
		return []string{"admins", "everyone"}
	}
	return []string{"everyone"}
}

// InRosterGroup reports whether a roster member with the given role belongs
// to the group key. Only "everyone" and "admins" exist in a roster.
func InRosterGroup(role, groupKey string) bool {
	switch groupKey {
	case "everyone":
		return true
	case "admins":
		return role == "ADMIN"
	}
	return false
}

// RosterGroups are the system groups served when a users file is in use, in
// the order the Directory API returns them.
var RosterGroups = []FixtureGroup{
	{ID: "local-group-admins", Key: "admins", DisplayName: "Admins", Kind: "SYSTEM", SystemKind: strPtr("admins")},
	{ID: "local-group-everyone", Key: "everyone", DisplayName: "Everyone", Kind: "SYSTEM", SystemKind: strPtr("everyone")},
}

// FixedUser returns the first user with manage, or the first user.
func FixedUser(users []RosterUser) RosterUser {
	for _, u := range users {
		if u.CanManage() {
			return u
		}
	}
	return users[0]
}

// Setup is called when a client is created in local mode. It refuses to run
// when a Keelson deployment mark is present and loads the users file. It
// returns nil users when no users file is in use.
func Setup() ([]RosterUser, error) {
	if marks := ProductionMarks(); len(marks) > 0 {
		return nil, fmt.Errorf(
			"KEELSON_LOCAL_MODE is set, but this is a Keelson deployment (%s). "+
				"Local mode is for local development only; unset KEELSON_LOCAL_MODE.",
			strings.Join(marks, ", "))
	}
	return loadUsersFile()
}

func loadUsersFile() ([]RosterUser, error) {
	path := strings.TrimSpace(os.Getenv("KEELSON_LOCAL_USERS_FILE"))
	if path == "" {
		if _, err := os.Stat(DefaultUsersFile); errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		path = DefaultUsersFile
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("KEELSON_LOCAL_MODE users file: %w", err)
	}
	users, err := ParseRoster(data)
	if err != nil {
		return nil, fmt.Errorf("KEELSON_LOCAL_MODE users file %s: %w", path, err)
	}
	return users, nil
}

// ParseRoster parses and validates the users file JSON.
func ParseRoster(data []byte) ([]RosterUser, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil || top == nil {
		return nil, fmt.Errorf("must be a JSON object with \"users\"")
	}
	var items []json.RawMessage
	if raw, ok := top["users"]; !ok || json.Unmarshal(raw, &items) != nil {
		return nil, fmt.Errorf("\"users\" must be an array")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("\"users\" must not be empty")
	}
	users := make([]RosterUser, 0, len(items))
	seen := map[string]bool{}
	for i, item := range items {
		u, err := parseRosterUser(item)
		if err != nil {
			return nil, fmt.Errorf("users[%d]: %w", i, err)
		}
		if seen[u.ID] {
			return nil, fmt.Errorf("users[%d]: duplicate id %q", i, u.ID)
		}
		seen[u.ID] = true
		users = append(users, u)
	}
	return users, nil
}

func parseRosterUser(data json.RawMessage) (RosterUser, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return RosterUser{}, fmt.Errorf("must be an object")
	}
	var u RosterUser
	for key, dst := range map[string]*string{"id": &u.ID, "email": &u.Email, "name": &u.Name} {
		if !decodeNonNull(fields[key], dst) {
			return RosterUser{}, fmt.Errorf("%q must be a string", key)
		}
	}
	if u.ID == "" {
		return RosterUser{}, fmt.Errorf("\"id\" must not be empty")
	}
	var perms []string
	if !decodeNonNull(fields["perms"], &perms) {
		return RosterUser{}, fmt.Errorf("\"perms\" must be an array of strings")
	}
	normalized, err := normalizePerms(perms)
	if err != nil {
		return RosterUser{}, err
	}
	u.Perms = normalized
	if raw := fields["image_url"]; raw != nil && !isNull(raw) {
		var image string
		if json.Unmarshal(raw, &image) != nil {
			return RosterUser{}, fmt.Errorf("\"image_url\" must be a string or null")
		}
		u.ImageURL = &image
	}
	return u, nil
}

func normalizePerms(perms []string) ([]string, error) {
	hasView, hasManage := false, false
	for _, p := range perms {
		switch {
		case p == "view" && !hasView:
			hasView = true
		case p == "manage" && !hasManage:
			hasManage = true
		default:
			return nil, fmt.Errorf("\"perms\" must be distinct values of \"view\" and \"manage\"")
		}
	}
	if !hasView {
		return nil, fmt.Errorf("\"perms\" must include \"view\"")
	}
	if hasManage {
		return []string{"view", "manage"}, nil
	}
	return []string{"view"}, nil
}

// decodeNonNull decodes a present, non-null JSON value into dst.
func decodeNonNull(raw json.RawMessage, dst any) bool {
	if raw == nil || isNull(raw) {
		return false
	}
	return json.Unmarshal(raw, dst) == nil
}

func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
