// Package tasks enqueues background tasks declared under `tasks:` in
// keelson.yaml, and reads their state.
//
// API (parity with the Python keelson_tasks and Node @keelsonhq/tasks SDKs):
//
//   - New()                                     — resolve the mode once.
//   - (*Client).Enqueue(ctx, name, payload, ...) — the task_id.
//   - (*Client).Get(ctx, taskID)                 — a *TaskStatus.
//
// Backends:
//
//   - Keelson (remote): POST / GET on the runtime API at
//     KEELSON_TASKS_BASE_URL, authenticated with an OIDC id token from the
//     metadata server whose audience is that URL. The command then runs later
//     on a separate instance, with platform retries.
//   - local: Enqueue starts `keelson dev task run <name> --payload - --json`
//     (the CLI on PATH), which runs the declared command once, synchronously,
//     and prints one JSON result. Results are kept in this process only, so
//     Get knows only tasks enqueued here. No retry.
//
// Mode resolution: KEELSON_MODE is the single mode signal, and New never
// silently falls back to local execution on Keelson; a configuration problem
// returns an *Error with Code "TASKS_NOT_CONFIGURED" that wraps ErrConfig.
//
// Every other failure is also an *Error, branched on Code: the runtime API's
// codes pass through (TASK_NOT_DECLARED, TASKS_UNAVAILABLE, ...), and the
// SDK's own codes start with TASKS_ (TASKS_UNAVAILABLE_TRANSIENT,
// TASKS_FORBIDDEN, ...). A cancelled ctx is returned as ctx.Err() itself. The
// payload never appears in an error message.
package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// MaxBodyBytes is the platform's limit, measured on the whole enqueue request
// body the server receives.
const MaxBodyBytes = 65536

const (
	sdkUserAgent = "Keelson-Go-SDK/0.2.0"

	idempotencyKeyMaxChars = 128

	// The identity endpoint, up to (not including) `?audience=`. The env
	// override KEELSON_TASKS_METADATA_URL is an SDK-internal test seam, NOT
	// part of the platform-injected env contract.
	defaultMetadataURL = "http://metadata.google.internal/computeMetadata/v1/" +
		"instance/service-accounts/default/identity"

	installURL = "https://keelson.dev/install.sh"

	// After ctx is cancelled the CLI gets SIGTERM (it forwards it to the
	// command's process group, which has 120 s before SIGKILL); past this it
	// is killed.
	localCancelWaitDelay = 130 * time.Second

	maxResponseBytes = 1 << 20
)

const (
	codeNotConfigured      = "TASKS_NOT_CONFIGURED"
	codeInvalidRequest     = "TASK_INVALID_REQUEST"
	codeNotDeclared        = "TASK_NOT_DECLARED"
	codeNotFound           = "TASK_NOT_FOUND"
	codePayloadTooLarge    = "TASK_PAYLOAD_TOO_LARGE"
	codeUnauthorized       = "TASKS_UNAUTHORIZED"
	codeForbidden          = "TASKS_FORBIDDEN"
	codeTransient          = "TASKS_UNAVAILABLE_TRANSIENT"
	codeServerError        = "TASKS_SERVER_ERROR"
	codeHTTPError          = "TASKS_HTTP_ERROR"
	codeUnexpectedResponse = "TASKS_UNEXPECTED_RESPONSE"
	codeIdentityToken      = "TASKS_IDENTITY_TOKEN_ERROR"
	codeLocalCLINotFound   = "TASKS_LOCAL_CLI_NOT_FOUND"
	codeLocalCLIFailed     = "TASKS_LOCAL_CLI_FAILED"
)

