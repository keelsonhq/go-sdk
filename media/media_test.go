package media_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keelsonhq/go-sdk/internal/httpclient"
	"github.com/keelsonhq/go-sdk/media"
)

// newTestClient creates a Media client in Keelson mode pointing at the test server.
func newTestClient(t *testing.T, url string) *media.Client {
	t.Helper()
	c, err := media.New(url, "test-token")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// newLocalClient creates a Media client in local mode with a temp directory.
func newLocalClient(t *testing.T) *media.Client {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MEDIA_DIR", dir)
	t.Setenv("KEELSON_MODE", "local")
	t.Setenv("KEELSON_APP_ID", "")
	t.Setenv("KEELSON_INTERNAL_MEDIA_BASE_URL", "")
	t.Setenv("KEELSON_APP_MEDIA_TOKEN", "")
	c, err := media.New("", "")
	if err != nil {
		t.Fatalf("New (local): %v", err)
	}
	return c
}

// ==========================================================================
// Keelson mode tests
// ==========================================================================

// --- Put ---

func TestPut_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("method = %q, want PUT", r.Method)
		}
		if r.URL.Path != "/__keelson/internal/files/my-file" {
			t.Errorf("path = %q, want /__keelson/internal/files/my-file", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-token")
		}
		if got := r.Header.Get("Content-Type"); got != "text/plain" {
			t.Errorf("Content-Type = %q, want %q", got, "text/plain")
		}
		if got := r.Header.Get("X-Keelson-Filename"); got != "readme.txt" {
			t.Errorf("X-Keelson-Filename = %q, want %q", got, "readme.txt")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "hello" {
			t.Errorf("body = %q, want %q", string(body), "hello")
		}
		w.WriteHeader(204)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	err := client.Put("my-file", strings.NewReader("hello"),
		media.WithContentType("text/plain"),
		media.WithFilename("readme.txt"),
	)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
}

func TestPut_WithContentLength(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Length"); got != "5" {
			t.Errorf("Content-Length = %q, want %q", got, "5")
		}
		w.WriteHeader(204)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	err := client.Put("my-file", strings.NewReader("hello"),
		media.WithContentLength(5),
	)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
}

func TestPut_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(413)
		w.Write([]byte(`{"detail":"Payload too large."}`))
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	err := client.Put("my-file", strings.NewReader("big data"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *httpclient.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 413 {
		t.Errorf("StatusCode = %d, want 413", apiErr.StatusCode)
	}
}

func TestPut_Conflict(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		w.Write([]byte(`{"detail":"File id already exists."}`))
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	err := client.Put("dup-file", strings.NewReader("data"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *httpclient.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 409 {
		t.Errorf("StatusCode = %d, want 409", apiErr.StatusCode)
	}
}

// --- Get ---

func TestGet_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/__keelson/internal/files/my-file" {
			t.Errorf("path = %q, want /__keelson/internal/files/my-file", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "5")
		w.Write([]byte("hello"))
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	fc, err := client.Get("my-file")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer fc.Close()

	if fc.ContentType != "text/plain" {
		t.Errorf("ContentType = %q, want %q", fc.ContentType, "text/plain")
	}
	if fc.ContentLength != 5 {
		t.Errorf("ContentLength = %d, want 5", fc.ContentLength)
	}
	data, _ := io.ReadAll(fc.Body)
	if string(data) != "hello" {
		t.Errorf("body = %q, want %q", string(data), "hello")
	}
}

func TestGet_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"detail":"File not found."}`))
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	_, err := client.Get("missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *httpclient.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

// --- Delete ---

func TestDelete_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		if r.URL.Path != "/__keelson/internal/files/my-file" {
			t.Errorf("path = %q, want /__keelson/internal/files/my-file", r.URL.Path)
		}
		w.WriteHeader(204)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	err := client.Delete("my-file")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestDelete_NotFound_Idempotent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"detail":"File not found."}`))
	}))
	defer ts.Close()

	// 404 on delete is treated as success (idempotent), matching local mode
	// and the Node/Python SDKs.
	client := newTestClient(t, ts.URL)
	err := client.Delete("missing")
	if err != nil {
		t.Fatalf("Delete 404 should be idempotent, got: %v", err)
	}
}

