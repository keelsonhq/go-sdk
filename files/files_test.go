package files

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Fake GCS + metadata server
// ---------------------------------------------------------------------------

type fakeGcs struct {
	mu           sync.Mutex
	store        map[string][]byte
	calls        []string // raw request URIs (RequestURI)
	tokenFetches int
	pageSize     int    // 0 = unbounded
	forceGetCode int    // when >0, GET object/list returns this status
	listBody     []byte // when non-nil, raw body for list responses
}

func newFakeGcs() *fakeGcs { return &fakeGcs{store: map[string][]byte{}} }

func (f *fakeGcs) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		f.calls = append(f.calls, r.URL.RequestURI())

		if r.URL.Path == "/token" {
			f.tokenFetches++
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": fmt.Sprintf("tok-%d", f.tokenFetches),
				"expires_in":   3600,
			})
			return
		}

		if f.forceGetCode > 0 && r.Method == http.MethodGet {
			w.WriteHeader(f.forceGetCode)
			return
		}

		// Upload: POST /upload/.../o?uploadType=media&name=<enc>
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/upload/") {
			name := r.URL.Query().Get("name")
			body, _ := io.ReadAll(r.Body)
			f.store[name] = body
			w.Write([]byte("{}"))
			return
		}

		// List: GET /.../o?prefix=<enc>[&pageToken=]
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/o") {
			if f.listBody != nil {
				w.Write(f.listBody)
				return
			}
			prefix := r.URL.Query().Get("prefix")
			var names []string
			for n := range f.store {
				if strings.HasPrefix(n, prefix) {
					names = append(names, n)
				}
			}
			sort.Strings(names)
			start := 0
			if tok := r.URL.Query().Get("pageToken"); tok != "" {
				start, _ = strconv.Atoi(tok)
			}
			type item struct {
				Name string `json:"name"`
			}
			out := struct {
				Items         []item `json:"items"`
				NextPageToken string `json:"nextPageToken,omitempty"`
			}{}
			end := len(names)
			if f.pageSize > 0 && start+f.pageSize < end {
				end = start + f.pageSize
				out.NextPageToken = strconv.Itoa(end)
			}
			for _, n := range names[start:end] {
				out.Items = append(out.Items, item{Name: n})
			}
			json.NewEncoder(w).Encode(out)
			return
		}

		// Object GET / DELETE: /storage/v1/b/<bucket>/o/<enc>
		marker := "/o/"
		idx := strings.Index(r.URL.Path, marker)
		name := ""
		if idx >= 0 {
			name, _ = url.PathUnescape(r.URL.Path[idx+len(marker):])
		}
		switch r.Method {
		case http.MethodGet:
			v, ok := f.store[name]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write(v)
		case http.MethodDelete:
			if _, ok := f.store[name]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			delete(f.store, name)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
}