// Test seams.
var (
	// Per HTTP call (metadata server and runtime API).
	requestTimeout = 15 * time.Second
	// Transient failures: 3 attempts in total, waiting 0.5 s then 1 s.
	retryDelays = []time.Duration{500 * time.Millisecond, time.Second}
	sleep       = func(ctx context.Context, d time.Duration) error {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
)

// Platform-owned identifiers whose presence means the app is running on Keelson.
var coreIdentifierEnvs = []string{
	"KEELSON_APP_ID",
	"KEELSON_WORKSPACE_ID",
	"KEELSON_TENANT_ID",
	"KEELSON_DEPLOY_ID",
}

// Declared task names (keelson-yaml-spec § 4A), after trim + lowercase.
var taskNameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

var rfc3339RE = regexp.MustCompile(
	`^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|[+-](\d{2}):(\d{2}))$`)

var nonNegativeIntRE = regexp.MustCompile(`^[0-9]+$`)

// CLI error codes that mean the same as the runtime API's and pass through.
var localPassthroughCodes = map[string]bool{
	codeNotDeclared:     true,
	codeInvalidRequest:  true,
	codePayloadTooLarge: true,
}

// ErrConfig is wrapped by the *Error New returns when the Tasks configuration
// is missing or inconsistent (Code "TASKS_NOT_CONFIGURED"). Use
// errors.Is(err, tasks.ErrConfig) to detect it.
var ErrConfig = errors.New("tasks: configuration error")

// Error is every Tasks failure. Branch on Code; Status is the HTTP status of
// the runtime API response, or 0 when there was none.
type Error struct {
	Code    string
	Status  int
	Message string
}

func (e *Error) Error() string { return "tasks: " + e.Code + ": " + e.Message }

// Unwrap returns ErrConfig for a configuration error, nil otherwise.
func (e *Error) Unwrap() error {
	if e.Code == codeNotConfigured {
		return ErrConfig
	}
	return nil
}

func newError(code string, status int, format string, args ...any) *Error {
	return &Error{Code: code, Status: status, Message: fmt.Sprintf(format, args...)}
}

// TaskStatus is a task's state, with the runtime API's fields (§ 9.3).
// LastFailureCode and FinishedAt are nil when the API returns null.
type TaskStatus struct {
	TaskID          string
	Name            string
	Status          string
	ClaimedAttempts int
	LastFailureCode *string
	CreatedAt       time.Time
	FinishedAt      *time.Time
}

func (s TaskStatus) clone() *TaskStatus {
	out := s
	if s.LastFailureCode != nil {
		v := *s.LastFailureCode
		out.LastFailureCode = &v
	}
	if s.FinishedAt != nil {
		v := *s.FinishedAt
		out.FinishedAt = &v
	}
	return &out
}

// EnqueueOption configures one Enqueue call.
type EnqueueOption func(*enqueueOptions)

type enqueueOptions struct {
	idempotencyKey *string
}

// WithIdempotencyKey sets the idempotency key (1-128 printable ASCII
// characters). A repeat with the same key returns the existing task_id instead
// of enqueueing again, and lets the SDK retry transient failures.
func WithIdempotencyKey(key string) EnqueueOption {
	return func(o *enqueueOptions) { o.idempotencyKey = &key }
}

// Client enqueues and reads tasks. Construct it with New; it is safe for
// concurrent use.
type Client struct {
	local bool

	// remote mode
	audience    string
	apiBase     string
	appID       string
	metadataURL string
	hc          *http.Client
}

// ---------------------------------------------------------------------------
// Mode resolution
// ---------------------------------------------------------------------------

func env(name string) string { return strings.TrimSpace(os.Getenv(name)) }

func notConfigured(format string, args ...any) *Error {
	return newError(codeNotConfigured, 0, format, args...)
}

// New resolves the backend from the environment, once:
//
//   - KEELSON_MODE=keelson → remote. KEELSON_TASKS_BASE_URL and KEELSON_APP_ID
//     are required; either missing → TASKS_NOT_CONFIGURED.
//   - KEELSON_MODE=local → local.
//   - KEELSON_MODE unset → local, unless a platform identifier is visible, in
//     which case the silent local fallback is refused. The remote env is not
//     consulted here.
//   - Any other value → TASKS_NOT_CONFIGURED.
func New() (*Client, error) {
	mode := strings.ToLower(env("KEELSON_MODE"))
	switch mode {
	case "keelson":
		baseURL := env("KEELSON_TASKS_BASE_URL")
		if baseURL == "" {
			return nil, notConfigured(
				"KEELSON_MODE=keelson but KEELSON_TASKS_BASE_URL is unset; the " +
					"platform injects it when keelson.yaml declares tasks: and the " +
					"app is deployed.")
		}
		appID := env("KEELSON_APP_ID")
		if appID == "" {
			return nil, notConfigured(
				"KEELSON_MODE=keelson but KEELSON_APP_ID is unset; the Tasks " +
					"capability is unavailable for this deployment.")
		}
		metadataURL := env("KEELSON_TASKS_METADATA_URL")
		if metadataURL == "" {
			metadataURL = defaultMetadataURL
		}
		return &Client{
			audience:    baseURL,
			apiBase:     strings.TrimRight(baseURL, "/"),
			appID:       appID,
			metadataURL: metadataURL,
			hc: &http.Client{
				// Never follow a redirect: it would carry the bearer token elsewhere.
				CheckRedirect: func(*http.Request, []*http.Request) error {
					return http.ErrUseLastResponse
				},
			},
		}, nil
	case "local":
		return &Client{local: true}, nil
	case "":
		for _, name := range coreIdentifierEnvs {
			if env(name) != "" {
				return nil, notConfigured(
					"Platform environment detected (KEELSON_APP_ID / " +
						"KEELSON_WORKSPACE_ID (or deprecated KEELSON_TENANT_ID alias) / " +
						"KEELSON_DEPLOY_ID set) but KEELSON_MODE is unset; refusing to " +
						"fall back to running tasks locally. Set KEELSON_MODE=local for " +
						"local development or KEELSON_MODE=keelson on the platform.")
			}
		}
		return &Client{local: true}, nil
	default:
		return nil, notConfigured(
			"Unrecognized KEELSON_MODE=%q; expected \"keelson\" or \"local\" "+
				"(or unset for local development).", mode)
	}
}

// IsLocal reports whether the client runs tasks locally through the CLI.
func (c *Client) IsLocal() bool { return c.local }

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Enqueue enqueues one run of the task name declared under `tasks:` and
// returns its task_id.
//
// payload is any value json.Marshal accepts (nil is JSON null; pass a
// json.RawMessage for pre-encoded JSON); the command reads it from stdin. In
// local mode the command has already finished when Enqueue returns (its
// failure is reported by Get, not returned).
func (c *Client) Enqueue(ctx context.Context, name string, payload any, opts ...EnqueueOption) (string, error) {
	var o enqueueOptions
	for _, opt := range opts {
		opt(&o)
	}
	if strings.TrimSpace(name) == "" {
		return "", newError(codeInvalidRequest, 0, "The task name must be a non-empty string.")
	}
	if err := checkIdempotencyKey(o.idempotencyKey); err != nil {
		return "", err
	}
	payloadJSON, body, err := requestBody(payload, o.idempotencyKey)
	if err != nil {
		return "", err
	}
	if c.local {
		return enqueueLocal(ctx, name, payloadJSON, o.idempotencyKey)
	}
	return c.enqueueRemote(ctx, name, body, o.idempotencyKey != nil)
}

// Get returns the current state of the task taskID.
func (c *Client) Get(ctx context.Context, taskID string) (*TaskStatus, error) {
	if taskID == "" {
		return nil, newError(codeInvalidRequest, 0, "taskID must be a non-empty string.")
	}
	if c.local {
		return getLocal(taskID)
	}
	return c.getRemote(ctx, taskID)
}

// ---------------------------------------------------------------------------
// Request validation (both modes)
// ---------------------------------------------------------------------------

func checkIdempotencyKey(key *string) error {
	if key == nil {
		return nil
	}
	k := *key
	ok := len(k) >= 1 && len(k) <= idempotencyKeyMaxChars
	for i := 0; ok && i < len(k); i++ {
		ok = k[i] >= 0x20 && k[i] <= 0x7e
	}
	if !ok {
		return newError(codeInvalidRequest, 0,
			"The idempotency key must be 1-128 printable ASCII characters (U+0020-U+007E).")
	}
	return nil
}

// requestBody returns the payload's JSON and the enqueue request body, after
// the size check on the exact bytes sent. Never quotes the payload in an error.
func requestBody(payload any, key *string) (payloadJSON, body []byte, err error) {
	payloadJSON, err = json.Marshal(payload)
	if err != nil {
		// Covers NaN / Inf, cycles, channels, functions and invalid
		// json.RawMessage values.
		return nil, nil, newError(codeInvalidRequest, 0,
			"The task payload cannot be serialized as JSON (NaN, Inf, cycles and "+
				"non-JSON types such as channels or functions are not allowed).")
	}
	body, err = json.Marshal(struct {
		Payload        json.RawMessage `json:"payload"`
		IdempotencyKey *string         `json:"idempotency_key,omitempty"`
	}{payloadJSON, key})
	if err != nil {
		return nil, nil, newError(codeInvalidRequest, 0, "The task payload cannot be serialized as JSON.")
	}
	if len(body) > MaxBodyBytes {
		return nil, nil, newError(codePayloadTooLarge, 0,
			"The enqueue request body is %d bytes; the limit is %d. Store large data "+
				"elsewhere (for example in the database) and pass its ID in the payload.",
			len(body), MaxBodyBytes)
	}
	return payloadJSON, body, nil
}

// ---------------------------------------------------------------------------
// Response parsing (both modes)
// ---------------------------------------------------------------------------

func parseRFC3339(s string) (time.Time, bool) {
	m := rfc3339RE.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	if m[7] != "" {
		h, _ := strconv.Atoi(m[7])
		mi, _ := strconv.Atoi(m[8])
		if h > 23 || mi > 59 {
			return time.Time{}, false
		}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func jsonObject(raw []byte) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, false
	}
	return obj, true
}

func isNull(raw json.RawMessage) bool { return string(raw) == "null" }

func jsonString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

func nonEmptyString(obj map[string]json.RawMessage, key string) (string, bool) {
	s, ok := jsonString(obj[key])
	return s, ok && s != ""
}

// parseStatus maps a § 9.3 object to TaskStatus. Unknown fields are ignored;
// an unknown status value is kept.
func parseStatus(raw []byte) (*TaskStatus, bool) {
	obj, ok := jsonObject(raw)
	if !ok {
		return nil, false
	}
	var s TaskStatus
	if s.TaskID, ok = nonEmptyString(obj, "task_id"); !ok {
		return nil, false
	}
	if s.Name, ok = nonEmptyString(obj, "name"); !ok {
		return nil, false
	}
	if s.Status, ok = nonEmptyString(obj, "status"); !ok {
		return nil, false
	}
	attempts := string(obj["claimed_attempts"])
	if !nonNegativeIntRE.MatchString(attempts) {
		return nil, false
	}
	n, err := strconv.Atoi(attempts)
	if err != nil {
		return nil, false
	}
	s.ClaimedAttempts = n
	created, ok := jsonString(obj["created_at"])
	if !ok {
		return nil, false
	}
	if s.CreatedAt, ok = parseRFC3339(created); !ok {
		return nil, false
	}
	if lf := obj["last_failure_code"]; !isNull(lf) {
		code, ok := jsonString(lf)
		if !ok {
			return nil, false
		}
		s.LastFailureCode = &code
	}
	if fa := obj["finished_at"]; !isNull(fa) {
		text, ok := jsonString(fa)
		if !ok {
			return nil, false
		}
		t, ok := parseRFC3339(text)
		if !ok {
			return nil, false
		}
		s.FinishedAt = &t
	}
	return &s, true
}

func unexpected(status int, what string) *Error {
	return newError(codeUnexpectedResponse, status, "The Tasks API returned an unexpected %s response.", what)
}

func parseEnqueueResponse(status int, raw []byte) (string, error) {
	obj, ok := jsonObject(raw)
	if !ok {
		return "", unexpected(status, "enqueue")
	}
	taskID, ok := nonEmptyString(obj, "task_id")
	if !ok {
		return "", unexpected(status, "enqueue")
	}
	return taskID, nil
}

// ---------------------------------------------------------------------------
// Error mapping
// ---------------------------------------------------------------------------

// envelope returns (code, message) from a {"error": {"code": ...}} body.
func envelope(raw []byte) (code, message string, ok bool) {
	top, ok := jsonObject(raw)
	if !ok {
		return "", "", false
	}
	inner, ok := jsonObject(top["error"])
	if !ok {
		return "", "", false
	}
	if code, ok = nonEmptyString(inner, "code"); !ok {
		return "", "", false
	}
	if message, ok = nonEmptyString(inner, "message"); !ok {
		message = code
	}
	return code, message, true
}

func transientError(status int, detail string) *Error {
	return newError(codeTransient, status, "The Tasks API is temporarily unreachable (%s).", detail)
}

// httpError classifies a non-success response and reports whether it is
// transient. The order is fixed (T-1037 design 3) and the first hit wins.
func httpError(status int, raw []byte) (*Error, bool) {
	switch status {
	case http.StatusUnauthorized:
		return newError(codeUnauthorized, status, "The Tasks API rejected the identity token (401)."), false
	case http.StatusForbidden:
		return newError(codeForbidden, status,
			"This app's service account may not call the Tasks API (403). Right after "+
				"the first deploy that declares tasks:, the permission can take a few "+
				"minutes to propagate."), false
	}
	if code, message, ok := envelope(raw); ok {
		return &Error{Code: code, Status: status, Message: message}, false
	}
	switch {
	case status == http.StatusBadGateway || status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout:
		return transientError(status, fmt.Sprintf("HTTP %d", status)), true
	case status >= 500 && status <= 599:
		return newError(codeServerError, status, "The Tasks API failed with HTTP %d.", status), false
	}
	return newError(codeHTTPError, status, "The Tasks API answered with HTTP %d.", status), false
}

// ---------------------------------------------------------------------------
// Remote (runtime API) backend
// ---------------------------------------------------------------------------

// identityToken fetches a fresh OIDC id token (never cached) whose audience is
// KEELSON_TASKS_BASE_URL verbatim.
func (c *Client) identityToken(ctx context.Context) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	u := c.metadataURL + "?audience=" + url.QueryEscape(c.audience)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return "", newError(codeIdentityToken, 0, "Invalid metadata server URL: %v", err)
	}
	req.Header.Set("Metadata-Flavor", "Google")
	req.Header.Set("User-Agent", sdkUserAgent)
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", newError(codeIdentityToken, 0,
			"Could not reach the metadata server for an identity token: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode != http.StatusOK {
		return "", newError(codeIdentityToken, 0,
			"The metadata server refused the identity token request (HTTP %d).", resp.StatusCode)
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", newError(codeIdentityToken, 0,
			"Could not read the identity token from the metadata server: %v", err)
	}
	token := strings.TrimSpace(string(raw))
	usable := token != ""
	for i := 0; usable && i < len(token); i++ {
		usable = token[i] > 0x20 && token[i] < 0x7f
	}
	if !usable {
		return "", newError(codeIdentityToken, 0, "The metadata server returned no usable identity token.")
	}
	return token, nil
}

