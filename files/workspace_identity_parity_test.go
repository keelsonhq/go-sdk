package files

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type workspaceIdentityFixture struct {
	Cases []struct {
		Name        string  `json:"name"`
		WorkspaceID *string `json:"workspace_id"`
		TenantID    *string `json:"tenant_id"`
		Expected    *string `json:"expected"`
	} `json:"cases"`
}

func TestWorkspaceIdentityParity(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "workspace_identity_parity.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture workspaceIdentityFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			workspace, tenant := "", ""
			if tc.WorkspaceID != nil {
				workspace = *tc.WorkspaceID
			}
			if tc.TenantID != nil {
				tenant = *tc.TenantID
			}
			t.Setenv("KEELSON_WORKSPACE_ID", workspace)
			t.Setenv("KEELSON_TENANT_ID", tenant)
			want := ""
			if tc.Expected != nil {
				want = *tc.Expected
			}
			if got := workspaceID(); got != want {
				t.Fatalf("workspaceID() = %q, want %q", got, want)
			}
		})
	}
}
