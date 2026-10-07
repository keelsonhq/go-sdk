package identity_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/keelsonhq/go-sdk/identity"
	"github.com/keelsonhq/go-sdk/internal/testfixtures"
)

func loadFixture(t *testing.T, name string, dst any) {
	t.Helper()
	data, err := testfixtures.ReadFile(name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
}

// setEnv clears every variable the fixture names, then applies env.
func setEnv(t *testing.T, names []string, env map[string]string) {
	t.Helper()
	for _, name := range names {
		t.Setenv(name, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// writeUsersFile writes roster (JSON value, or raw text) and points
// KEELSON_LOCAL_USERS_FILE at it.
func writeUsersFile(t *testing.T, data []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dev-users.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KEELSON_LOCAL_USERS_FILE", path)
}

// assertJSONEqual compares got (marshalled) with the fixture value want.
func assertJSONEqual(t *testing.T, got any, want json.RawMessage) {
	t.Helper()
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("got %s, want %s", data, want)
	}
}

func toHeader(m map[string]string) http.Header {
	h := http.Header{}
	for k, v := range m {
		h[k] = []string{v}
	}
	return h
}

func TestParity_LocalModeGuard(t *testing.T) {
	var fx struct {
		EnvVars            []string `json:"env_vars"`
		MessageMustContain []string `json:"message_must_contain"`
		Cases              []struct {
			Name     string            `json:"name"`
			Env      map[string]string `json:"env"`
			Expected struct {
				SDK   string   `json:"sdk"`
				Marks []string `json:"marks"`
			} `json:"expected"`
		} `json:"cases"`
	}
	loadFixture(t, "identity_local_mode_guard.json", &fx)

	for _, tc := range fx.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			setEnv(t, fx.EnvVars, tc.Env)
			client, err := identity.New("http://identity.invalid")
			switch tc.Expected.SDK {
			case "local", "not_local":
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				if got, want := client.IsLocal(), tc.Expected.SDK == "local"; got != want {
					t.Errorf("IsLocal() = %v, want %v", got, want)
				}
			case "error":
				if err == nil {
					t.Fatal("New: want error")
				}
				assertGuardMessage(t, err.Error(), fx.MessageMustContain, tc.Expected.Marks, tc.Env)
			default:
				t.Fatalf("unknown expectation %q", tc.Expected.SDK)
			}
		})
	}
}

func assertGuardMessage(t *testing.T, msg string, mustContain, marks []string, env map[string]string) {
	t.Helper()
	for _, s := range mustContain {
		if !strings.Contains(msg, s) {
			t.Errorf("message %q missing %q", msg, s)
		}
	}
	last := -1
	for _, mark := range marks {
		i := strings.Index(msg, mark)
		if i < 0 {
			t.Errorf("message %q missing mark %q", msg, mark)
		} else if i < last {
			t.Errorf("message %q lists %q out of order", msg, mark)
		}
		last = i
	}
	for name, value := range env {
		if name != "KEELSON_LOCAL_MODE" && name != "KEELSON_MODE" && strings.Contains(msg, value) {
			t.Errorf("message %q leaks the value of %s", msg, name)
		}
	}
}

