package directory_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelsonhq/go-sdk/directory"
	"github.com/keelsonhq/go-sdk/internal/testfixtures"
)

// TestParity_Members parses the shared parity fixture and asserts
// the same semantic field values that Node and Python parity tests assert.
func TestParity_Members(t *testing.T) {
	fixture, err := testfixtures.ReadFile("identity_members.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture)
	}))
	defer ts.Close()

	t.Setenv("KEELSON_LOCAL_MODE", "")
	client, err := directory.New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := client.ListMembers(nil)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}

	// --- Cross-language parity assertions ---
	if result.Limit != 25 {
		t.Errorf("limit = %d, want 25", result.Limit)
	}
	if result.Offset != 0 {
		t.Errorf("offset = %d, want 0", result.Offset)
	}
	if result.NextOffset != nil {
		t.Errorf("next_offset = %v, want nil", result.NextOffset)
	}
	if len(result.Items) != 3 {
		t.Fatalf("items length = %d, want 3", len(result.Items))
	}

	alice := result.Items[0]
	if alice.ID != "usr_m01" {
		t.Errorf("items[0].id = %q, want %q", alice.ID, "usr_m01")
	}
	if alice.Email != "alice@example.com" {
		t.Errorf("items[0].email = %q, want %q", alice.Email, "alice@example.com")
	}
	if alice.Name != "Alice" {
		t.Errorf("items[0].name = %q, want %q", alice.Name, "Alice")
	}
	if alice.Role != "admin" {
		t.Errorf("items[0].role = %q, want %q", alice.Role, "admin")
	}
	if alice.ImageURL == nil || *alice.ImageURL != "https://img.clerk.com/parity-alice" {
		t.Errorf("items[0].image_url = %v, want %q", alice.ImageURL, "https://img.clerk.com/parity-alice")
	}

	bob := result.Items[1]
	if bob.ID != "usr_m02" {
		t.Errorf("items[1].id = %q, want %q", bob.ID, "usr_m02")
	}
	if bob.Role != "" {
		t.Errorf("items[1].role = %q, want empty string", bob.Role)
	}
	if bob.ImageURL != nil {
		t.Errorf("items[1].image_url = %q, want nil", *bob.ImageURL)
	}

	// Key absent (older gateway) parses to nil, same as an explicit null.
	carol := result.Items[2]
	if carol.ID != "usr_m03" {
		t.Errorf("items[2].id = %q, want %q", carol.ID, "usr_m03")
	}
	if carol.ImageURL != nil {
		t.Errorf("items[2].image_url = %q, want nil", *carol.ImageURL)
	}
}

// TestParity_Groups parses the shared parity fixture.
func TestParity_Groups(t *testing.T) {
	fixture, err := testfixtures.ReadFile("identity_groups.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture)
	}))
	defer ts.Close()

	t.Setenv("KEELSON_LOCAL_MODE", "")
	client, err := directory.New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	groups, err := client.ListGroups()
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}

	// --- Cross-language parity assertions ---
	if len(groups) != 3 {
		t.Fatalf("groups length = %d, want 3", len(groups))
	}

	admins := groups[0]
	if admins.ID != "grp_admins01" {
		t.Errorf("groups[0].id = %q, want %q", admins.ID, "grp_admins01")
	}
	if admins.Key == nil || *admins.Key != "admins" {
		t.Errorf("groups[0].key = %v, want %q", admins.Key, "admins")
	}
	if admins.DisplayName != "Administrators" {
		t.Errorf("groups[0].display_name = %q, want %q", admins.DisplayName, "Administrators")
	}
	if admins.Kind != "system" {
		t.Errorf("groups[0].kind = %q, want %q", admins.Kind, "system")
	}
	if admins.SystemKind == nil || *admins.SystemKind != "admin" {
		t.Errorf("groups[0].system_kind = %v, want %q", admins.SystemKind, "admin")
	}

	editors := groups[1]
	if editors.Key == nil || *editors.Key != "editors" {
		t.Errorf("groups[1].key = %v, want %q", editors.Key, "editors")
	}
	if editors.SystemKind != nil {
		t.Errorf("groups[1].system_kind = %v, want nil", editors.SystemKind)
	}

	// A keyless group carries a stable id but key is null.
	keyless := groups[2]
	if keyless.ID != "grp_keyless01" {
		t.Errorf("groups[2].id = %q, want %q", keyless.ID, "grp_keyless01")
	}
	if keyless.Key != nil {
		t.Errorf("groups[2].key = %v, want nil", keyless.Key)
	}
}