func TestDelete_OtherError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		w.Write([]byte(`{"detail":"Bad gateway."}`))
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	err := client.Delete("file")
	if err == nil {
		t.Fatal("expected error for 502, got nil")
	}
	var apiErr *httpclient.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 502 {
		t.Errorf("StatusCode = %d, want 502", apiErr.StatusCode)
	}
}

// --- Exists ---

func TestExists_True(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "HEAD" {
			t.Errorf("method = %q, want HEAD", r.Method)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(200)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	ok, err := client.Exists("my-file")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !ok {
		t.Error("Exists = false, want true")
	}
}

func TestExists_False(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"detail":"File not found."}`))
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	ok, err := client.Exists("missing")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if ok {
		t.Error("Exists = true, want false")
	}
}

func TestExists_OtherError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"detail":"Invalid files token."}`))
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	_, err := client.Exists("my-file")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- Head ---

func TestHead_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "HEAD" {
			t.Errorf("method = %q, want HEAD", r.Method)
		}
		if r.URL.Path != "/__keelson/internal/files/my-file" {
			t.Errorf("path = %q, want /__keelson/internal/files/my-file", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", "2048")
		w.WriteHeader(200)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	meta, err := client.Head("my-file")
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if meta.ContentType != "image/png" {
		t.Errorf("ContentType = %q, want %q", meta.ContentType, "image/png")
	}
	if meta.ContentLength != 2048 {
		t.Errorf("ContentLength = %d, want 2048", meta.ContentLength)
	}
}

// --- URL ---

func TestURL_DefaultPrefix(t *testing.T) {
	t.Setenv("KEELSON_MEDIA_URL_PREFIX", "")
	client, err := media.New("http://gateway.example:8787", "tok")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := client.URL("my-file")
	want := "/media/my-file"
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

func TestURL_CustomPrefix(t *testing.T) {
	t.Setenv("KEELSON_MEDIA_URL_PREFIX", "/static/assets")
	client, err := media.New("http://gateway.example:8787", "tok")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := client.URL("my-file")
	want := "/static/assets/my-file"
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

func TestURL_EscapesSpecialChars(t *testing.T) {
	t.Setenv("KEELSON_MEDIA_URL_PREFIX", "")
	client, err := media.New("http://gateway.example:8787", "tok")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := client.URL("file with spaces")
	want := "/media/file%20with%20spaces"
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

// --- Validation ---

func TestValidation_EmptyFileID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach server")
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)

	if err := client.Put("", bytes.NewReader(nil)); err == nil {
		t.Error("Put: expected error for empty file_id")
	}
	if _, err := client.Get(""); err == nil {
		t.Error("Get: expected error for empty file_id")
	}
	if err := client.Delete(""); err == nil {
		t.Error("Delete: expected error for empty file_id")
	}
	if _, err := client.Head(""); err == nil {
		t.Error("Head: expected error for empty file_id")
	}
}

func TestValidation_SlashInFileID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach server")
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	err := client.Put("path/traversal", strings.NewReader("data"))
	if err == nil {
		t.Fatal("expected error for file_id with slash")
	}
	if !strings.Contains(err.Error(), "must not contain '/'") {
		t.Errorf("error = %q, want to contain slash message", err.Error())
	}
}

func TestValidation_TooLongFileID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach server")
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	longID := strings.Repeat("a", 65)
	err := client.Put(longID, strings.NewReader("data"))
	if err == nil {
		t.Fatal("expected error for too-long file_id")
	}
	if !strings.Contains(err.Error(), "at most 64") {
		t.Errorf("error = %q, want to contain length message", err.Error())
	}
}

