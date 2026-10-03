package directory_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelsonhq/go-sdk/directory"
	"github.com/keelsonhq/go-sdk/internal/httpclient"
)

func TestListMembers(t *testing.T) {
	nextOffset := 25
	tests := []struct {
		name       string
		params     *directory.ListMembersParams
		status     int
		response   any
		wantPath   string
		wantErr    bool
		wantAPIErr bool
		check      func(t *testing.T, pm *directory.PaginatedMembers)
	}{
		{
			name:     "no params",
			params:   nil,
			status:   200,
			wantPath: "/__keelson/members",
			response: map[string]any{
				"items":       []map[string]string{{"id": "u-1", "email": "a@b.com", "name": "Alice", "role": "OWNER"}},
				"limit":       25,
				"offset":      0,
				"next_offset": nil,
			},
			check: func(t *testing.T, pm *directory.PaginatedMembers) {
				if len(pm.Items) != 1 {
					t.Fatalf("len(Items) = %d, want 1", len(pm.Items))
				}
				if pm.Items[0].ID != "u-1" {
					t.Errorf("Items[0].ID = %q, want %q", pm.Items[0].ID, "u-1")
				}
				if pm.NextOffset != nil {
					t.Errorf("NextOffset = %v, want nil", pm.NextOffset)
				}
			},
		},
		{
			name:   "with pagination and filters",
			params: &directory.ListMembersParams{Limit: intPtr(10), Offset: intPtr(5), Q: "alice", Role: "BUILDER"},
			status: 200,
			response: map[string]any{
				"items":       []map[string]string{{"id": "u-2", "email": "alice@co.com", "name": "Alice", "role": "BUILDER"}},
				"limit":       10,
				"offset":      5,
				"next_offset": nextOffset,
			},
			check: func(t *testing.T, pm *directory.PaginatedMembers) {
				if pm.Limit != 10 {
					t.Errorf("Limit = %d, want 10", pm.Limit)
				}
				if pm.Offset != 5 {
					t.Errorf("Offset = %d, want 5", pm.Offset)
				}
				if pm.NextOffset == nil || *pm.NextOffset != 25 {
					t.Errorf("NextOffset = %v, want 25", pm.NextOffset)
				}
			},
		},
		{
			name:       "401 error",
			params:     nil,
			status:     401,
			response:   map[string]string{"detail": "unauthenticated"},
			wantErr:    true,
			wantAPIErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("method = %q, want GET", r.Method)
				}
				// Verify path starts with /__keelson/members
				if got := r.URL.Path; got != "/__keelson/members" {
					t.Errorf("path = %q, want /__keelson/members", got)
				}
				// Verify query params when specified
				if tt.params != nil {
					q := r.URL.Query()
					if tt.params.Q != "" && q.Get("q") != tt.params.Q {
						t.Errorf("q = %q, want %q", q.Get("q"), tt.params.Q)
					}
					if tt.params.Role != "" && q.Get("role") != tt.params.Role {
						t.Errorf("role = %q, want %q", q.Get("role"), tt.params.Role)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer ts.Close()

			client, _ := directory.New(ts.URL)
			pm, err := client.ListMembers(tt.params)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantAPIErr {
					var apiErr *httpclient.APIError
					if !errors.As(err, &apiErr) {
						t.Fatalf("expected *APIError, got %T: %v", err, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("ListMembers: %v", err)
			}
			if tt.check != nil {
				tt.check(t, pm)
			}
		})
	}
}

func TestGetUser(t *testing.T) {
	tests := []struct {
		name       string
		userID     string
		status     int
		response   any
		wantErr    bool
		wantAPIErr bool
		check      func(t *testing.T, m *directory.MemberItem)
	}{
		{
			name:   "success",
			userID: "u-1",
			status: 200,
			response: map[string]string{
				"id": "u-1", "email": "a@b.com", "name": "Alice", "role": "OWNER",
			},
			check: func(t *testing.T, m *directory.MemberItem) {
				if m.ID != "u-1" {
					t.Errorf("ID = %q, want %q", m.ID, "u-1")
				}
				if m.Email != "a@b.com" {
					t.Errorf("Email = %q, want %q", m.Email, "a@b.com")
				}
			},
		},
		{
			name:   "image_url string",
			userID: "u-1",
			status: 200,
			response: map[string]any{
				"id": "u-1", "email": "a@b.com", "name": "Alice", "role": "OWNER",
				"image_url": "https://img.clerk.com/u-1",
			},
			check: func(t *testing.T, m *directory.MemberItem) {
				if m.ImageURL == nil || *m.ImageURL != "https://img.clerk.com/u-1" {
					t.Errorf("ImageURL = %v, want %q", m.ImageURL, "https://img.clerk.com/u-1")
				}
			},
		},
		{
			name:   "image_url null",
			userID: "u-1",
			status: 200,
			response: map[string]any{
				"id": "u-1", "email": "a@b.com", "name": "Alice", "role": "OWNER",
				"image_url": nil,
			},
			check: func(t *testing.T, m *directory.MemberItem) {
				if m.ImageURL != nil {
					t.Errorf("ImageURL = %q, want nil", *m.ImageURL)
				}
			},
		},
		{
			name:   "image_url absent",
			userID: "u-1",
			status: 200,
			response: map[string]any{
				"id": "u-1", "email": "a@b.com", "name": "Alice", "role": "OWNER",
			},
			check: func(t *testing.T, m *directory.MemberItem) {
				if m.ImageURL != nil {
					t.Errorf("ImageURL = %q, want nil", *m.ImageURL)
				}
			},
		},
		{
			name:       "404 not found",
			userID:     "nonexistent",
			status:     404,
			response:   map[string]string{"detail": "User not found."},
			wantErr:    true,
			wantAPIErr: true,
		},
		{
			name:    "empty user_id",
			userID:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("method = %q, want GET", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer ts.Close()

			client, _ := directory.New(ts.URL)
			m, err := client.GetUser(tt.userID)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantAPIErr {
					var apiErr *httpclient.APIError
					if !errors.As(err, &apiErr) {
						t.Fatalf("expected *APIError, got %T: %v", err, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("GetUser: %v", err)
			}
			if tt.check != nil {
				tt.check(t, m)
			}
		})
	}
}

func TestGetUser_PathEscape(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// r.URL.RawPath preserves percent-encoding; r.URL.Path is decoded.
		if got := r.URL.RawPath; got != "/__keelson/users/id%2Fwith%2Fslashes" {
			t.Errorf("RawPath = %q, want /__keelson/users/id%%2Fwith%%2Fslashes", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"id": "id/with/slashes", "email": "x@y.com", "name": "X", "role": "BUILDER",
		})
	}))
	defer ts.Close()

	client, _ := directory.New(ts.URL)
	_, err := client.GetUser("id/with/slashes")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
}

func TestListGroups(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		response any
		wantErr  bool
		check    func(t *testing.T, groups []directory.GroupItem)
	}{
		{
			name:   "success",
			status: 200,
			response: map[string]any{
				"items": []map[string]any{
					{"id": "g-dev", "key": "developers", "display_name": "Developers", "kind": "SYSTEM", "system_kind": "developers"},
					{"id": "g-design", "key": "design", "display_name": "Design Team", "kind": "CUSTOM", "system_kind": nil},
					{"id": "g-keyless", "key": nil, "display_name": "Keyless Team", "kind": "CUSTOM", "system_kind": nil},
				},
			},
			check: func(t *testing.T, groups []directory.GroupItem) {
				if len(groups) != 3 {
					t.Fatalf("len(groups) = %d, want 3", len(groups))
				}
				if groups[0].ID != "g-dev" {
					t.Errorf("groups[0].ID = %q, want %q", groups[0].ID, "g-dev")
				}
				if groups[0].Key == nil || *groups[0].Key != "developers" {
					t.Errorf("groups[0].Key = %v, want %q", groups[0].Key, "developers")
				}
				if groups[0].Kind != "SYSTEM" {
					t.Errorf("groups[0].Kind = %q, want %q", groups[0].Kind, "SYSTEM")
				}
				if groups[0].SystemKind == nil || *groups[0].SystemKind != "developers" {
					t.Errorf("groups[0].SystemKind = %v, want %q", groups[0].SystemKind, "developers")
				}
				if groups[1].SystemKind != nil {
					t.Errorf("groups[1].SystemKind = %v, want nil", groups[1].SystemKind)
				}
				// A keyless group carries id but key stays nil.
				if groups[2].ID != "g-keyless" {
					t.Errorf("groups[2].ID = %q, want %q", groups[2].ID, "g-keyless")
				}
				if groups[2].Key != nil {
					t.Errorf("groups[2].Key = %v, want nil", groups[2].Key)
				}
			},
		},
		{
			name:   "empty list",
			status: 200,
			response: map[string]any{
				"items": []any{},
			},
			check: func(t *testing.T, groups []directory.GroupItem) {
				if len(groups) != 0 {
					t.Errorf("len(groups) = %d, want 0", len(groups))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/__keelson/groups" {
					t.Errorf("path = %q, want /__keelson/groups", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer ts.Close()

			client, _ := directory.New(ts.URL)
			groups, err := client.ListGroups()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ListGroups: %v", err)
			}
			if tt.check != nil {
				tt.check(t, groups)
			}
		})
	}
}

func TestListMembers_ForwardsHeaders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "session=abc" {
			t.Errorf("Cookie = %q, want %q", got, "session=abc")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer jwt-xyz" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer jwt-xyz")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"items": []any{}, "limit": 25, "offset": 0, "next_offset": nil,
		})
	}))
	defer ts.Close()

	client, _ := directory.New(ts.URL)
	_, err := client.ListMembers(nil,
		directory.WithCookie("session=abc"),
		directory.WithAuthorization("Bearer jwt-xyz"),
	)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
}

