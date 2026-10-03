package tasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The fake `keelson` CLI is this test binary itself: installFakeCLI puts a
// `keelson` symlink to it on PATH, and TestMain turns into the fake when the
// marker env is set. Its behavior is driven by FAKE_CLI_* env vars, which the
// SDK passes through because the CLI inherits the app's environment.
const fakeCLIMarker = "KEELSON_TASKS_FAKE_CLI"

func TestMain(m *testing.M) {
	if os.Getenv(fakeCLIMarker) == "1" {
		os.Exit(fakeCLI())
	}
	os.Exit(m.Run())
}

type cliCall struct {
	Argv  []string `json:"argv"`
	Stdin string   `json:"stdin"`
	Cwd   string   `json:"cwd"`
}

func fakeCLI() int {
	stdin, _ := io.ReadAll(os.Stdin)
	cwd, _ := os.Getwd()
	if path := os.Getenv("FAKE_CLI_LOG"); path != "" {
		line, _ := json.Marshal(cliCall{Argv: os.Args[1:], Stdin: string(stdin), Cwd: cwd})
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = f.Write(append(line, '\n'))
			_ = f.Close()
		}
	}
	if os.Getenv("FAKE_CLI_STDERR") == "1" {
		fmt.Fprintln(os.Stderr, "fake-cli-stderr")
	}

	if os.Getenv("FAKE_CLI_MODE") == "wait-term" {
		dir := os.Getenv("FAKE_CLI_SIGNAL_DIR")
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGTERM)
		_ = os.WriteFile(filepath.Join(dir, "ready"), nil, 0o600)
		select {
		case <-sigs:
			_ = os.WriteFile(filepath.Join(dir, "got-term"), nil, 0o600)
		case <-time.After(30 * time.Second):
		}
		fmt.Print(`{"error":{"code":"task_interrupted","message":"interrupted","hint":"","retryable":false}}`)
		return 1
	}

	if out, ok := os.LookupEnv("FAKE_CLI_STDOUT"); ok {
		fmt.Print(out)
	} else {
		name := ""
		if len(os.Args) > 4 {
			name = strings.ToLower(strings.TrimSpace(os.Args[4]))
		}
		id := make([]byte, 16)
		_, _ = rand.Read(id)
		out, _ := json.Marshal(map[string]any{"task": map[string]any{
			"task_id":           hex.EncodeToString(id),
			"name":              name,
			"status":            "succeeded",
			"claimed_attempts":  1,
			"last_failure_code": nil,
			"created_at":        "2026-10-02T03:04:05.000000Z",
			"finished_at":       "2026-10-02T03:04:06.000000Z",
			"exit_code":         0,
			"timed_out":         false,
		}})
		fmt.Println(string(out))
	}
	if code := os.Getenv("FAKE_CLI_EXIT"); code == "1" {
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// Env helpers
// ---------------------------------------------------------------------------

var tasksEnvVars = []string{
	"KEELSON_MODE",
	"KEELSON_TASKS_BASE_URL",
	"KEELSON_APP_ID",
	"KEELSON_WORKSPACE_ID",
	"KEELSON_TENANT_ID",
	"KEELSON_DEPLOY_ID",
	"KEELSON_TASKS_METADATA_URL",
	"FAKE_CLI_STDOUT",
	"FAKE_CLI_EXIT",
	"FAKE_CLI_MODE",
	"FAKE_CLI_STDERR",
}

// clearEnv unsets every variable the SDK or the fake CLI reads, restoring it
// when the test ends.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range tasksEnvVars {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
}

func asTasksError(t *testing.T, err error) *Error {
	t.Helper()
	te, ok := err.(*Error)
	if !ok {
		t.Fatalf("error = %#v (%v), want *tasks.Error", err, err)
	}
	return te
}

// ---------------------------------------------------------------------------
// Local mode: the fake CLI
// ---------------------------------------------------------------------------

type fakeCLIHandle struct {
	dir string
	log string
}

func (f *fakeCLIHandle) calls(t *testing.T) []cliCall {
	t.Helper()
	raw, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls []cliCall
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var c cliCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("fake CLI log line %q: %v", line, err)
		}
		calls = append(calls, c)
	}
	return calls
}