// newRemoteTestClient wires a Client to a fake GCS server in KEELSON_MODE=keelson.
func newRemoteTestClient(t *testing.T, srvURL string) *Client {
	t.Helper()
	t.Setenv("KEELSON_MODE", "keelson")
	t.Setenv("KEELSON_FILES_BUCKET", "example-bucket")
	t.Setenv("KEELSON_FILES_PREFIX", "tenants/t/apps/a/files/")
	t.Setenv("KEELSON_FILES_STORAGE_BASE", srvURL)
	t.Setenv("KEELSON_FILES_METADATA_URL", srvURL+"/token")
	// Fail closed: platform identity is required in KEELSON_MODE=keelson.
	t.Setenv("KEELSON_APP_ID", "a")
	t.Setenv("KEELSON_TENANT_ID", "t")
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func newLocalTestClient(t *testing.T) *Client {
	t.Helper()
	return newLocalTestClientDir(t, t.TempDir())
}

func newLocalTestClientDir(t *testing.T, dir string) *Client {
	t.Helper()
	t.Setenv("KEELSON_MODE", "local")
	t.Setenv("KEELSON_FILES_DIR", dir)
	t.Setenv("KEELSON_FILES_BUCKET", "")
	t.Setenv("KEELSON_FILES_PREFIX", "")
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// ---------------------------------------------------------------------------
// Local backend
// ---------------------------------------------------------------------------

func TestLocalWriteReadRoundtrip(t *testing.T) {
	c := newLocalTestClient(t)
	if err := c.Write("seen.json", []byte("[]")); err != nil {
		t.Fatal(err)
	}
	data, ok, err := c.Read("seen.json")
	if err != nil || !ok || string(data) != "[]" {
		t.Fatalf("got %q ok=%v err=%v", data, ok, err)
	}
}

func TestLocalOverwrite(t *testing.T) {
	c := newLocalTestClient(t)
	c.Write("state", []byte("one"))
	c.Write("state", []byte("two"))
	data, _, _ := c.Read("state")
	if string(data) != "two" {
		t.Fatalf("got %q", data)
	}
}

func TestLocalReadMissing(t *testing.T) {
	c := newLocalTestClient(t)
	data, ok, err := c.Read("nope")
	if err != nil || ok || data != nil {
		t.Fatalf("expected (nil,false,nil), got (%q,%v,%v)", data, ok, err)
	}
}

func TestLocalDeleteIdempotent(t *testing.T) {
	c := newLocalTestClient(t)
	c.Write("x", []byte("1"))
	if err := c.Delete("x"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete("x"); err != nil {
		t.Fatalf("second delete must be idempotent: %v", err)
	}
}

func TestLocalNestedAndList(t *testing.T) {
	c := newLocalTestClient(t)
	c.Write("b.txt", []byte("b"))
	c.Write("a.txt", []byte("a"))
	c.Write("cache/z.json", []byte("z"))
	c.Write("cache/a.json", []byte("a"))
	got, err := c.List("")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.txt", "b.txt", "cache/a.json", "cache/z.json"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestLocalListPrefix(t *testing.T) {
	c := newLocalTestClient(t)
	c.Write("a.txt", []byte("a"))
	c.Write("cache/z.json", []byte("z"))
	got, _ := c.List("cache/")
	if len(got) != 1 || got[0] != "cache/z.json" {
		t.Fatalf("got %v", got)
	}
}

func TestLocalListEmpty(t *testing.T) {
	c := newLocalTestClient(t)
	got, err := c.List("")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v err %v", got, err)
	}
}

func TestLocalLiteralFileLayout(t *testing.T) {
	// The key is the literal file path so local storage remains easy to inspect.
	c := newLocalTestClient(t)
	c.Write("seen_urls.json", []byte("x"))
	info, err := os.Lstat(filepath.Join(c.dir, "seen_urls.json"))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("key must be a literal regular file: err=%v", err)
	}
	// No temp artifact left in the files dir.
	entries, _ := os.ReadDir(c.dir)
	if len(entries) != 1 || entries[0].Name() != "seen_urls.json" {
		t.Fatalf("unexpected files-dir contents: %v", entries)
	}
}

func TestLocalTempPrefixKeyIsReal(t *testing.T) {
	// A key that looks like the old temp name is a valid key and must NOT be
	// hidden by List (temp files now use a control-char prefix instead).
	c := newLocalTestClient(t)
	if err := c.Write(".keelson-tmp-user-state", []byte("v")); err != nil {
		t.Fatal(err)
	}
	data, ok, _ := c.Read(".keelson-tmp-user-state")
	if !ok || string(data) != "v" {
		t.Fatalf("got %q ok=%v", data, ok)
	}
	got, _ := c.List("")
	if len(got) != 1 || got[0] != ".keelson-tmp-user-state" {
		t.Fatalf("got %v", got)
	}
}

func TestLocalReadOfKeyShadowedByDirIsMissing(t *testing.T) {
	c := newLocalTestClient(t)
	c.Write("cache/item", []byte("child"))
	if _, ok, err := c.Read("cache"); err != nil || ok {
		t.Fatalf("expected missing, got ok=%v err=%v", ok, err)
	}
}

func TestLocalDeleteOfKeyShadowedByDirIsNoop(t *testing.T) {
	c := newLocalTestClient(t)
	c.Write("cache/item", []byte("child"))
	if err := c.Delete("cache"); err != nil {
		t.Fatalf("delete of dir-shadowed key must be a no-op: %v", err)
	}
	if _, ok, _ := c.Read("cache/item"); !ok {
		t.Fatal("child was removed")
	}
}

func TestLocalWriteParentOverChildDirIsError(t *testing.T) {
	c := newLocalTestClient(t)
	c.Write("cache/item", []byte("child"))
	err := c.Write("cache", []byte("parent"))
	if err == nil || !strings.Contains(err.Error(), "collides with a nested key") {
		t.Fatalf("expected collision error, got %v", err)
	}
	if _, ok, _ := c.Read("cache/item"); !ok {
		t.Fatal("child was clobbered")
	}
}

func TestLocalWriteChildUnderParentFileIsError(t *testing.T) {
	c := newLocalTestClient(t)
	c.Write("cache", []byte("parent"))
	err := c.Write("cache/item", []byte("child"))
	if err == nil || !strings.Contains(err.Error(), "collides with a nested key") {
		t.Fatalf("expected collision error, got %v", err)
	}
	if d, ok, _ := c.Read("cache"); !ok || string(d) != "parent" {
		t.Fatalf("parent value changed: %q ok=%v", d, ok)
	}
}

func TestLocalReadDoesNotFollowSymlinkedKeyFile(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "files")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret-outside")
	if err := os.WriteFile(secret, []byte("secret-outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(base, "leak")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	c := newLocalTestClientDir(t, base)
	if _, _, err := c.Read("leak"); err == nil {
		t.Fatal("read followed a symlinked key file out of the files dir")
	}
}

func TestLocalListDoesNotFollowSymlinkedDir(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "files")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	c := newLocalTestClientDir(t, base)
	c.Write("real", []byte("v"))
	// An external tree that looks like a stored key ("planted/deep.json").
	planted := filepath.Join(root, "outside", "planted")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planted, "deep.json"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(planted, filepath.Join(base, "linked")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	got, _ := c.List("")
	if len(got) != 1 || got[0] != "real" {
		t.Fatalf("list must not follow symlinked dir; got %v", got)
	}
}

func TestLocalContainmentEnforcedByValidation(t *testing.T) {
	c := newLocalTestClient(t)
	// "../escape" is rejected at validation before any path work.
	if err := c.Write("../escape", []byte("x")); err == nil {
		t.Fatal("expected validation error for traversal key")
	}
	// Ensure nothing was written outside the dir.
	parent := filepath.Dir(c.dir)
	if _, err := os.Stat(filepath.Join(parent, "escape")); !os.IsNotExist(err) {
		t.Fatal("file escaped the files directory")
	}
}

func TestLocalSymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "files")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "escape")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	c := newLocalTestClientDir(t, base)
	if err := c.Write("escape/pwned", []byte("x")); err == nil {
		t.Fatal("expected symlink-escape rejection")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned")); !os.IsNotExist(err) {
		t.Fatal("write escaped through the symlink")
	}
}