func TestListMembers_MalformedResponses(t *testing.T) {
	tests := []struct {
		name     string
		response any
		wantMsg  string
	}{
		{
			name:     "empty object (no items key)",
			response: map[string]any{},
			wantMsg:  "missing 'items'",
		},
		{
			name:     "missing limit",
			response: map[string]any{"items": []any{}, "offset": 0},
			wantMsg:  "missing 'limit'",
		},
		{
			name:     "missing offset",
			response: map[string]any{"items": []any{}, "limit": 25},
			wantMsg:  "missing 'offset'",
		},
		{
			name: "item missing id",
			response: map[string]any{
				"items":       []map[string]string{{"id": "", "email": "a@b.com", "name": "A", "role": "OWNER"}},
				"limit":       25,
				"offset":      0,
				"next_offset": nil,
			},
			wantMsg: "missing 'id'",
		},
		{
			name: "item missing email",
			response: map[string]any{
				"items":       []map[string]any{{"id": "u-1", "name": "A", "role": "OWNER"}},
				"limit":       25,
				"offset":      0,
				"next_offset": nil,
			},
			wantMsg: "missing 'email'",
		},
		{
			name: "item missing name",
			response: map[string]any{
				"items":       []map[string]any{{"id": "u-1", "email": "a@b.com", "role": "OWNER"}},
				"limit":       25,
				"offset":      0,
				"next_offset": nil,
			},
			wantMsg: "missing 'name'",
		},
		{
			name: "item missing role",
			response: map[string]any{
				"items":       []map[string]any{{"id": "u-1", "email": "a@b.com", "name": "A"}},
				"limit":       25,
				"offset":      0,
				"next_offset": nil,
			},
			wantMsg: "missing 'role'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer ts.Close()

			client, _ := directory.New(ts.URL)
			_, err := client.ListMembers(nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got := err.Error(); !strings.Contains(got, tt.wantMsg) {
				t.Errorf("error = %q, want to contain %q", got, tt.wantMsg)
			}
		})
	}
}