// do sends one runtime API request; a non-nil error is a transport failure.
func (c *Client) do(ctx context.Context, method, rawURL string, body []byte) (int, []byte, error) {
	token, err := c.identityToken(ctx)
	if err != nil {
		return 0, nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, rawURL, reader)
	if err != nil {
		return 0, nil, newError(codeNotConfigured, 0, "Invalid KEELSON_TASKS_BASE_URL: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", sdkUserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, transportError(ctx, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, transportError(ctx, err)
	}
	return resp.StatusCode, raw, nil
}

func transportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return transientError(0, fmt.Sprintf("timed out after %s", requestTimeout))
	}
	return transientError(0, err.Error())
}

// call sends a runtime API request, retried on transient failures when retry.
func (c *Client) call(ctx context.Context, method, path string, body []byte, okStatus func(int) bool, retry bool, noRetryHint string) (int, []byte, error) {
	attempts := 1
	if retry {
		attempts = len(retryDelays) + 1
	}
	var last *Error
	for attempt := range attempts {
		if attempt > 0 {
			if err := sleep(ctx, retryDelays[attempt-1]); err != nil {
				return 0, nil, err
			}
		}
		status, raw, err := c.do(ctx, method, c.apiBase+path, body)
		if err != nil {
			var te *Error
			if errors.As(err, &te) && te.Code == codeTransient {
				last = te
				continue
			}
			return 0, nil, err
		}
		if okStatus(status) {
			return status, raw, nil
		}
		e, transient := httpError(status, raw)
		if !transient {
			return 0, nil, e
		}
		last = e
	}
	if !retry && noRetryHint != "" {
		last = &Error{Code: last.Code, Status: last.Status, Message: last.Message + " " + noRetryHint}
	}
	return 0, nil, last
}