func TestLocalAncestorSymlinkSwapIsConfined(t *testing.T) {
	// TOCTOU: an ancestor directory swapped to an external symlink after a key
	// was written must not let read/write/delete escape (os.Root re-resolves
	// each component against a held descriptor).
	root := t.TempDir()
	base := filepath.Join(root, "files")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	c := newLocalTestClientDir(t, base)
	if err := c.Write("safe/secret", []byte("inside")); err != nil {
		t.Fatal(err)
	}
	// Plant an external directory with a same-named file, then swap base/safe.
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(base, "safe")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "safe")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	// read must not return the external content.
	if data, ok, _ := c.Read("safe/secret"); ok && string(data) == "outside-secret" {
		t.Fatal("read escaped through a swapped ancestor symlink")
	}
	// write must not create files outside the files dir.
	_ = c.Write("safe/planted", []byte("x"))
	if _, err := os.Stat(filepath.Join(outside, "planted")); !os.IsNotExist(err) {
		t.Fatal("write escaped through a swapped ancestor symlink")
	}
	// delete must not remove the external file.
	_ = c.Delete("safe/secret")
	if _, err := os.Stat(filepath.Join(outside, "secret")); err != nil {
		t.Fatal("delete escaped through a swapped ancestor symlink")
	}
}

func TestOversizeRejected(t *testing.T) {
	c := newLocalTestClient(t)
	if err := c.Write("big", make([]byte, MaxObjectSizeBytes+1)); err == nil {
		t.Fatal("expected size-limit error")
	}
}