func TestGetUser_MalformedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return object with empty id.
		json.NewEncoder(w).Encode(map[string]string{"id": "", "email": "a@b.com", "name": "A", "role": "X"})
	}))
	defer ts.Close()

	client, _ := directory.New(ts.URL)
	_, err := client.GetUser("u-1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "missing 'id'") {
		t.Errorf("error = %q, want to contain 'missing id'", err.Error())
	}
}

func TestListGroups_MalformedResponses(t *testing.T) {
	tests := []struct {
		name     string
		response any
		wantMsg  string
	}{
		{
			name:     "missing items key",
			response: map[string]any{},
			wantMsg:  "missing 'items'",
		},
		{
			name: "item missing id",
			response: map[string]any{
				"items": []map[string]any{{"key": "devs", "display_name": "X", "kind": "CUSTOM", "system_kind": nil}},
			},
			wantMsg: "missing 'id'",
		},
		{
			name: "item missing display_name",
			response: map[string]any{
				"items": []map[string]any{{"id": "g1", "key": "devs", "kind": "CUSTOM", "system_kind": nil}},
			},
			wantMsg: "missing 'display_name'",
		},
		{
			name: "item missing kind",
			response: map[string]any{
				"items": []map[string]any{{"id": "g1", "key": "devs", "display_name": "Devs", "system_kind": nil}},
			},
			wantMsg: "missing 'kind'",
		},
		{
			name: "item with empty key",
			response: map[string]any{
				"items": []map[string]any{{"id": "g1", "key": "", "display_name": "Devs", "kind": "CUSTOM", "system_kind": nil}},
			},
			wantMsg: "invalid 'key'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer ts.Close()

			client, _ := directory.New(ts.URL)
			_, err := client.ListGroups()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantMsg)
			}
		})
	}
}