// --- Constructor ---

func TestNew_KeelsonMode(t *testing.T) {
	client, err := media.New("http://gateway.example:8787", "my-token")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.IsLocal() {
		t.Error("expected Keelson mode, got local")
	}
}

// Fixed-contract error messages (must match media.go verbatim).
const (
	msgPartialMissingToken = "media: incomplete remote configuration: both KEELSON_INTERNAL_MEDIA_BASE_URL " +
		"and KEELSON_APP_MEDIA_TOKEN are required, but KEELSON_APP_MEDIA_TOKEN is missing"
	msgPartialMissingBase = "media: incomplete remote configuration: both KEELSON_INTERNAL_MEDIA_BASE_URL " +
		"and KEELSON_APP_MEDIA_TOKEN are required, but KEELSON_INTERNAL_MEDIA_BASE_URL is missing"
	msgKeelsonUnavailable = "media: KEELSON_MODE=keelson but Media is not configured " +
		"(KEELSON_INTERNAL_MEDIA_BASE_URL and KEELSON_APP_MEDIA_TOKEN are unset); " +
		"the Media capability is unavailable for this deployment"
	msgRefuseFallback = "media: platform environment detected " +
		"(KEELSON_APP_ID / KEELSON_TENANT_ID / KEELSON_DEPLOY_ID set) but Media is " +
		"not configured; refusing to fall back to local storage. " +
		"Set KEELSON_MODE=local for local development"
)

// clearModeEnv clears every env var the mode resolver consults, so a test
// starts from a known-clean slate regardless of the ambient environment.
func clearModeEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"KEELSON_MODE", "KEELSON_APP_ID", "KEELSON_TENANT_ID", "KEELSON_DEPLOY_ID",
		"KEELSON_INTERNAL_MEDIA_BASE_URL", "KEELSON_APP_MEDIA_TOKEN",
	} {
		t.Setenv(k, "")
	}
}