// installFakeCLI puts the fake CLI on PATH (and nothing else), selects local
// mode and forgets local results when the test ends.
func installFakeCLI(t *testing.T) *fakeCLIHandle {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI is a symlink to the test binary")
	}
	clearEnv(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(dir, "keelson")); err != nil {
		t.Fatal(err)
	}
	h := &fakeCLIHandle{dir: dir, log: filepath.Join(t.TempDir(), "calls.jsonl")}
	t.Setenv("PATH", dir)
	t.Setenv(fakeCLIMarker, "1")
	t.Setenv("FAKE_CLI_LOG", h.log)
	t.Setenv("KEELSON_MODE", "local")
	resetLocalState(t)
	return h
}

func resetLocalState(t *testing.T) {
	reset := func() {
		local.mu.Lock()
		defer local.mu.Unlock()
		local.results = map[string]TaskStatus{}
		local.keys = map[localKey]string{}
	}
	reset()
	t.Cleanup(reset)
}

func newLocalClient(t *testing.T) *Client {
	t.Helper()
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !c.IsLocal() {
		t.Fatal("expected a local client")
	}
	return c
}

// ---------------------------------------------------------------------------
// Remote mode: a fake metadata server + runtime API
// ---------------------------------------------------------------------------

type apiRequest struct {
	Method        string
	Path          string // escaped
	Authorization string
	ContentType   string
	Body          string
}

type fakeAPI struct {
	srv *httptest.Server

	mu        sync.Mutex
	audiences []string
	flavors   []string
	requests  []apiRequest
	sleeps    []time.Duration

	// identity answers the metadata server; nil issues "tok-<n>".
	identity func(w http.ResponseWriter, n int)
	// handle answers the runtime API's n-th (0-based) request.
	handle func(w http.ResponseWriter, r *http.Request, n int)
}

func (f *fakeAPI) apiRequests() []apiRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]apiRequest(nil), f.requests...)
}

func (f *fakeAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/identity" {
		f.mu.Lock()
		f.audiences = append(f.audiences, r.URL.Query().Get("audience"))
		f.flavors = append(f.flavors, r.Header.Get("Metadata-Flavor"))
		n := len(f.audiences)
		identity := f.identity
		f.mu.Unlock()
		if identity != nil {
			identity(w, n)
			return
		}
		fmt.Fprintf(w, "tok-%d", n)
		return
	}
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	n := len(f.requests)
	f.requests = append(f.requests, apiRequest{
		Method:        r.Method,
		Path:          r.URL.EscapedPath(),
		Authorization: r.Header.Get("Authorization"),
		ContentType:   r.Header.Get("Content-Type"),
		Body:          string(body),
	})
	handle := f.handle
	f.mu.Unlock()
	if handle == nil {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"task_id":"t-1"}`)
		return
	}
	handle(w, r, n)
}

// newRemote starts the fake servers and selects remote mode against them, with
// a trailing slash on KEELSON_TASKS_BASE_URL. Retry sleeps are recorded, not
// slept.
func newRemote(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, n int)) (*Client, *fakeAPI) {
	t.Helper()
	clearEnv(t)
	f := &fakeAPI{handle: handle}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.srv.Close)
	stubSleep(t, f)
	t.Setenv("KEELSON_MODE", "keelson")
	t.Setenv("KEELSON_TASKS_BASE_URL", f.srv.URL+"/")
	t.Setenv("KEELSON_APP_ID", "app_123")
	t.Setenv("KEELSON_TASKS_METADATA_URL", f.srv.URL+"/identity")
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, f
}

func stubSleep(t *testing.T, f *fakeAPI) {
	orig := sleep
	sleep = func(ctx context.Context, d time.Duration) error {
		f.mu.Lock()
		f.sleeps = append(f.sleeps, d)
		f.mu.Unlock()
		return ctx.Err()
	}
	t.Cleanup(func() { sleep = orig })
}

func respond(status int, body string) func(http.ResponseWriter, *http.Request, int) {
	return func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}
