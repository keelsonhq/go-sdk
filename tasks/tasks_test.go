package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

func TestNewConfigErrorIsErrConfig(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"keelson_without_base_url": {"KEELSON_MODE": "keelson", "KEELSON_APP_ID": "app_123"},
		"keelson_without_app_id":   {"KEELSON_MODE": "keelson", "KEELSON_TASKS_BASE_URL": "https://rt.example"},
		"unset_on_platform":        {"KEELSON_DEPLOY_ID": "dep_1"},
		"unknown_mode":             {"KEELSON_MODE": "prod"},
	} {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range env {
				t.Setenv(k, v)
			}
			c, err := New()
			if c != nil {
				t.Fatalf("New returned a client: %+v", c)
			}
			if !errors.Is(err, ErrConfig) {
				t.Fatalf("errors.Is(%v, ErrConfig) = false", err)
			}
			var te *Error
			if !errors.As(err, &te) || te.Code != "TASKS_NOT_CONFIGURED" || te.Status != 0 {
				t.Fatalf("errors.As = %+v, want TASKS_NOT_CONFIGURED", te)
			}
			if !strings.HasPrefix(err.Error(), "tasks: TASKS_NOT_CONFIGURED: ") {
				t.Fatalf("Error() = %q", err.Error())
			}
		})
	}
	// Only configuration errors wrap ErrConfig.
	if errors.Is(&Error{Code: "TASK_NOT_FOUND"}, ErrConfig) {
		t.Fatal("TASK_NOT_FOUND must not wrap ErrConfig")
	}
}

func TestNewBaseURLMissingHintsAtDeclaringTasks(t *testing.T) {
	clearEnv(t)
	t.Setenv("KEELSON_MODE", "keelson")
	t.Setenv("KEELSON_APP_ID", "app_123")
	_, err := New()
	if err == nil || !strings.Contains(err.Error(), "declares tasks:") {
		t.Fatalf("err = %v, want the tasks: declaration hint", err)
	}
}

// ---------------------------------------------------------------------------
// Remote: requests and the identity token
// ---------------------------------------------------------------------------

func TestIdentityTokenAudienceIsTheBaseURLVerbatim(t *testing.T) {
	c, f := newRemote(t, nil)
	taskID, err := c.Enqueue(context.Background(), "generate-pdf", map[string]int{"order_id": 1})
	if err != nil || taskID != "t-1" {
		t.Fatalf("Enqueue = %q, %v", taskID, err)
	}
	f.handle = respond(http.StatusOK, `{"task_id":"t-1","name":"generate-pdf","status":"queued",`+
		`"claimed_attempts":0,"last_failure_code":null,"created_at":"2026-10-02T03:04:05Z","finished_at":null}`)
	if _, err := c.Get(context.Background(), taskID); err != nil {
		t.Fatalf("Get: %v", err)
	}

	// The trailing slash stays in the audience but not in the request URL.
	want := f.srv.URL + "/"
	if len(f.audiences) != 2 || f.audiences[0] != want || f.audiences[1] != want {
		t.Fatalf("audiences = %q, want %q twice (a fresh token per call)", f.audiences, want)
	}
	for _, flavor := range f.flavors {
		if flavor != "Google" {
			t.Fatalf("Metadata-Flavor = %q", flavor)
		}
	}
	reqs := f.apiRequests()
	if reqs[0].Method != http.MethodPost ||
		reqs[0].Path != "/internal/apps/app_123/tasks/generate-pdf/enqueue" ||
		reqs[0].Authorization != "Bearer tok-1" ||
		reqs[0].ContentType != "application/json" ||
		reqs[0].Body != `{"payload":{"order_id":1}}` {
		t.Fatalf("enqueue request = %+v", reqs[0])
	}
	if reqs[1].Method != http.MethodGet || reqs[1].Path != "/internal/apps/app_123/tasks/t-1" ||
		reqs[1].Authorization != "Bearer tok-2" || reqs[1].Body != "" {
		t.Fatalf("get request = %+v", reqs[1])
	}
}

