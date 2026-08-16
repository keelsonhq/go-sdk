package directory_test

import (
	"errors"
	"testing"

	"github.com/keelsonhq/go-sdk/directory"
	"github.com/keelsonhq/go-sdk/internal/httpclient"
)

func TestNew_LocalMode(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "true")
	t.Setenv("KEELSON_IDENTITY_BASE_URL", "")

	client, err := directory.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !client.IsLocal() {
		t.Error("expected local mode")
	}
}

func TestListMembers_LocalMode_Default(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	t.Setenv("KEELSON_LOCAL_USER_ID", "")

	client, _ := directory.New("")

	result, err := client.ListMembers(nil)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}

	if len(result.Items) != 4 {
		t.Fatalf("Items count = %d, want 4", len(result.Items))
	}
	if result.Items[0].ID != "local-user-001" {
		t.Errorf("first member ID = %q, want %q", result.Items[0].ID, "local-user-001")
	}
	if result.Limit != 25 {
		t.Errorf("Limit = %d, want 25", result.Limit)
	}
	if result.Offset != 0 {
		t.Errorf("Offset = %d, want 0", result.Offset)
	}
	if result.NextOffset != nil {
		t.Errorf("NextOffset = %v, want nil", result.NextOffset)
	}
}

func TestListMembers_LocalMode_FilterByQ(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	t.Setenv("KEELSON_LOCAL_USER_ID", "")

	client, _ := directory.New("")

	result, err := client.ListMembers(&directory.ListMembersParams{Q: "alice"})
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("Items count = %d, want 1 for q=alice", len(result.Items))
	}
	if result.Items[0].Name != "Alice (local)" {
		t.Errorf("Name = %q, want %q", result.Items[0].Name, "Alice (local)")
	}
}

func TestListMembers_LocalMode_FilterByEmail(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	t.Setenv("KEELSON_LOCAL_USER_ID", "")

	client, _ := directory.New("")

	result, err := client.ListMembers(&directory.ListMembersParams{Q: "bob@"})
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("Items count = %d, want 1 for q=bob@", len(result.Items))
	}
	if result.Items[0].ID != "local-user-003" {
		t.Errorf("ID = %q, want %q", result.Items[0].ID, "local-user-003")
	}
}

func TestListMembers_LocalMode_FilterByRole(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	t.Setenv("KEELSON_LOCAL_USER_ID", "")

	client, _ := directory.New("")

	result, err := client.ListMembers(&directory.ListMembersParams{Role: "ADMIN"})
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("Items count = %d, want 1 for role=ADMIN", len(result.Items))
	}
	if result.Items[0].Name != "Alice (local)" {
		t.Errorf("Name = %q", result.Items[0].Name)
	}
}

func TestListMembers_LocalMode_FilterByGroupKey(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	t.Setenv("KEELSON_LOCAL_USER_ID", "")

	client, _ := directory.New("")

	// "owners" group should only contain OWNER role.
	result, err := client.ListMembers(&directory.ListMembersParams{GroupKey: "owners"})
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("Items count = %d, want 1 for group_key=owners", len(result.Items))
	}
	if result.Items[0].Role != "OWNER" {
		t.Errorf("Role = %q, want OWNER", result.Items[0].Role)
	}

	// "admins" group should contain OWNER and ADMIN.
	result2, _ := client.ListMembers(&directory.ListMembersParams{GroupKey: "admins"})
	if len(result2.Items) != 2 {
		t.Errorf("Items count = %d, want 2 for group_key=admins", len(result2.Items))
	}

	// "everyone" should have all members.
	result3, _ := client.ListMembers(&directory.ListMembersParams{GroupKey: "everyone"})
	if len(result3.Items) != 4 {
		t.Errorf("Items count = %d, want 4 for group_key=everyone", len(result3.Items))
	}

	// Unknown group should return empty.
	result4, _ := client.ListMembers(&directory.ListMembersParams{GroupKey: "nonexistent"})
	if len(result4.Items) != 0 {
		t.Errorf("Items count = %d, want 0 for unknown group", len(result4.Items))
	}
}

func TestListMembers_LocalMode_FilterByGroupID(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	t.Setenv("KEELSON_LOCAL_USER_ID", "")

	client, _ := directory.New("")

	// group_id resolves to the same fixture group as its key.
	result, err := client.ListMembers(&directory.ListMembersParams{GroupID: "local-group-owners"})
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Role != "OWNER" {
		t.Errorf("group_id=local-group-owners: got %d items, want 1 OWNER", len(result.Items))
	}

	// Unknown group_id matches no group.
	resultUnknown, _ := client.ListMembers(&directory.ListMembersParams{GroupID: "no-such-id"})
	if len(resultUnknown.Items) != 0 {
		t.Errorf("unknown group_id: got %d items, want 0", len(resultUnknown.Items))
	}
}