func TestParity_RequestUser(t *testing.T) {
	var fx struct {
		EnvVars     []string `json:"env_vars"`
		HeaderCases []struct {
			Name     string            `json:"name"`
			Headers  map[string]string `json:"headers"`
			Expected json.RawMessage   `json:"expected"`
			Error    string            `json:"error"`
		} `json:"header_cases"`
		LocalCases []struct {
			Name      string            `json:"name"`
			Env       map[string]string `json:"env"`
			UsersFile json.RawMessage   `json:"users_file"`
			Headers   map[string]string `json:"headers"`
			Expected  json.RawMessage   `json:"expected"`
		} `json:"local_cases"`
	}
	loadFixture(t, "identity_request_user.json", &fx)

	for _, tc := range fx.HeaderCases {
		t.Run("header/"+tc.Name, func(t *testing.T) {
			setEnv(t, fx.EnvVars, nil)
			client, err := identity.New("")
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got, err := client.GetRequestUser(identity.WithHeaders(toHeader(tc.Headers)))
			if tc.Error != "" {
				_, currentErr := client.GetCurrentUser(identity.WithHeaders(toHeader(tc.Headers)))
				if err == nil || currentErr == nil || err.Error() != currentErr.Error() {
					t.Fatalf("GetRequestUser err = %v, want the GetCurrentUser error %v", err, currentErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetRequestUser: %v", err)
			}
			assertJSONEqual(t, got, tc.Expected)
		})
	}

	for _, tc := range fx.LocalCases {
		t.Run("local/"+tc.Name, func(t *testing.T) {
			setEnv(t, fx.EnvVars, tc.Env)
			if tc.UsersFile != nil {
				writeUsersFile(t, tc.UsersFile)
			}
			client, err := identity.New("")
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got, err := client.GetRequestUser(identity.WithHeaders(toHeader(tc.Headers)))
			if err != nil {
				t.Fatalf("GetRequestUser: %v", err)
			}
			assertJSONEqual(t, got, tc.Expected)
		})
	}
}

type rosterFixture struct {
	EnvVars        []string                   `json:"env_vars"`
	Roster         json.RawMessage            `json:"roster"`
	CurrentUser    json.RawMessage            `json:"current_user"`
	RequestUser    json.RawMessage            `json:"request_user"`
	FixedUserID    string                     `json:"fixed_user_id"`
	Identities     map[string]json.RawMessage `json:"identities"`
	FixedUserCases []struct {
		Name           string          `json:"name"`
		Roster         json.RawMessage `json:"roster"`
		ExpectedUserID string          `json:"expected_user_id"`
	} `json:"fixed_user_cases"`
	WorkspaceIDCases []struct {
		Name                string            `json:"name"`
		Env                 map[string]string `json:"env"`
		ExpectedWorkspaceID string            `json:"expected_workspace_id"`
	} `json:"workspace_id_cases"`
	InvalidRosters []struct {
		Reason string          `json:"reason"`
		Text   *string         `json:"text"`
		Roster json.RawMessage `json:"roster"`
	} `json:"invalid_rosters"`
}

func newRosterClient(t *testing.T, fx *rosterFixture, roster []byte, env map[string]string) *identity.Client {
	t.Helper()
	setEnv(t, fx.EnvVars, map[string]string{"KEELSON_LOCAL_MODE": "1"})
	for k, v := range env {
		t.Setenv(k, v)
	}
	writeUsersFile(t, roster)
	client, err := identity.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func TestParity_LocalRoster_Identity(t *testing.T) {
	var fx rosterFixture
	loadFixture(t, "identity_local_roster.json", &fx)

	t.Run("fixed_user", func(t *testing.T) {
		// The env user overrides must not apply when a users file is in use.
		client := newRosterClient(t, &fx, fx.Roster, map[string]string{
			"KEELSON_LOCAL_USER_ID":        "env-user",
			"KEELSON_LOCAL_USER_EMAIL":     "env@example.com",
			"KEELSON_LOCAL_USER_NAME":      "Env",
			"KEELSON_LOCAL_WORKSPACE_ROLE": "OWNER",
		})
		current, err := client.GetCurrentUser()
		if err != nil {
			t.Fatal(err)
		}
		assertJSONEqual(t, current, fx.CurrentUser)
		request, err := client.GetRequestUser()
		if err != nil {
			t.Fatal(err)
		}
		assertJSONEqual(t, request, fx.RequestUser)
		full, err := client.GetCurrentIdentity()
		if err != nil {
			t.Fatal(err)
		}
		assertJSONEqual(t, full, fx.Identities[fx.FixedUserID])
	})

	// Each identity is checked with a roster of that user alone, which makes
	// them the fixed user.
	var roster struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal(fx.Roster, &roster); err != nil {
		t.Fatal(err)
	}
	for _, user := range roster.Users {
		id := user["id"].(string)
		t.Run("identity/"+id, func(t *testing.T) {
			data, _ := json.Marshal(map[string]any{"users": []any{user}})
			full, err := newRosterClient(t, &fx, data, nil).GetCurrentIdentity()
			if err != nil {
				t.Fatal(err)
			}
			assertJSONEqual(t, full, fx.Identities[id])
		})
	}

	for _, tc := range fx.FixedUserCases {
		t.Run("fixed_user/"+tc.Name, func(t *testing.T) {
			me, err := newRosterClient(t, &fx, tc.Roster, nil).GetCurrentUser()
			if err != nil {
				t.Fatal(err)
			}
			if me.ID != tc.ExpectedUserID {
				t.Errorf("ID = %q, want %q", me.ID, tc.ExpectedUserID)
			}
		})
	}

	for _, tc := range fx.WorkspaceIDCases {
		t.Run("workspace_id/"+tc.Name, func(t *testing.T) {
			full, err := newRosterClient(t, &fx, fx.Roster, tc.Env).GetCurrentIdentity()
			if err != nil {
				t.Fatal(err)
			}
			if full.Workspace.ID != tc.ExpectedWorkspaceID || full.Tenant.ID != tc.ExpectedWorkspaceID {
				t.Errorf("workspace = %q, tenant = %q, want %q", full.Workspace.ID, full.Tenant.ID, tc.ExpectedWorkspaceID)
			}
		})
	}

	for _, tc := range fx.InvalidRosters {
		t.Run("invalid/"+tc.Reason, func(t *testing.T) {
			setEnv(t, fx.EnvVars, map[string]string{"KEELSON_LOCAL_MODE": "1"})
			data := []byte(tc.Roster)
			if tc.Text != nil {
				data = []byte(*tc.Text)
			}
			writeUsersFile(t, data)
			if _, err := identity.New(""); err == nil {
				t.Error("New: want error")
			}
		})
	}

	t.Run("explicit_file_missing", func(t *testing.T) {
		setEnv(t, fx.EnvVars, map[string]string{"KEELSON_LOCAL_MODE": "1"})
		t.Setenv("KEELSON_LOCAL_USERS_FILE", filepath.Join(t.TempDir(), "missing.json"))
		if _, err := identity.New(""); err == nil {
			t.Error("New: want error")
		}
	})

	t.Run("default_file", func(t *testing.T) {
		setEnv(t, fx.EnvVars, map[string]string{"KEELSON_LOCAL_MODE": "1"})
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".keelson"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".keelson", "dev-users.json"), fx.Roster, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Chdir(dir)
		client, err := identity.New("")
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		me, err := client.GetCurrentUser()
		if err != nil {
			t.Fatal(err)
		}
		if me.ID != fx.FixedUserID {
			t.Errorf("ID = %q, want %q", me.ID, fx.FixedUserID)
		}
	})
}