func TestEnqueueBodyCarriesIdempotencyKeyAndNullPayload(t *testing.T) {
	c, f := newRemote(t, nil)
	if _, err := c.Enqueue(context.Background(), "generate-pdf", nil, WithIdempotencyKey("order-1")); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"pre":"encoded"}`)
	if _, err := c.Enqueue(context.Background(), "generate-pdf", raw); err != nil {
		t.Fatal(err)
	}
	reqs := f.apiRequests()
	if reqs[0].Body != `{"payload":null,"idempotency_key":"order-1"}` {
		t.Fatalf("body = %s", reqs[0].Body)
	}
	if reqs[1].Body != `{"payload":{"pre":"encoded"}}` {
		t.Fatalf("body = %s", reqs[1].Body)
	}
}

func TestPathSegmentsAreEscaped(t *testing.T) {
	c, f := newRemote(t, respond(http.StatusNotFound, `{"error":{"code":"TASK_NOT_FOUND"}}`))
	_, _ = c.Enqueue(context.Background(), "a/b c", nil)
	_, _ = c.Get(context.Background(), "x/../y")
	reqs := f.apiRequests()
	if reqs[0].Path != "/internal/apps/app_123/tasks/a%2Fb%20c/enqueue" {
		t.Fatalf("enqueue path = %q", reqs[0].Path)
	}
	if reqs[1].Path != "/internal/apps/app_123/tasks/x%2F..%2Fy" {
		t.Fatalf("get path = %q", reqs[1].Path)
	}
}

func TestIdentityTokenErrorIsNotRetried(t *testing.T) {
	for name, identity := range map[string]func(http.ResponseWriter, int){
		"http_500": func(w http.ResponseWriter, _ int) { w.WriteHeader(http.StatusInternalServerError) },
		"empty":    func(http.ResponseWriter, int) {},
		"spaces":   func(w http.ResponseWriter, _ int) { fmt.Fprint(w, "tok en") },
	} {
		t.Run(name, func(t *testing.T) {
			c, f := newRemote(t, nil)
			f.identity = identity
			_, err := c.Get(context.Background(), "t-1")
			if te := asTasksError(t, err); te.Code != "TASKS_IDENTITY_TOKEN_ERROR" || te.Status != 0 {
				t.Fatalf("error = %+v", te)
			}
			if len(f.audiences) != 1 || len(f.apiRequests()) != 0 {
				t.Fatalf("token fetches = %d, API requests = %d", len(f.audiences), len(f.apiRequests()))
			}
		})
	}
}

func TestForbiddenMessageMentionsPropagationDelay(t *testing.T) {
	c, f := newRemote(t, respond(http.StatusForbidden, "<html>Forbidden</html>"))
	_, err := c.Enqueue(context.Background(), "generate-pdf", nil, WithIdempotencyKey("k"))
	te := asTasksError(t, err)
	if te.Code != "TASKS_FORBIDDEN" || !strings.Contains(te.Message, "few minutes") {
		t.Fatalf("error = %+v", te)
	}
	if len(f.apiRequests()) != 1 {
		t.Fatal("403 must not be retried")
	}
}

func TestRemoteCancelledContextReturnsCtxErr(t *testing.T) {
	c, f := newRemote(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Enqueue(ctx, "generate-pdf", nil, WithIdempotencyKey("k"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	var te *Error
	if errors.As(err, &te) {
		t.Fatalf("err = %+v, want ctx.Err() itself", te)
	}
	if len(f.apiRequests()) != 0 {
		t.Fatal("no request expected")
	}
}

// ---------------------------------------------------------------------------
// Remote: retries
// ---------------------------------------------------------------------------

func TestEnqueueRetriesTransientOnlyWithIdempotencyKey(t *testing.T) {
	t.Run("without_key_not_retried", func(t *testing.T) {
		c, f := newRemote(t, respond(http.StatusServiceUnavailable, "Service Unavailable"))
		_, err := c.Enqueue(context.Background(), "generate-pdf", nil)
		te := asTasksError(t, err)
		if te.Code != "TASKS_UNAVAILABLE_TRANSIENT" || te.Status != 503 {
			t.Fatalf("error = %+v", te)
		}
		if !strings.Contains(te.Message, "WithIdempotencyKey") {
			t.Fatalf("message %q lacks the idempotency key hint", te.Message)
		}
		if len(f.apiRequests()) != 1 || len(f.sleeps) != 0 {
			t.Fatalf("attempts = %d, sleeps = %v", len(f.apiRequests()), f.sleeps)
		}
	})
	t.Run("with_key_three_attempts", func(t *testing.T) {
		c, f := newRemote(t, respond(http.StatusBadGateway, "<html>Bad Gateway</html>"))
		_, err := c.Enqueue(context.Background(), "generate-pdf", nil, WithIdempotencyKey("k"))
		te := asTasksError(t, err)
		if te.Code != "TASKS_UNAVAILABLE_TRANSIENT" || te.Status != 502 ||
			strings.Contains(te.Message, "WithIdempotencyKey") {
			t.Fatalf("error = %+v", te)
		}
		if len(f.apiRequests()) != 3 {
			t.Fatalf("attempts = %d, want 3", len(f.apiRequests()))
		}
		if len(f.sleeps) != 2 || f.sleeps[0] != 500*time.Millisecond || f.sleeps[1] != time.Second {
			t.Fatalf("sleeps = %v, want [500ms 1s]", f.sleeps)
		}
		// Each attempt fetches a fresh token and resends the same body.
		for i, r := range f.apiRequests() {
			if r.Authorization != fmt.Sprintf("Bearer tok-%d", i+1) ||
				r.Body != `{"payload":null,"idempotency_key":"k"}` {
				t.Fatalf("attempt %d = %+v", i, r)
			}
		}
	})
	t.Run("with_key_succeeds_on_third_attempt", func(t *testing.T) {
		c, f := newRemote(t, func(w http.ResponseWriter, _ *http.Request, n int) {
			if n < 2 {
				w.WriteHeader(http.StatusGatewayTimeout)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"task_id":"t-9"}`)
		})
		got, err := c.Enqueue(context.Background(), "generate-pdf", nil, WithIdempotencyKey("k"))
		if err != nil || got != "t-9" || len(f.apiRequests()) != 3 {
			t.Fatalf("Enqueue = %q, %v after %d attempts", got, err, len(f.apiRequests()))
		}
	})
	t.Run("envelope_on_503_not_retried", func(t *testing.T) {
		c, f := newRemote(t, respond(http.StatusServiceUnavailable,
			`{"error":{"code":"TASKS_UNAVAILABLE","message":"intake closed"}}`))
		_, err := c.Enqueue(context.Background(), "generate-pdf", nil, WithIdempotencyKey("k"))
		if te := asTasksError(t, err); te.Code != "TASKS_UNAVAILABLE" || te.Message != "intake closed" {
			t.Fatalf("error = %+v", te)
		}
		if len(f.apiRequests()) != 1 {
			t.Fatalf("attempts = %d, want 1", len(f.apiRequests()))
		}
	})
}