// ---------------------------------------------------------------------------
// Key / prefix validation
// ---------------------------------------------------------------------------

func TestValidateKey(t *testing.T) {
	valid := []string{"seen_urls.json", "cache/hn.json", "a/b/c.txt", "state", "日本語.json"}
	for _, k := range valid {
		if err := validateKey(k); err != nil {
			t.Errorf("valid key %q rejected: %v", k, err)
		}
	}
	invalid := []string{"", "/lead", "trail/", "a//b", "a/./b", "a/../b", "..",
		"a\x00b", "a\x1fb", strings.Repeat("a", 513),
		// ill-formed UTF-8 byte sequences (lone continuation / truncated).
		"a\xffb", "a\x80b", "a\xed\xa0\x80b",
		// C1 control characters (category Cc): U+0085 NEL, U+009F.
		"a\u0085b", "a\u009fb",
		// single segment over NAME_MAX (255 bytes) though the key is ≤ 512.
		strings.Repeat("a", 256)}
	for _, k := range invalid {
		if err := validateKey(k); err == nil {
			t.Errorf("invalid key %q accepted", k)
		}
	}
	// multibyte byte-length: 171*3 = 513 bytes > 512.
	if err := validateKey(strings.Repeat("あ", 171)); err == nil {
		t.Error("513-byte multibyte key accepted")
	}
	// a 255-byte segment split across two segments is valid (each ≤ 255).
	if err := validateKey(strings.Repeat("a", 200) + "/" + strings.Repeat("b", 200)); err != nil {
		t.Errorf("valid two-segment key rejected: %v", err)
	}
}

func TestValidatePrefix(t *testing.T) {
	if err := validatePrefix(""); err != nil {
		t.Error("empty prefix rejected")
	}
	if err := validatePrefix("cache/"); err != nil {
		t.Error("trailing-slash prefix rejected")
	}
	if err := validatePrefix("/abs"); err == nil {
		t.Error("leading-slash prefix accepted")
	}
	if err := validatePrefix("a/../b"); err == nil {
		t.Error("traversal prefix accepted")
	}
}

// ---------------------------------------------------------------------------
// Remote (GCS) backend
// ---------------------------------------------------------------------------

func TestRemoteWriteReadRoundtrip(t *testing.T) {
	fake := newFakeGcs()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	c := newRemoteTestClient(t, srv.URL)

	if err := c.Write("k", []byte("payload")); err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.store["tenants/t/apps/a/files/k"]; !ok {
		t.Fatal("object not stored under prefix")
	}
	data, ok, err := c.Read("k")
	if err != nil || !ok || string(data) != "payload" {
		t.Fatalf("got %q ok=%v err=%v", data, ok, err)
	}
}

func TestRemoteReadMissing(t *testing.T) {
	fake := newFakeGcs()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	c := newRemoteTestClient(t, srv.URL)
	data, ok, err := c.Read("absent")
	if err != nil || ok || data != nil {
		t.Fatalf("expected (nil,false,nil), got (%q,%v,%v)", data, ok, err)
	}
}

func TestRemoteDeleteIdempotent(t *testing.T) {
	fake := newFakeGcs()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	c := newRemoteTestClient(t, srv.URL)
	if err := c.Delete("absent"); err != nil {
		t.Fatalf("delete missing must be idempotent: %v", err)
	}
	c.Write("k", []byte("v"))
	c.Delete("k")
	if _, ok := fake.store["tenants/t/apps/a/files/k"]; ok {
		t.Fatal("object not deleted")
	}
}

