package directory_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/keelsonhq/go-sdk/directory"
	"github.com/keelsonhq/go-sdk/internal/httpclient"
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

func writeUsersFile(t *testing.T, data []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dev-users.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KEELSON_LOCAL_USERS_FILE", path)
}

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
			client, err := directory.New("http://directory.invalid")
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
				msg := err.Error()
				for _, s := range append(fx.MessageMustContain, tc.Expected.Marks...) {
					if !strings.Contains(msg, s) {
						t.Errorf("message %q missing %q", msg, s)
					}
				}
			default:
				t.Fatalf("unknown expectation %q", tc.Expected.SDK)
			}
		})
	}
}

func TestParity_LocalRoster_Directory(t *testing.T) {
	var fx struct {
		EnvVars     []string        `json:"env_vars"`
		Roster      json.RawMessage `json:"roster"`
		ListMembers []struct {
			Name  string `json:"name"`
			Query struct {
				Q        string `json:"q"`
				Role     string `json:"role"`
				GroupKey string `json:"group_key"`
				GroupID  string `json:"group_id"`
				Limit    *int   `json:"limit"`
				Offset   *int   `json:"offset"`
			} `json:"query"`
			Response json.RawMessage `json:"response"`
			Error    string          `json:"error"`
		} `json:"list_members"`
		GetUser []struct {
			UserID   string          `json:"user_id"`
			Response json.RawMessage `json:"response"`
			Error    string          `json:"error"`
		} `json:"get_user"`
		Groups struct {
			Items json.RawMessage `json:"items"`
		} `json:"groups"`
		InvalidRosters []struct {
			Reason string          `json:"reason"`
			Text   *string         `json:"text"`
			Roster json.RawMessage `json:"roster"`
		} `json:"invalid_rosters"`
	}
	loadFixture(t, "identity_local_roster.json", &fx)

	newClient := func(t *testing.T) *directory.Client {
		t.Helper()
		setEnv(t, fx.EnvVars, map[string]string{"KEELSON_LOCAL_MODE": "1"})
		writeUsersFile(t, fx.Roster)
		client, err := directory.New("")
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return client
	}

	for _, tc := range fx.ListMembers {
		t.Run("list_members/"+tc.Name, func(t *testing.T) {
			q := tc.Query
			got, err := newClient(t).ListMembers(&directory.ListMembersParams{
				Q: q.Q, Role: q.Role, GroupKey: q.GroupKey, GroupID: q.GroupID, Limit: q.Limit, Offset: q.Offset,
			})
			if tc.Error != "" {
				if err == nil {
					t.Fatal("ListMembers: want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ListMembers: %v", err)
			}
			assertJSONEqual(t, got, tc.Response)
		})
	}

	for _, tc := range fx.GetUser {
		t.Run("get_user/"+tc.UserID, func(t *testing.T) {
			got, err := newClient(t).GetUser(tc.UserID)
			if tc.Error != "" {
				var apiErr *httpclient.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
					t.Fatalf("GetUser err = %v, want a 404 APIError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetUser: %v", err)
			}
			assertJSONEqual(t, got, tc.Response)
		})
	}

	t.Run("groups", func(t *testing.T) {
		got, err := newClient(t).ListGroups()
		if err != nil {
			t.Fatalf("ListGroups: %v", err)
		}
		assertJSONEqual(t, got, fx.Groups.Items)
	})

	for _, tc := range fx.InvalidRosters {
		t.Run("invalid/"+tc.Reason, func(t *testing.T) {
			setEnv(t, fx.EnvVars, map[string]string{"KEELSON_LOCAL_MODE": "1"})
			data := []byte(tc.Roster)
			if tc.Text != nil {
				data = []byte(*tc.Text)
			}
			writeUsersFile(t, data)
			if _, err := directory.New(""); err == nil {
				t.Error("New: want error")
			}
		})
	}
}