func TestRetryStopsWhenContextIsCancelledDuringBackoff(t *testing.T) {
	c, f := newRemote(t, respond(http.StatusServiceUnavailable, ""))
	ctx, cancel := context.WithCancel(context.Background())
	sleep = func(context.Context, time.Duration) error {
		cancel()
		return ctx.Err()
	}
	_, err := c.Get(ctx, "t-1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(f.apiRequests()) != 1 {
		t.Fatalf("attempts = %d, want 1", len(f.apiRequests()))
	}
}

// ---------------------------------------------------------------------------
// Request validation
// ---------------------------------------------------------------------------

func TestPayloadLimitIsCheckedOnExactBodyBytes(t *testing.T) {
	// {"payload":"<s>"} is 14 bytes plus s; "é" is 2 bytes of UTF-8.
	atLimit := strings.Repeat("é", (MaxBodyBytes-14)/2)
	if (MaxBodyBytes-14)%2 != 0 {
		t.Fatal("limit arithmetic changed")
	}
	c, f := newRemote(t, nil)
	if _, err := c.Enqueue(context.Background(), "generate-pdf", atLimit); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	if got := len(f.apiRequests()[0].Body); got != MaxBodyBytes {
		t.Fatalf("sent %d bytes, want %d", got, MaxBodyBytes)
	}

	_, err := c.Enqueue(context.Background(), "generate-pdf", atLimit+"x")
	if te := asTasksError(t, err); te.Code != "TASK_PAYLOAD_TOO_LARGE" || te.Status != 0 {
		t.Fatalf("error = %+v", te)
	}
	// The idempotency key counts too.
	_, err = c.Enqueue(context.Background(), "generate-pdf", atLimit, WithIdempotencyKey("k"))
	if te := asTasksError(t, err); te.Code != "TASK_PAYLOAD_TOO_LARGE" {
		t.Fatalf("error = %+v", te)
	}
	if len(f.apiRequests()) != 1 {
		t.Fatalf("oversized bodies were sent: %d requests", len(f.apiRequests()))
	}
}