func TestRemoteListStripsPrefixSorts(t *testing.T) {
	fake := newFakeGcs()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	c := newRemoteTestClient(t, srv.URL)
	c.Write("b.txt", []byte("b"))
	c.Write("a.txt", []byte("a"))
	c.Write("cache/z.json", []byte("z"))
	got, _ := c.List("")
	want := []string{"a.txt", "b.txt", "cache/z.json"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestRemoteListPaging(t *testing.T) {
	fake := newFakeGcs()
	fake.pageSize = 2
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	c := newRemoteTestClient(t, srv.URL)
	for i := 0; i < 5; i++ {
		c.Write(fmt.Sprintf("k%d.txt", i), []byte("x"))
	}
	got, _ := c.List("")
	want := []string{"k0.txt", "k1.txt", "k2.txt", "k3.txt", "k4.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestRemoteNon404IsError(t *testing.T) {
	for _, code := range []int{401, 403, 429, 500, 503} {
		fake := newFakeGcs()
		fake.forceGetCode = code
		srv := httptest.NewServer(fake.handler())
		c := newRemoteTestClient(t, srv.URL)
		_, ok, err := c.Read("k")
		if err == nil || ok {
			t.Errorf("status %d: expected error, got ok=%v err=%v", code, ok, err)
		}
		srv.Close()
	}
}

func TestRemoteListWrongSchemaIsError(t *testing.T) {
	// Valid JSON but wrong schema (200 OK) must be an error, never a silent
	// empty list or a raw decode panic.
	bodies := [][]byte{
		[]byte("null"),                   // top-level null
		[]byte(`[]`),                     // array, not object
		[]byte(`{"items": "not-array"}`), // items wrong type
		[]byte(`{"items": null}`),        // items present but null
		[]byte(`{"items": [null]}`),      // list item null
		[]byte(`{"items": [{}]}`),        // item missing name
		[]byte(`{"items": [{"name": null}]}`),
		[]byte(`{"items": [{"name": 42}]}`), // item name not a string
		[]byte(`{"nextPageToken": null}`),   // token present but null
		[]byte(`{"nextPageToken": 5}`),      // token not a string
	}
	for _, body := range bodies {
		fake := newFakeGcs()
		fake.listBody = body
		srv := httptest.NewServer(fake.handler())
		c := newRemoteTestClient(t, srv.URL)
		if _, err := c.List(""); err == nil {
			t.Errorf("body %q: expected error, got nil", body)
		}
		srv.Close()
	}
}

func TestRemoteTokenCached(t *testing.T) {
	fake := newFakeGcs()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	c := newRemoteTestClient(t, srv.URL)
	c.Write("a", []byte("1"))
	c.Write("b", []byte("2"))
	c.Read("a")
	if fake.tokenFetches != 1 {
		t.Fatalf("expected 1 token fetch, got %d", fake.tokenFetches)
	}
}

func TestRemoteSizeLimitBeforeUpload(t *testing.T) {
	fake := newFakeGcs()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	c := newRemoteTestClient(t, srv.URL)
	if err := c.Write("big", make([]byte, MaxObjectSizeBytes+1)); err == nil {
		t.Fatal("expected size-limit error")
	}
	if len(fake.store) != 0 {
		t.Fatal("oversize object was uploaded")
	}
}

// ---------------------------------------------------------------------------
// Mode resolution (fail-closed)
// ---------------------------------------------------------------------------

const (
	missingIdentityMessage = "files: KEELSON_MODE=keelson but the platform identity is missing " +
		"(KEELSON_APP_ID and KEELSON_WORKSPACE_ID must be set; " +
		"KEELSON_TENANT_ID remains a deprecated alias); " +
		"the Files capability is unavailable for this deployment"
	refuseFallbackMessage = "files: platform environment detected " +
		"(KEELSON_APP_ID / KEELSON_WORKSPACE_ID (or deprecated " +
		"KEELSON_TENANT_ID alias) / KEELSON_DEPLOY_ID set) but " +
		"KEELSON_MODE is unset; refusing to fall back to local storage. " +
		"Set KEELSON_MODE=local for local development or KEELSON_MODE=keelson " +
		"for platform storage"
)

func cleanEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"KEELSON_MODE", "KEELSON_APP_ID", "KEELSON_WORKSPACE_ID", "KEELSON_TENANT_ID",
		"KEELSON_DEPLOY_ID", "KEELSON_FILES_BUCKET", "KEELSON_FILES_PREFIX"} {
		t.Setenv(k, "")
	}
}

func TestModeKeelsonWithEnv(t *testing.T) {
	cleanEnv(t)
	t.Setenv("KEELSON_MODE", "keelson")
	t.Setenv("KEELSON_FILES_BUCKET", "b")
	t.Setenv("KEELSON_FILES_PREFIX", "tenants/t/apps/a/files/")
	t.Setenv("KEELSON_APP_ID", "a")
	t.Setenv("KEELSON_TENANT_ID", "t")
	c, err := New()
	if err != nil || c.IsLocal() {
		t.Fatalf("expected remote client, err=%v local=%v", err, c != nil && c.IsLocal())
	}
}

func TestModeKeelsonWithWorkspaceEnv(t *testing.T) {
	cleanEnv(t)
	t.Setenv("KEELSON_MODE", "keelson")
	t.Setenv("KEELSON_FILES_BUCKET", "b")
	t.Setenv("KEELSON_FILES_PREFIX", "workspaces/w/apps/a/files/")
	t.Setenv("KEELSON_APP_ID", "a")
	t.Setenv("KEELSON_WORKSPACE_ID", "w")
	c, err := New()
	if err != nil || c.IsLocal() {
		t.Fatalf("expected remote client, err=%v local=%v", err, c != nil && c.IsLocal())
	}
}

func TestModeKeelsonMissingEnvFailsClosed(t *testing.T) {
	cleanEnv(t)
	t.Setenv("KEELSON_MODE", "keelson")
	if _, err := New(); !errors.Is(err, ErrConfig) {
		t.Fatalf("expected ErrConfig, got %v", err)
	}
}

func TestModeKeelsonMissingIdentityFailsClosed(t *testing.T) {
	cleanEnv(t)
	t.Setenv("KEELSON_MODE", "keelson")
	t.Setenv("KEELSON_FILES_BUCKET", "b")
	t.Setenv("KEELSON_FILES_PREFIX", "tenants/t/apps/a/files/")
	_, err := New()
	if !errors.Is(err, ErrConfig) || err.Error() != missingIdentityMessage {
		t.Fatalf("identity-missing error = %v, want %q", err, missingIdentityMessage)
	}
}

func TestModeUnsetWithRemoteEnvIsLocal(t *testing.T) {
	// KEELSON_MODE is the single mode signal: the remote env is NOT consulted
	// to infer remote when the mode is unset (no platform env → local).
	cleanEnv(t)
	t.Setenv("KEELSON_FILES_BUCKET", "b")
	t.Setenv("KEELSON_FILES_PREFIX", "tenants/t/apps/a/files/")
	c, err := New()
	if err != nil || !c.IsLocal() {
		t.Fatalf("expected local, err=%v", err)
	}
}

func TestModePartialConfig(t *testing.T) {
	cleanEnv(t)
	t.Setenv("KEELSON_FILES_BUCKET", "b")
	_, err := New()
	if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), "KEELSON_FILES_PREFIX is missing") {
		t.Fatalf("expected prefix-missing config error, got %v", err)
	}
}