func TestNew_LocalFallback_NeitherSet(t *testing.T) {
	clearModeEnv(t)
	client, err := media.New("", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !client.IsLocal() {
		t.Error("expected local mode, got Keelson mode")
	}
}

// Partial remote configuration (only one of base URL / token) is now a
// configuration error in every mode — the old silent local fallback masked
// broken deployments. The message is asserted verbatim (fixed contract).

func TestNew_PartialConfig_OnlyBaseURL(t *testing.T) {
	clearModeEnv(t)
	t.Setenv("KEELSON_INTERNAL_MEDIA_BASE_URL", "http://gateway.example:8787")
	_, err := media.New("", "")
	if err == nil {
		t.Fatal("expected configuration error when only base URL set, got nil")
	}
	if !errors.Is(err, media.ErrConfig) {
		t.Errorf("error = %v, want to wrap media.ErrConfig", err)
	}
	if err.Error() != msgPartialMissingToken {
		t.Errorf("error = %q, want exactly %q", err.Error(), msgPartialMissingToken)
	}
}

func TestNew_PartialConfig_OnlyToken(t *testing.T) {
	clearModeEnv(t)
	t.Setenv("KEELSON_APP_MEDIA_TOKEN", "tok")
	_, err := media.New("", "")
	if err == nil {
		t.Fatal("expected configuration error when only token set, got nil")
	}
	if !errors.Is(err, media.ErrConfig) {
		t.Errorf("error = %v, want to wrap media.ErrConfig", err)
	}
	if err.Error() != msgPartialMissingBase {
		t.Errorf("error = %q, want exactly %q", err.Error(), msgPartialMissingBase)
	}
}

// Partial config is an error even in explicit local mode.
func TestNew_PartialConfig_ErrorsEvenInLocalMode(t *testing.T) {
	clearModeEnv(t)
	t.Setenv("KEELSON_MODE", "local")
	t.Setenv("KEELSON_INTERNAL_MEDIA_BASE_URL", "http://gateway.example:8787")
	_, err := media.New("", "")
	if !errors.Is(err, media.ErrConfig) {
		t.Fatalf("expected ErrConfig for partial config in local mode, got %v", err)
	}
	if err.Error() != msgPartialMissingToken {
		t.Errorf("error = %q, want exactly %q", err.Error(), msgPartialMissingToken)
	}
}

// KEELSON_MODE=keelson requires both Media env values; missing configuration
// must fail closed without creating a local file when Media is unavailable.
func TestNew_KeelsonMode_MissingEnv_FailsClosed(t *testing.T) {
	clearModeEnv(t)
	t.Setenv("KEELSON_MODE", "keelson")
	_, err := media.New("", "")
	if err == nil {
		t.Fatal("expected error for KEELSON_MODE=keelson without Media env, got nil")
	}
	if !errors.Is(err, media.ErrConfig) {
		t.Errorf("error = %v, want to wrap media.ErrConfig", err)
	}
	if err.Error() != msgKeelsonUnavailable {
		t.Errorf("error = %q, want exactly %q", err.Error(), msgKeelsonUnavailable)
	}
}

// A Keelson deployment with Media disabled must NOT be mistaken for local
// mode: constructing must fail and no local file may be written.
func TestNew_FilesDisabled_NoLocalFileCreated(t *testing.T) {
	dir := t.TempDir()
	clearModeEnv(t)
	t.Setenv("KEELSON_MODE", "keelson")
	t.Setenv("KEELSON_APP_ID", "app_123")
	t.Setenv("MEDIA_DIR", dir)

	if _, err := media.New("", ""); err == nil {
		t.Fatal("expected error, got nil")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("MEDIA_DIR should be empty, found %d entries", len(entries))
	}
}

func TestNew_KeelsonMode_WithEnv_Remote(t *testing.T) {
	clearModeEnv(t)
	t.Setenv("KEELSON_MODE", "keelson")
	t.Setenv("KEELSON_INTERNAL_MEDIA_BASE_URL", "http://gateway.example:8787")
	t.Setenv("KEELSON_APP_MEDIA_TOKEN", "tok")
	client, err := media.New("", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.IsLocal() {
		t.Error("expected remote mode in KEELSON_MODE=keelson with Media env")
	}
}

// Explicit local mode always uses local storage, even if platform env is set.
func TestNew_ExplicitLocalMode(t *testing.T) {
	clearModeEnv(t)
	t.Setenv("KEELSON_MODE", "local")
	t.Setenv("KEELSON_APP_ID", "app_123")
	client, err := media.New("", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !client.IsLocal() {
		t.Error("expected local mode for KEELSON_MODE=local")
	}
}

// mode unset + platform env visible + Media unconfigured must refuse the
// silent local fallback rather than write ephemeral files on Keelson. Every
// core identifier (APP_ID / TENANT_ID / DEPLOY_ID) independently triggers this.
func TestNew_PlatformEnvVisible_RefusesLocalFallback(t *testing.T) {
	for _, envKey := range []string{"KEELSON_APP_ID", "KEELSON_TENANT_ID", "KEELSON_DEPLOY_ID"} {
		t.Run(envKey, func(t *testing.T) {
			clearModeEnv(t)
			t.Setenv(envKey, "id_123")
			_, err := media.New("", "")
			if !errors.Is(err, media.ErrConfig) {
				t.Fatalf("expected ErrConfig when %s visible but Media unconfigured, got %v", envKey, err)
			}
			if err.Error() != msgRefuseFallback {
				t.Errorf("error = %q, want exactly %q", err.Error(), msgRefuseFallback)
			}
		})
	}
}

// Any unrecognized non-empty KEELSON_MODE is a configuration error, even when
// Media env is otherwise valid — an unknown mode must never resolve to local.
func TestNew_UnknownMode_Errors(t *testing.T) {
	clearModeEnv(t)
	t.Setenv("KEELSON_MODE", "typo")
	_, err := media.New("", "")
	if !errors.Is(err, media.ErrConfig) {
		t.Fatalf("expected ErrConfig for unknown mode, got %v", err)
	}
	want := `media: unrecognized KEELSON_MODE="typo"; expected "keelson" or "local" (or unset for local development)`
	if err.Error() != want {
		t.Errorf("error = %q, want exactly %q", err.Error(), want)
	}
}

func TestNew_UnknownMode_ErrorsEvenWithValidEnv(t *testing.T) {
	clearModeEnv(t)
	t.Setenv("KEELSON_MODE", "production")
	t.Setenv("KEELSON_INTERNAL_MEDIA_BASE_URL", "http://gateway.example:8787")
	t.Setenv("KEELSON_APP_MEDIA_TOKEN", "tok")
	_, err := media.New("", "")
	if !errors.Is(err, media.ErrConfig) {
		t.Fatalf("expected ErrConfig for unknown mode with valid env, got %v", err)
	}
}

func TestNew_EnvFallback(t *testing.T) {
	t.Setenv("KEELSON_INTERNAL_MEDIA_BASE_URL", "http://env-host:9999")
	t.Setenv("KEELSON_APP_MEDIA_TOKEN", "env-token")
	client, err := media.New("", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.IsLocal() {
		t.Error("expected Keelson mode with env vars set")
	}
}

func TestNew_TrailingSlashBaseURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__keelson/internal/files/test" {
			t.Errorf("path = %q, want no double slash", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "2")
		w.WriteHeader(200)
	}))
	defer ts.Close()

	client, err := media.New(ts.URL+"/", "tok")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Head("test")
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
}