func TestEnqueueValidatesRequestBeforeSending(t *testing.T) {
	c, f := newRemote(t, nil)
	type cycle struct{ Next *cycle }
	loop := &cycle{}
	loop.Next = loop
	cases := map[string]func() error{
		"empty_name": func() error { _, err := c.Enqueue(context.Background(), "  ", nil); return err },
		"empty_key": func() error {
			_, err := c.Enqueue(context.Background(), "a", nil, WithIdempotencyKey(""))
			return err
		},
		"long_key": func() error {
			_, err := c.Enqueue(context.Background(), "a", nil, WithIdempotencyKey(strings.Repeat("k", 129)))
			return err
		},
		"control_char_key": func() error {
			_, err := c.Enqueue(context.Background(), "a", nil, WithIdempotencyKey("a\nb"))
			return err
		},
		"non_ascii_key": func() error {
			_, err := c.Enqueue(context.Background(), "a", nil, WithIdempotencyKey("é"))
			return err
		},
		"nan_payload": func() error { _, err := c.Enqueue(context.Background(), "a", math.NaN()); return err },
		"chan_payload": func() error {
			_, err := c.Enqueue(context.Background(), "a", make(chan int))
			return err
		},
		"cyclic_payload": func() error { _, err := c.Enqueue(context.Background(), "a", loop); return err },
		"invalid_raw_payload": func() error {
			_, err := c.Enqueue(context.Background(), "a", json.RawMessage(`{`))
			return err
		},
		"empty_task_id": func() error { _, err := c.Get(context.Background(), ""); return err },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			if te := asTasksError(t, run()); te.Code != "TASK_INVALID_REQUEST" || te.Status != 0 {
				t.Fatalf("error = %+v", te)
			}
		})
	}
	if len(f.apiRequests()) != 0 || len(f.audiences) != 0 {
		t.Fatalf("invalid requests reached the servers: %d", len(f.apiRequests()))
	}
	if _, err := c.Enqueue(context.Background(), "a", nil,
		WithIdempotencyKey(" "+strings.Repeat("~", 127))); err != nil {
		t.Fatalf("a 128-character printable key is valid: %v", err)
	}
}

func TestErrorMessageNeverContainsPayload(t *testing.T) {
	const secret = "s3cr3t-payload-value"
	payload := map[string]string{"token": secret}
	check := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("expected an error")
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks the payload: %v", err)
		}
	}
	t.Run("too_large", func(t *testing.T) {
		c, _ := newRemote(t, nil)
		_, err := c.Enqueue(context.Background(), "a", strings.Repeat(secret, 4000))
		check(t, err)
	})
	t.Run("unserializable", func(t *testing.T) {
		c, _ := newRemote(t, nil)
		_, err := c.Enqueue(context.Background(), "a", map[string]any{"token": secret, "f": func() {}})
		check(t, err)
	})
	t.Run("server_error", func(t *testing.T) {
		c, _ := newRemote(t, respond(http.StatusInternalServerError, "boom"))
		_, err := c.Enqueue(context.Background(), "a", payload)
		check(t, err)
	})
	t.Run("transient", func(t *testing.T) {
		c, _ := newRemote(t, respond(http.StatusServiceUnavailable, ""))
		_, err := c.Enqueue(context.Background(), "a", payload, WithIdempotencyKey("k"))
		check(t, err)
	})
	t.Run("local_cli_failed", func(t *testing.T) {
		installFakeCLI(t)
		t.Setenv("FAKE_CLI_STDOUT", "garbage")
		_, err := newLocalClient(t).Enqueue(context.Background(), "a", payload)
		check(t, err)
	})
	t.Run("local_cli_not_found", func(t *testing.T) {
		installFakeCLI(t)
		t.Setenv("PATH", t.TempDir())
		_, err := newLocalClient(t).Enqueue(context.Background(), "a", payload)
		check(t, err)
	})
}

// ---------------------------------------------------------------------------
// Local: `keelson dev task run`
// ---------------------------------------------------------------------------

