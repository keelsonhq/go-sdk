package identity_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/keelsonhq/go-sdk/identity"
)

// TestContract_GetCurrentIdentity is a thin contract test that calls the real
// Keelson auth gateway endpoint. It is skipped unless KEELSON_CONTRACT_TEST_URL,
// KEELSON_CONTRACT_TEST_USER_ID, and KEELSON_CONTRACT_TEST_APP_TOKEN are set.
//
// Run: KEELSON_CONTRACT_TEST_URL=http://localhost:8787 \
//
//	KEELSON_CONTRACT_TEST_USER_ID="..." \
//	KEELSON_CONTRACT_TEST_APP_TOKEN="keelson_..." \
//	go test ./identity/ -run TestContract -v
func TestContract_GetCurrentIdentity(t *testing.T) {
	baseURL := os.Getenv("KEELSON_CONTRACT_TEST_URL")
	userID := os.Getenv("KEELSON_CONTRACT_TEST_USER_ID")
	appToken := os.Getenv("KEELSON_CONTRACT_TEST_APP_TOKEN")
	if baseURL == "" || userID == "" || appToken == "" {
		t.Skip("skipping contract test: set KEELSON_CONTRACT_TEST_URL, KEELSON_CONTRACT_TEST_USER_ID, and KEELSON_CONTRACT_TEST_APP_TOKEN")
	}

	client, err := identity.New(baseURL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	user, err := client.GetCurrentUser(identity.WithHeaders(http.Header{
		"X-Keelson-User-Id": {userID},
	}))
	if err != nil {
		t.Fatalf("GetCurrentUser: %v", err)
	}
	if user.ID == "" {
		t.Error("user.id is empty")
	}

	ci, err := client.GetCurrentIdentity(
		identity.WithHeaders(http.Header{"X-Keelson-User-Id": {userID}}),
		identity.WithAppToken(appToken),
	)
	if err != nil {
		t.Fatalf("GetCurrentIdentity: %v", err)
	}

	// Validate that the gateway returned every required identity field.
	if ci.User.ID == "" {
		t.Error("user.id is empty")
	}
	if ci.Tenant.ID == "" {
		t.Error("tenant.id is empty")
	}
	if ci.Tenant.Role == "" {
		t.Error("tenant.role is empty")
	}
	if ci.App.ID == "" {
		t.Error("app.id is empty")
	}

	t.Logf("user.id=%s tenant.id=%s tenant.role=%s app.id=%s",
		ci.User.ID, ci.Tenant.ID, ci.Tenant.Role, ci.App.ID)
}