func (c *Client) tasksPath(rest string) string {
	return "/internal/apps/" + url.PathEscape(c.appID) + "/tasks/" + rest
}

func (c *Client) enqueueRemote(ctx context.Context, name string, body []byte, hasKey bool) (string, error) {
	status, raw, err := c.call(ctx, http.MethodPost,
		c.tasksPath(url.PathEscape(name)+"/enqueue"), body,
		func(s int) bool { return s == http.StatusOK || s == http.StatusAccepted },
		// Without a key, a lost response may hide an accepted task: a resend
		// could enqueue it twice.
		hasKey,
		"Not retried: without an idempotency key the task may already have been "+
			"accepted. Pass WithIdempotencyKey so the SDK can retry safely.")
	if err != nil {
		return "", err
	}
	return parseEnqueueResponse(status, raw)
}

func (c *Client) getRemote(ctx context.Context, taskID string) (*TaskStatus, error) {
	status, raw, err := c.call(ctx, http.MethodGet, c.tasksPath(url.PathEscape(taskID)), nil,
		func(s int) bool { return s == http.StatusOK }, true, "")
	if err != nil {
		return nil, err
	}
	s, ok := parseStatus(raw)
	if !ok {
		return nil, unexpected(status, "get")
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Local backend — `keelson dev task run` per enqueue, results kept in-process
// ---------------------------------------------------------------------------

type localKey struct{ name, key string }

// Process-wide, so every Client from New sees the same results.
var local = struct {
	mu      sync.Mutex
	results map[string]TaskStatus
	keys    map[localKey]string
}{results: map[string]TaskStatus{}, keys: map[localKey]string{}}

func cliFailed(format string, args ...any) *Error {
	return newError(codeLocalCLIFailed, 0,
		"`keelson dev task run` failed: %s If the CLI is older than this SDK, run `keelson upgrade`.",
		fmt.Sprintf(format, args...))
}

func cliNotFound() *Error {
	return newError(codeLocalCLINotFound, 0,
		"Local tasks run through the Keelson CLI, but `keelson` was not found on "+
			"PATH. Install it with `curl -fsSL %s | sh`, or set KEELSON_MODE=keelson "+
			"on the platform.", installURL)
}

func runLocalCLI(ctx context.Context, name string, payload []byte) (*TaskStatus, error) {
	cli, err := exec.LookPath("keelson")
	if err != nil {
		return nil, cliNotFound()
	}
	cmd := exec.CommandContext(ctx, cli, "dev", "task", "run", name, "--payload", "-", "--json")
	cmd.Stdin = bytes.NewReader(payload)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// stderr is inherited: the task's logs show up in the dev server's
	// terminal. The exit code is not consulted; stdout alone decides.
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error {
		if runtime.GOOS == "windows" {
			return cmd.Process.Kill()
		}
		// The CLI forwards SIGTERM to the command's process group.
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = localCancelWaitDelay
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, cliNotFound()
		}
		return nil, cliFailed("could not start %s (%v).", cli, err)
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	obj, ok := jsonObject(bytes.TrimSpace(stdout.Bytes()))
	if !ok {
		if waitErr != nil && !isExitError(waitErr) {
			return nil, cliFailed("%v, without a JSON result on stdout.", waitErr)
		}
		return nil, cliFailed("it exited with code %d without a JSON result on stdout.",
			cmd.ProcessState.ExitCode())
	}
	if task, has := obj["task"]; has {
		result, ok := parseStatus(task)
		if !ok {
			return nil, cliFailed("its JSON result is not a valid task.")
		}
		return result, nil
	}
	inner, ok := jsonObject(obj["error"])
	code, hasCode := "", false
	if ok {
		code, hasCode = nonEmptyString(inner, "code")
	}
	if !hasCode {
		return nil, cliFailed("its JSON output has neither a task nor an error code.")
	}
	var parts []string
	for _, k := range []string{"message", "hint"} {
		if s, ok := nonEmptyString(inner, k); ok {
			parts = append(parts, s)
		}
	}
	detail := strings.Join(parts, " ")
	if localPassthroughCodes[code] {
		if detail == "" {
			detail = code
		}
		return nil, &Error{Code: code, Message: detail}
	}
	if detail != "" {
		return nil, cliFailed("%s: %s", code, detail)
	}
	return nil, cliFailed("%s.", code)
}

func isExitError(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee)
}

func enqueueLocal(ctx context.Context, name string, payload []byte, idempotencyKey *string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if !taskNameRE.MatchString(normalized) {
		// No declaration can have this name; also keeps a name like "--json"
		// from being read as a CLI flag.
		return "", newError(codeNotDeclared, 0,
			"Task %q is not declared in keelson.yaml (task names are 1-63 of a-z, 0-9 and '-').",
			normalized)
	}
	var key *localKey
	if idempotencyKey != nil {
		key = &localKey{name: normalized, key: *idempotencyKey}
		local.mu.Lock()
		existing, ok := local.keys[*key]
		local.mu.Unlock()
		if ok {
			return existing, nil
		}
	}
	// The lock is not held while the command runs: tasks may enqueue tasks.
	// Concurrent calls with the same key may both run the CLI; the first to
	// finish keeps the key.
	result, err := runLocalCLI(ctx, name, payload)
	if err != nil {
		return "", err
	}
	local.mu.Lock()
	defer local.mu.Unlock()
	local.results[result.TaskID] = *result
	if key != nil {
		if _, ok := local.keys[*key]; !ok {
			local.keys[*key] = result.TaskID
		}
	}
	return result.TaskID, nil
}

func getLocal(taskID string) (*TaskStatus, error) {
	local.mu.Lock()
	result, ok := local.results[taskID]
	local.mu.Unlock()
	if !ok {
		return nil, newError(codeNotFound, 0,
			"No task with this ID was enqueued in this process (local mode keeps results in memory only).")
	}
	return result.clone(), nil
}
