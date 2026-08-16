package files

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelsonhq/go-sdk/internal/testfixtures"
)

// TestParity_KeyValidation drives the shared key-grammar fixture, asserting the
// same accept/reject decisions as the Node and Python SDKs.
func TestParity_KeyValidation(t *testing.T) {
	raw, err := testfixtures.ReadFile("files_key_validation.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx struct {
		Valid   []string `json:"valid"`
		Invalid []struct {
			Key    string `json:"key"`
			Reason string `json:"reason"`
		} `json:"invalid"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range fx.Valid {
		if err := validateKey(k); err != nil {
			t.Errorf("valid key %q rejected: %v", k, err)
		}
	}
	for _, c := range fx.Invalid {
		if err := validateKey(c.Key); err == nil {
			t.Errorf("invalid key (%s) accepted", c.Reason)
		}
	}
}

// TestParity_ListOrdering drives the shared list-ordering fixture: prefix
// stripping + lexicographic sort over the fake GCS store.
func TestParity_ListOrdering(t *testing.T) {
	raw, err := testfixtures.ReadFile("files_list_ordering.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx struct {
		FilesPrefix string   `json:"files_prefix"`
		ObjectNames []string `json:"object_names"`
		Expected    []string `json:"expected"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	fake := newFakeGcs()
	for _, n := range fx.ObjectNames {
		fake.store[n] = []byte("x")
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	t.Setenv("KEELSON_MODE", "keelson")
	t.Setenv("KEELSON_FILES_BUCKET", "b")
	t.Setenv("KEELSON_FILES_PREFIX", fx.FilesPrefix)
	t.Setenv("KEELSON_FILES_STORAGE_BASE", srv.URL)
	t.Setenv("KEELSON_FILES_METADATA_URL", srv.URL+"/token")
	t.Setenv("KEELSON_APP_ID", "a")
	t.Setenv("KEELSON_TENANT_ID", "t")
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := c.List("")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != strings.Join(fx.Expected, ",") {
		t.Fatalf("got %v want %v", got, fx.Expected)
	}
}

// TestParity_MissingRead drives the shared missing-read fixture: 404 → missing,
// every other non-2xx status is an error.
func TestParity_MissingRead(t *testing.T) {
	raw, err := testfixtures.ReadFile("files_missing_read.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx struct {
		ErrorStatuses []int `json:"error_statuses"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	fake := newFakeGcs()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	c := newRemoteTestClient(t, srv.URL)

	if _, ok, err := c.Read("absent"); err != nil || ok {
		t.Fatalf("missing read: expected (_,false,nil), got ok=%v err=%v", ok, err)
	}
	if err := c.Delete("absent"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	for _, code := range fx.ErrorStatuses {
		fake.forceGetCode = code
		if _, ok, err := c.Read("k"); err == nil || ok {
			t.Errorf("status %d must be an error, not missing", code)
		}
	}
}

// TestParity_GcsObjectEncoding drives the shared percent-encoding fixture: the
// full object name is stored decoded, and the '/' is percent-encoded (%2F) on
// the wire so each key is a single flat object.
func TestParity_GcsObjectEncoding(t *testing.T) {
	raw, err := testfixtures.ReadFile("files_gcs_object_encoding.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx struct {
		FilesPrefix   string `json:"files_prefix"`
		Key           string `json:"key"`
		ObjectName    string `json:"object_name"`
		EncodedObject string `json:"encoded_object"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	fake := newFakeGcs()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	t.Setenv("KEELSON_MODE", "keelson")
	t.Setenv("KEELSON_FILES_BUCKET", "b")
	t.Setenv("KEELSON_FILES_PREFIX", fx.FilesPrefix)
	t.Setenv("KEELSON_FILES_STORAGE_BASE", srv.URL)
	t.Setenv("KEELSON_FILES_METADATA_URL", srv.URL+"/token")
	t.Setenv("KEELSON_APP_ID", "a")
	t.Setenv("KEELSON_TENANT_ID", "t")
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.Write(fx.Key, []byte("v")); err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.store[fx.ObjectName]; !ok {
		t.Fatalf("object not stored under decoded name %q; store=%v", fx.ObjectName, fake.store)
	}
	found := false
	for _, u := range fake.calls {
		if strings.Contains(u, fx.EncodedObject) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no request URL contained the percent-encoded object %q; calls=%v",
			fx.EncodedObject, fake.calls)
	}
	if data, ok, err := c.Read(fx.Key); err != nil || !ok || string(data) != "v" {
		t.Fatalf("round-trip read failed: %q ok=%v err=%v", data, ok, err)
	}
}