func TestListMembers_TrailingSlashBaseURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__keelson/members" {
			t.Errorf("path = %q, want /__keelson/members (no double slash)", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"items": []any{}, "limit": 25, "offset": 0, "next_offset": nil,
		})
	}))
	defer ts.Close()

	client, _ := directory.New(ts.URL + "/")
	_, err := client.ListMembers(nil)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
}

func TestNew_RequiresBaseURL(t *testing.T) {
	t.Setenv("KEELSON_DIRECTORY_BASE_URL", "")
	t.Setenv("KEELSON_IDENTITY_BASE_URL", "")
	_, err := directory.New("")
	if err == nil {
		t.Fatal("expected error for empty base_url, got nil")
	}
}

// capturedAuthServer starts a members endpoint that records the
// Authorization and Cookie headers it received.
func capturedAuthServer(t *testing.T) (*httptest.Server, *string, *string) {
	t.Helper()
	var gotAuth, gotCookie string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"items": []any{}, "limit": 25, "offset": 0, "next_offset": nil,
		})
	}))
	return ts, &gotAuth, &gotCookie
}

func TestWithAppToken_SetsBearer(t *testing.T) {
	ts, gotAuth, _ := capturedAuthServer(t)
	defer ts.Close()

	t.Setenv("KEELSON_DIRECTORY_TOKEN", "")
	client, _ := directory.New(ts.URL)
	if _, err := client.ListMembers(nil, directory.WithAppToken("keelson_abc")); err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if *gotAuth != "Bearer keelson_abc" {
		t.Errorf("Authorization = %q, want %q", *gotAuth, "Bearer keelson_abc")
	}
}

func TestAppToken_EnvFallback(t *testing.T) {
	ts, gotAuth, _ := capturedAuthServer(t)
	defer ts.Close()

	t.Setenv("KEELSON_DIRECTORY_TOKEN", "keelson_env")
	client, _ := directory.New(ts.URL)
	if _, err := client.ListMembers(nil); err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if *gotAuth != "Bearer keelson_env" {
		t.Errorf("Authorization = %q, want %q", *gotAuth, "Bearer keelson_env")
	}
}

func TestAppToken_CookieSkipsEnvFallback(t *testing.T) {
	ts, gotAuth, gotCookie := capturedAuthServer(t)
	defer ts.Close()

	t.Setenv("KEELSON_DIRECTORY_TOKEN", "keelson_env")
	client, _ := directory.New(ts.URL)
	if _, err := client.ListMembers(nil, directory.WithCookie("session=abc")); err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	// A cookie signals user-as-actor intent: the env app token must not leak in.
	if *gotAuth != "" {
		t.Errorf("Authorization = %q, want empty (cookie skips env fallback)", *gotAuth)
	}
	if *gotCookie != "session=abc" {
		t.Errorf("Cookie = %q, want %q", *gotCookie, "session=abc")
	}
}