func TestLocalEnqueueRunsCLIAndGetReturnsResult(t *testing.T) {
	cli := installFakeCLI(t)
	dir := t.TempDir()
	t.Chdir(dir)
	c := newLocalClient(t)

	taskID, err := c.Enqueue(context.Background(), "Generate-PDF", map[string]any{"order_id": 1, "note": "é"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	calls := cli.calls(t)
	if len(calls) != 1 {
		t.Fatalf("CLI calls = %d, want 1", len(calls))
	}
	// The name goes as given (the CLI normalizes it); only the payload is on stdin.
	if got := strings.Join(calls[0].Argv, " "); got != "dev task run Generate-PDF --payload - --json" {
		t.Fatalf("argv = %q", got)
	}
	if calls[0].Stdin != `{"note":"é","order_id":1}` {
		t.Fatalf("stdin = %q", calls[0].Stdin)
	}
	if wantDir, _ := filepath.EvalSymlinks(dir); calls[0].Cwd != wantDir && calls[0].Cwd != dir {
		t.Fatalf("cwd = %q, want %q", calls[0].Cwd, dir)
	}

	got, err := c.Get(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	assertStatus(t, got, wantStatus{
		TaskID: taskID, Name: "generate-pdf", Status: "succeeded", ClaimedAttempts: 1,
		CreatedAt: "2026-10-02T03:04:05Z", FinishedAt: ptr("2026-10-02T03:04:06Z"),
	})
	// Get hands out a copy: mutating it does not change the stored result.
	got.Status = "mutated"
	*got.FinishedAt = time.Time{}
	again, _ := c.Get(context.Background(), taskID)
	if again.Status != "succeeded" || again.FinishedAt.IsZero() {
		t.Fatalf("stored result changed: %+v", again)
	}
	// Results are process-wide: another client sees them.
	if _, err := newLocalClient(t).Get(context.Background(), taskID); err != nil {
		t.Fatalf("Get from a second client: %v", err)
	}
}

func ptr(s string) *string { return &s }

func TestLocalFailedCommandIsNotAnEnqueueError(t *testing.T) {
	installFakeCLI(t)
	t.Setenv("FAKE_CLI_STDOUT", `{"task":{"task_id":"t-f","name":"generate-pdf","status":"failed",`+
		`"claimed_attempts":1,"last_failure_code":"exit_nonzero","created_at":"2026-10-02T03:04:05Z",`+
		`"finished_at":"2026-10-02T03:04:06Z","exit_code":3,"timed_out":false}}`)
	t.Setenv("FAKE_CLI_EXIT", "1")
	c := newLocalClient(t)
	taskID, err := c.Enqueue(context.Background(), "generate-pdf", nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	got, _ := c.Get(context.Background(), taskID)
	if got.Status != "failed" || got.LastFailureCode == nil || *got.LastFailureCode != "exit_nonzero" {
		t.Fatalf("Get = %+v", got)
	}
}

func TestLocalPayloadDefaultsToNull(t *testing.T) {
	cli := installFakeCLI(t)
	if _, err := newLocalClient(t).Enqueue(context.Background(), "generate-pdf", nil); err != nil {
		t.Fatal(err)
	}
	if got := cli.calls(t)[0].Stdin; got != "null" {
		t.Fatalf("stdin = %q, want null", got)
	}
}

func TestLocalSameKeyDoesNotRerunCLI(t *testing.T) {
	cli := installFakeCLI(t)
	c := newLocalClient(t)
	ctx := context.Background()

	first, err := c.Enqueue(ctx, "generate-pdf", 1, WithIdempotencyKey("k1"))
	if err != nil {
		t.Fatal(err)
	}
	// Same normalized name + key, even from another client: no new run.
	second, err := newLocalClient(t).Enqueue(ctx, " Generate-PDF ", 2, WithIdempotencyKey("k1"))
	if err != nil || second != first {
		t.Fatalf("second = %q, %v; want %q", second, err, first)
	}
	if n := len(cli.calls(t)); n != 1 {
		t.Fatalf("CLI calls = %d, want 1", n)
	}
	// Another key, another name, or no key: each runs the CLI.
	other, _ := c.Enqueue(ctx, "generate-pdf", 1, WithIdempotencyKey("k2"))
	otherName, _ := c.Enqueue(ctx, "send-mail", 1, WithIdempotencyKey("k1"))
	noKey1, _ := c.Enqueue(ctx, "generate-pdf", 1)
	noKey2, _ := c.Enqueue(ctx, "generate-pdf", 1)
	if n := len(cli.calls(t)); n != 5 {
		t.Fatalf("CLI calls = %d, want 5", n)
	}
	ids := map[string]bool{first: true, other: true, otherName: true, noKey1: true, noKey2: true}
	if len(ids) != 5 {
		t.Fatalf("task ids are not distinct: %v", ids)
	}
}

func TestLocalConcurrentEnqueuesKeepTheFirstKey(t *testing.T) {
	installFakeCLI(t)
	c := newLocalClient(t)
	const n = 6
	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i], errs[i] = c.Enqueue(context.Background(), "generate-pdf", i, WithIdempotencyKey("same"))
		}()
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("Enqueue %d: %v", i, errs[i])
		}
		if _, err := c.Get(context.Background(), ids[i]); err != nil {
			t.Fatalf("Get %d: %v", i, err)
		}
	}
	kept, err := c.Enqueue(context.Background(), "generate-pdf", 0, WithIdempotencyKey("same"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range ids {
		found = found || id == kept
	}
	if !found {
		t.Fatalf("kept key %q is none of %v", kept, ids)
	}
}

func TestLocalGetUnknownIsTaskNotFound(t *testing.T) {
	installFakeCLI(t)
	_, err := newLocalClient(t).Get(context.Background(), "never-enqueued")
	if te := asTasksError(t, err); te.Code != "TASK_NOT_FOUND" || te.Status != 0 {
		t.Fatalf("error = %+v", te)
	}
}

func TestLocalMissingCLIErrorHasInstallHint(t *testing.T) {
	installFakeCLI(t)
	t.Setenv("PATH", t.TempDir())
	_, err := newLocalClient(t).Enqueue(context.Background(), "generate-pdf", nil)
	te := asTasksError(t, err)
	if te.Code != "TASKS_LOCAL_CLI_NOT_FOUND" ||
		!strings.Contains(te.Message, "`keelson` was not found") ||
		!strings.Contains(te.Message, "https://keelson.dev/install.sh") {
		t.Fatalf("error = %+v", te)
	}
}

func TestLocalInvalidNameIsNotDeclaredWithoutRunningCLI(t *testing.T) {
	cli := installFakeCLI(t)
	c := newLocalClient(t)
	for _, name := range []string{"--json", "a_b", "-a", strings.Repeat("a", 64)} {
		_, err := c.Enqueue(context.Background(), name, nil)
		if te := asTasksError(t, err); te.Code != "TASK_NOT_DECLARED" {
			t.Fatalf("%q: error = %+v", name, te)
		}
	}
	if n := len(cli.calls(t)); n != 0 {
		t.Fatalf("CLI calls = %d, want 0", n)
	}
}

func TestLocalChecksRequestBeforeRunningCLI(t *testing.T) {
	cli := installFakeCLI(t)
	c := newLocalClient(t)
	_, err := c.Enqueue(context.Background(), "generate-pdf", strings.Repeat("x", MaxBodyBytes))
	if te := asTasksError(t, err); te.Code != "TASK_PAYLOAD_TOO_LARGE" {
		t.Fatalf("error = %+v", te)
	}
	_, err = c.Enqueue(context.Background(), "generate-pdf", nil, WithIdempotencyKey(""))
	if te := asTasksError(t, err); te.Code != "TASK_INVALID_REQUEST" {
		t.Fatalf("error = %+v", te)
	}
	if n := len(cli.calls(t)); n != 0 {
		t.Fatalf("CLI calls = %d, want 0", n)
	}
}

func TestLocalCLIStderrGoesToAppStderr(t *testing.T) {
	installFakeCLI(t)
	t.Setenv("FAKE_CLI_STDERR", "1")
	c := newLocalClient(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	_, err = c.Enqueue(context.Background(), "generate-pdf", nil)
	os.Stderr = orig
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "fake-cli-stderr") {
		t.Fatalf("stderr = %q", out)
	}
}

func TestLocalEnqueueCancelSendsTermToCLI(t *testing.T) {
	installFakeCLI(t)
	signals := t.TempDir()
	t.Setenv("FAKE_CLI_MODE", "wait-term")
	t.Setenv("FAKE_CLI_SIGNAL_DIR", signals)
	c := newLocalClient(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(signals, "ready")); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	start := time.Now()
	_, err := c.Enqueue(ctx, "generate-pdf", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	var te *Error
	if errors.As(err, &te) {
		t.Fatalf("err = %+v, want ctx.Err() itself (not the CLI's task_interrupted)", te)
	}
	if _, statErr := os.Stat(filepath.Join(signals, "got-term")); statErr != nil {
		t.Fatalf("the CLI did not receive SIGTERM: %v", statErr)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("Enqueue took %s after cancellation", elapsed)
	}
}