func TestModeLocalWithPlatformEnv(t *testing.T) {
	cleanEnv(t)
	t.Setenv("KEELSON_MODE", "local")
	t.Setenv("KEELSON_APP_ID", "app_1")
	c, err := New()
	if err != nil || !c.IsLocal() {
		t.Fatalf("expected local, err=%v", err)
	}
}

func TestModeZeroConfigLocal(t *testing.T) {
	cleanEnv(t)
	c, err := New()
	if err != nil || !c.IsLocal() {
		t.Fatalf("expected local, err=%v", err)
	}
}

func TestModeRefuseFallbackOnPlatform(t *testing.T) {
	for _, v := range []string{"KEELSON_APP_ID", "KEELSON_WORKSPACE_ID", "KEELSON_TENANT_ID", "KEELSON_DEPLOY_ID"} {
		cleanEnv(t)
		t.Setenv(v, "x")
		if _, err := New(); !errors.Is(err, ErrConfig) || err.Error() != refuseFallbackMessage {
			t.Fatalf("%s set: error = %v, want %q", v, err, refuseFallbackMessage)
		}
	}
}

func TestModeUnknownFailsClosed(t *testing.T) {
	cleanEnv(t)
	t.Setenv("KEELSON_MODE", "production")
	if _, err := New(); !errors.Is(err, ErrConfig) {
		t.Fatalf("expected ErrConfig, got %v", err)
	}
}