func TestListMembers_RejectsGroupIDAndGroupKeyTogether(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")

	client, _ := directory.New("")

	_, err := client.ListMembers(&directory.ListMembersParams{
		GroupID:  "local-group-owners",
		GroupKey: "owners",
	})
	if err == nil {
		t.Fatal("expected error when both GroupID and GroupKey are set, got nil")
	}
}

func TestListMembers_LocalMode_Pagination(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	t.Setenv("KEELSON_LOCAL_USER_ID", "")

	client, _ := directory.New("")

	limit := 2
	result, err := client.ListMembers(&directory.ListMembersParams{Limit: &limit})
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("Items count = %d, want 2", len(result.Items))
	}
	if result.NextOffset == nil || *result.NextOffset != 2 {
		t.Errorf("NextOffset = %v, want 2", result.NextOffset)
	}

	// Second page.
	offset := 2
	result2, _ := client.ListMembers(&directory.ListMembersParams{Limit: &limit, Offset: &offset})
	if len(result2.Items) != 2 {
		t.Fatalf("Items count = %d, want 2", len(result2.Items))
	}
	if result2.NextOffset != nil {
		t.Errorf("NextOffset = %v, want nil (last page)", result2.NextOffset)
	}
}

func TestListMembers_LocalMode_NegativeLimit(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	t.Setenv("KEELSON_LOCAL_USER_ID", "")

	client, _ := directory.New("")

	neg := -1
	result, err := client.ListMembers(&directory.ListMembersParams{Limit: &neg})
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	// Negative limit is ignored, falls back to default 25.
	if result.Limit != 25 {
		t.Errorf("Limit = %d, want 25 (default)", result.Limit)
	}
	if len(result.Items) != 4 {
		t.Errorf("Items count = %d, want 4", len(result.Items))
	}
}

func TestListMembers_LocalMode_NegativeOffset(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	t.Setenv("KEELSON_LOCAL_USER_ID", "")

	client, _ := directory.New("")

	neg := -1
	result, err := client.ListMembers(&directory.ListMembersParams{Offset: &neg})
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	// Negative offset is ignored, falls back to 0.
	if result.Offset != 0 {
		t.Errorf("Offset = %d, want 0 (default)", result.Offset)
	}
	if len(result.Items) != 4 {
		t.Errorf("Items count = %d, want 4", len(result.Items))
	}
}

func TestGetUser_LocalMode_Found(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	t.Setenv("KEELSON_LOCAL_USER_ID", "")

	client, _ := directory.New("")

	// Default env user.
	user, err := client.GetUser("local-user-001")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.Name != "Local Developer" {
		t.Errorf("Name = %q, want %q", user.Name, "Local Developer")
	}

	// Companion member.
	user2, err := client.GetUser("local-user-002")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user2.Name != "Alice (local)" {
		t.Errorf("Name = %q, want %q", user2.Name, "Alice (local)")
	}
}

func TestGetUser_LocalMode_CustomEnvUser(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")
	t.Setenv("KEELSON_LOCAL_USER_ID", "my-dev")
	t.Setenv("KEELSON_LOCAL_USER_NAME", "Dev Person")

	client, _ := directory.New("")

	user, err := client.GetUser("my-dev")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.Name != "Dev Person" {
		t.Errorf("Name = %q, want %q", user.Name, "Dev Person")
	}
}

func TestGetUser_LocalMode_NotFound(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")

	client, _ := directory.New("")

	_, err := client.GetUser("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}
	// Should return *httpclient.APIError with 404, matching remote behavior.
	var apiErr *httpclient.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

func TestGetUser_LocalMode_EmptyID(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")

	client, _ := directory.New("")

	_, err := client.GetUser("")
	if err == nil {
		t.Fatal("expected error for empty user ID")
	}
}

func TestListGroups_LocalMode(t *testing.T) {
	t.Setenv("KEELSON_LOCAL_MODE", "1")

	client, _ := directory.New("")

	groups, err := client.ListGroups()
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 4 {
		t.Fatalf("Groups count = %d, want 4", len(groups))
	}

	expected := []string{"everyone", "developers", "admins", "owners"}
	for i, key := range expected {
		if groups[i].Key == nil || *groups[i].Key != key {
			t.Errorf("groups[%d].Key = %v, want %q", i, groups[i].Key, key)
		}
		if groups[i].ID == "" {
			t.Errorf("groups[%d].ID is empty, want a stable identifier", i)
		}
		if groups[i].Kind != "SYSTEM" {
			t.Errorf("groups[%d].Kind = %q, want SYSTEM", i, groups[i].Kind)
		}
		if groups[i].SystemKind == nil || *groups[i].SystemKind != key {
			t.Errorf("groups[%d].SystemKind = %v, want %q", i, groups[i].SystemKind, key)
		}
	}
}