// --- Auth ---

func TestAuth_Unauthorized(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"detail":"Invalid files token."}`))
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	err := client.Put("file", strings.NewReader("data"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *httpclient.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want 401", apiErr.StatusCode)
	}
}

// ==========================================================================
// Local mode tests
// ==========================================================================

func TestLocal_PutAndGet(t *testing.T) {
	client := newLocalClient(t)

	// Put
	err := client.Put("doc.txt", strings.NewReader("hello local"),
		media.WithContentType("text/plain"),
	)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Get
	fc, err := client.Get("doc.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer fc.Close()

	data, _ := io.ReadAll(fc.Body)
	if string(data) != "hello local" {
		t.Errorf("body = %q, want %q", string(data), "hello local")
	}
	if fc.ContentType != "text/plain" {
		t.Errorf("ContentType = %q, want %q", fc.ContentType, "text/plain")
	}
	if fc.ContentLength != 11 {
		t.Errorf("ContentLength = %d, want 11", fc.ContentLength)
	}
}

func TestLocal_Exists(t *testing.T) {
	client := newLocalClient(t)

	ok, err := client.Exists("nope")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if ok {
		t.Error("Exists = true, want false for missing file")
	}

	_ = client.Put("found", strings.NewReader("x"))
	ok, err = client.Exists("found")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !ok {
		t.Error("Exists = false, want true after Put")
	}
}

func TestLocal_Head(t *testing.T) {
	client := newLocalClient(t)

	_ = client.Put("img.png", strings.NewReader("fake-png"),
		media.WithContentType("image/png"),
	)

	meta, err := client.Head("img.png")
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if meta.ContentType != "image/png" {
		t.Errorf("ContentType = %q, want %q", meta.ContentType, "image/png")
	}
	if meta.ContentLength != 8 {
		t.Errorf("ContentLength = %d, want 8", meta.ContentLength)
	}
}

func TestLocal_Head_NotFound(t *testing.T) {
	client := newLocalClient(t)
	_, err := client.Head("missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "file not found") {
		t.Errorf("error = %q, want to contain 'file not found'", err.Error())
	}
}

func TestLocal_Delete(t *testing.T) {
	client := newLocalClient(t)

	_ = client.Put("del-me", strings.NewReader("bye"))

	err := client.Delete("del-me")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	ok, _ := client.Exists("del-me")
	if ok {
		t.Error("file still exists after Delete")
	}
}

func TestLocal_Delete_Missing(t *testing.T) {
	client := newLocalClient(t)
	// Deleting a non-existent file should not error.
	err := client.Delete("ghost")
	if err != nil {
		t.Fatalf("Delete missing file: %v", err)
	}
}

func TestLocal_Get_NotFound(t *testing.T) {
	client := newLocalClient(t)
	_, err := client.Get("nope")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "file not found") {
		t.Errorf("error = %q, want to contain 'file not found'", err.Error())
	}
}

func TestLocal_MetaFile(t *testing.T) {
	client := newLocalClient(t)

	_ = client.Put("typed", strings.NewReader("data"),
		media.WithContentType("application/pdf"),
	)

	// Verify .meta.json was written alongside the file.
	dir := os.Getenv("MEDIA_DIR")
	metaPath := filepath.Join(dir, "typed.meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read meta file: %v", err)
	}
	if !strings.Contains(string(data), "application/pdf") {
		t.Errorf("meta = %q, want to contain content_type", string(data))
	}
}

func TestLocal_DefaultContentType(t *testing.T) {
	client := newLocalClient(t)

	// Put without WithContentType — should default to application/octet-stream.
	_ = client.Put("raw", strings.NewReader("bytes"))

	meta, err := client.Head("raw")
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if meta.ContentType != "application/octet-stream" {
		t.Errorf("ContentType = %q, want %q", meta.ContentType, "application/octet-stream")
	}
}

// ==========================================================================
// Upload (auto-id) tests
// ==========================================================================

func TestUpload_Success(t *testing.T) {
	var receivedPath string
	var receivedBody string
	var receivedCT string
	var receivedFilename string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedCT = r.Header.Get("Content-Type")
		receivedFilename = r.Header.Get("X-Keelson-Filename")
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.WriteHeader(204)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	fileID, err := client.Upload(strings.NewReader("hello world"),
		media.WithContentType("text/plain"),
		media.WithFilename("readme.txt"),
	)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// Verify ULID format: 26 uppercase Crockford Base32 characters.
	if len(fileID) != 26 {
		t.Errorf("fileID length = %d, want 26", len(fileID))
	}
	for _, ch := range fileID {
		if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", ch) {
			t.Errorf("fileID contains invalid crockford char: %c", ch)
		}
	}

	// Verify the request was made correctly.
	if !strings.HasPrefix(receivedPath, "/__keelson/internal/files/") {
		t.Errorf("path = %q, want prefix /__keelson/internal/files/", receivedPath)
	}
	if receivedBody != "hello world" {
		t.Errorf("body = %q, want %q", receivedBody, "hello world")
	}
	if receivedCT != "text/plain" {
		t.Errorf("Content-Type = %q, want %q", receivedCT, "text/plain")
	}
	if receivedFilename != "readme.txt" {
		t.Errorf("X-Keelson-Filename = %q, want %q", receivedFilename, "readme.txt")
	}
}

func TestUpload_AutoContentType(t *testing.T) {
	var receivedCT string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCT = r.Header.Get("Content-Type")
		w.WriteHeader(204)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	_, err := client.Upload(strings.NewReader("{}"),
		media.WithFilename("data.json"),
	)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if receivedCT != "application/json" {
		t.Errorf("Content-Type = %q, want %q", receivedCT, "application/json")
	}
}

func TestUpload_NoFilename_DefaultContentType(t *testing.T) {
	var receivedCT string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCT = r.Header.Get("Content-Type")
		w.WriteHeader(204)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	_, err := client.Upload(strings.NewReader("binary"))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	// Without filename or explicit content type, Upload defaults to
	// application/octet-stream, matching the Node/Python SDKs.
	if receivedCT != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want %q", receivedCT, "application/octet-stream")
	}
}

func TestUpload_TextFilename_BareContentType(t *testing.T) {
	var receivedCT string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCT = r.Header.Get("Content-Type")
		w.WriteHeader(204)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	_, err := client.Upload(strings.NewReader("hello"),
		media.WithFilename("readme.txt"),
	)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	// mime.TypeByExtension may return "text/plain; charset=utf-8" on some
	// platforms. Upload strips the parameters to match Node/Python bare types.
	if receivedCT != "text/plain" {
		t.Errorf("Content-Type = %q, want %q", receivedCT, "text/plain")
	}
}

func TestUpload_HtmlFilename_BareContentType(t *testing.T) {
	var receivedCT string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCT = r.Header.Get("Content-Type")
		w.WriteHeader(204)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	_, err := client.Upload(strings.NewReader("<h1>Hi</h1>"),
		media.WithFilename("page.html"),
	)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if receivedCT != "text/html" {
		t.Errorf("Content-Type = %q, want %q", receivedCT, "text/html")
	}
}

func TestUpload_UniqueIDs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := client.Upload(strings.NewReader("x"))
		if err != nil {
			t.Fatalf("Upload %d: %v", i, err)
		}
		if seen[id] {
			t.Fatalf("duplicate ID on iteration %d: %s", i, id)
		}
		seen[id] = true
	}
}

func TestUpload_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"detail":"Internal error."}`))
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	id, err := client.Upload(strings.NewReader("data"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if id != "" {
		t.Errorf("expected empty ID on error, got %q", id)
	}
}

func TestUpload_RoundTrip_Local(t *testing.T) {
	client := newLocalClient(t)

	fileID, err := client.Upload(strings.NewReader("local content"),
		media.WithContentType("text/plain"),
		media.WithFilename("test.txt"),
	)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if len(fileID) != 26 {
		t.Errorf("fileID length = %d, want 26", len(fileID))
	}

	// Round-trip: read it back.
	fc, err := client.Get(fileID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer fc.Close()

	data, _ := io.ReadAll(fc.Body)
	if string(data) != "local content" {
		t.Errorf("body = %q, want %q", string(data), "local content")
	}
	if fc.ContentType != "text/plain" {
		t.Errorf("ContentType = %q, want %q", fc.ContentType, "text/plain")
	}

	// Exists check.
	ok, err := client.Exists(fileID)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !ok {
		t.Error("Exists = false after Upload")
	}

	// URL check.
	u := client.URL(fileID)
	if !strings.HasPrefix(u, "/media/") {
		t.Errorf("URL = %q, want /media/ prefix", u)
	}
}

// ==========================================================================
// ULID generation tests
// ==========================================================================

func TestULID_Format(t *testing.T) {
	for i := 0; i < 50; i++ {
		id := media.NewULIDForTest()
		if len(id) != 26 {
			t.Fatalf("ULID length = %d, want 26", len(id))
		}
		for _, ch := range id {
			if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", ch) {
				t.Fatalf("ULID contains invalid char: %c in %q", ch, id)
			}
		}
	}
}

func TestULID_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := media.NewULIDForTest()
		if seen[id] {
			t.Fatalf("duplicate ULID: %s", id)
		}
		seen[id] = true
	}
}

func TestLocal_CustomFilesDir(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "custom-store")
	t.Setenv("MEDIA_DIR", subDir)
	t.Setenv("KEELSON_INTERNAL_MEDIA_BASE_URL", "")
	t.Setenv("KEELSON_APP_MEDIA_TOKEN", "")

	client, err := media.New("", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Dir should be created by Put.
	_ = client.Put("a", strings.NewReader("b"))
	ok, _ := client.Exists("a")
	if !ok {
		t.Error("file not found in custom MEDIA_DIR")
	}
}