func TestWithAppToken_DropsCookie(t *testing.T) {
	ts, gotAuth, gotCookie := capturedAuthServer(t)
	defer ts.Close()

	t.Setenv("KEELSON_DIRECTORY_TOKEN", "")
	client, _ := directory.New(ts.URL)
	_, err := client.ListMembers(nil,
		directory.WithCookie("session=abc"),
		directory.WithAppToken("keelson_abc"),
	)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	// WithAppToken makes the app the actor: the user cookie must not ride along.
	if *gotCookie != "" {
		t.Errorf("Cookie = %q, want empty (WithAppToken drops Cookie)", *gotCookie)
	}
	if *gotAuth != "Bearer keelson_abc" {
		t.Errorf("Authorization = %q, want %q", *gotAuth, "Bearer keelson_abc")
	}
}

func TestWithAuthorization_WinsOverEnvToken(t *testing.T) {
	ts, gotAuth, _ := capturedAuthServer(t)
	defer ts.Close()

	t.Setenv("KEELSON_DIRECTORY_TOKEN", "keelson_env")
	client, _ := directory.New(ts.URL)
	if _, err := client.ListMembers(nil, directory.WithAuthorization("Bearer jwt-xyz")); err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if *gotAuth != "Bearer jwt-xyz" {
		t.Errorf("Authorization = %q, want %q", *gotAuth, "Bearer jwt-xyz")
	}
}

func TestWithAppToken_LastWins(t *testing.T) {
	t.Setenv("KEELSON_DIRECTORY_TOKEN", "")

	t.Run("app token after authorization", func(t *testing.T) {
		ts, gotAuth, _ := capturedAuthServer(t)
		defer ts.Close()
		client, _ := directory.New(ts.URL)
		_, err := client.ListMembers(nil,
			directory.WithAuthorization("Bearer jwt-xyz"),
			directory.WithAppToken("keelson_abc"),
		)
		if err != nil {
			t.Fatalf("ListMembers: %v", err)
		}
		if *gotAuth != "Bearer keelson_abc" {
			t.Errorf("Authorization = %q, want %q", *gotAuth, "Bearer keelson_abc")
		}
	})

	t.Run("authorization after app token", func(t *testing.T) {
		ts, gotAuth, _ := capturedAuthServer(t)
		defer ts.Close()
		client, _ := directory.New(ts.URL)
		_, err := client.ListMembers(nil,
			directory.WithAppToken("keelson_abc"),
			directory.WithAuthorization("Bearer jwt-xyz"),
		)
		if err != nil {
			t.Fatalf("ListMembers: %v", err)
		}
		if *gotAuth != "Bearer jwt-xyz" {
			t.Errorf("Authorization = %q, want %q", *gotAuth, "Bearer jwt-xyz")
		}
	})
}

func TestNew_DirectoryBaseURLEnv(t *testing.T) {
	ts, _, _ := capturedAuthServer(t)
	defer ts.Close()

	t.Setenv("KEELSON_LOCAL_MODE", "")
	t.Setenv("KEELSON_DIRECTORY_TOKEN", "")
	t.Setenv("KEELSON_DIRECTORY_BASE_URL", ts.URL)
	t.Setenv("KEELSON_IDENTITY_BASE_URL", "http://wrong-identity")

	client, err := directory.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.ListMembers(nil); err != nil {
		t.Fatalf("ListMembers: %v (KEELSON_DIRECTORY_BASE_URL not used?)", err)
	}
}

func TestNew_FallsBackToIdentityBaseURL(t *testing.T) {
	ts, _, _ := capturedAuthServer(t)
	defer ts.Close()

	t.Setenv("KEELSON_LOCAL_MODE", "")
	t.Setenv("KEELSON_DIRECTORY_TOKEN", "")
	t.Setenv("KEELSON_DIRECTORY_BASE_URL", "")
	t.Setenv("KEELSON_IDENTITY_BASE_URL", ts.URL)

	client, err := directory.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.ListMembers(nil); err != nil {
		t.Fatalf("ListMembers: %v (KEELSON_IDENTITY_BASE_URL fallback not used?)", err)
	}
}

func intPtr(n int) *int { return &n }
