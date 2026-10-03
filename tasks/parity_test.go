package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/keelsonhq/go-sdk/internal/testfixtures"
)

func readFixture(t *testing.T, name string, dest any) {
	t.Helper()
	raw, err := testfixtures.ReadFile(name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
}

// wantStatus is a fixture's expected get result (the seven § 9.3 fields).
type wantStatus struct {
	TaskID          string  `json:"task_id"`
	Name            string  `json:"name"`
	Status          string  `json:"status"`
	ClaimedAttempts int     `json:"claimed_attempts"`
	LastFailureCode *string `json:"last_failure_code"`
	CreatedAt       string  `json:"created_at"`
	FinishedAt      *string `json:"finished_at"`
}

func assertStatus(t *testing.T, got *TaskStatus, want wantStatus) {
	t.Helper()
	if got.TaskID != want.TaskID || got.Name != want.Name || got.Status != want.Status ||
		got.ClaimedAttempts != want.ClaimedAttempts {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if (got.LastFailureCode == nil) != (want.LastFailureCode == nil) ||
		(got.LastFailureCode != nil && *got.LastFailureCode != *want.LastFailureCode) {
		t.Fatalf("LastFailureCode = %v, want %v", got.LastFailureCode, want.LastFailureCode)
	}
	created, err := time.Parse(time.RFC3339Nano, want.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}
	if want.FinishedAt == nil {
		if got.FinishedAt != nil {
			t.Fatalf("FinishedAt = %v, want nil", got.FinishedAt)
		}
		return
	}
	finished, err := time.Parse(time.RFC3339Nano, *want.FinishedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.FinishedAt == nil || !got.FinishedAt.Equal(finished) {
		t.Fatalf("FinishedAt = %v, want %v", got.FinishedAt, finished)
	}
}

// TestParity_TasksModeResolution drives the shared mode-resolution fixture
// through New, asserting the same decisions as the Node and Python SDKs.
func TestParity_TasksModeResolution(t *testing.T) {
	var fx struct {
		EnvVars []string `json:"env_vars"`
		Cases   []struct {
			Name     string            `json:"name"`
			Env      map[string]string `json:"env"`
			Expected struct {
				Mode      string `json:"mode"`
				Audience  string `json:"audience"`
				APIBase   string `json:"api_base"`
				AppID     string `json:"app_id"`
				ErrorCode string `json:"error_code"`
			} `json:"expected"`
		} `json:"cases"`
	}
	readFixture(t, "tasks_mode_resolution.json", &fx)
	for _, name := range fx.EnvVars {
		if !strings.HasPrefix(name, "KEELSON_") {
			t.Fatalf("unexpected env var %q", name)
		}
	}
	for _, tc := range fx.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			clearEnv(t)
			for _, name := range fx.EnvVars {
				t.Setenv(name, "")
				_ = os.Unsetenv(name)
			}
			for k, v := range tc.Env {
				t.Setenv(k, v)
			}
			c, err := New()
			want := tc.Expected
			if want.ErrorCode != "" {
				if c != nil || err == nil {
					t.Fatalf("New = %+v, %v; want error %s", c, err, want.ErrorCode)
				}
				if te := asTasksError(t, err); te.Code != want.ErrorCode || te.Status != 0 {
					t.Fatalf("error = %+v, want %s", te, want.ErrorCode)
				}
				if !errors.Is(err, ErrConfig) {
					t.Fatalf("errors.Is(%v, ErrConfig) = false", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			switch want.Mode {
			case "local":
				if !c.IsLocal() {
					t.Fatalf("got remote %+v, want local", c)
				}
			case "remote":
				if c.IsLocal() || c.audience != want.Audience || c.apiBase != want.APIBase ||
					c.appID != want.AppID {
					t.Fatalf("got %+v, want %+v", c, want)
				}
			default:
				t.Fatalf("unknown expected mode %q", want.Mode)
			}
		})
	}
}

// TestParity_TasksErrorMapping drives the shared error-mapping fixture: every
// HTTP case through Get (always retried) and Enqueue without a key (never
// retried), and the transport failures.
func TestParity_TasksErrorMapping(t *testing.T) {
	type expected struct {
		Code      string `json:"code"`
		Status    *int   `json:"status"`
		Transient bool   `json:"transient"`
	}
	var fx struct {
		HTTP []struct {
			Name     string   `json:"name"`
			Status   int      `json:"status"`
			Body     string   `json:"body"`
			Expected expected `json:"expected"`
		} `json:"http"`
		Transport []struct {
			Name     string   `json:"name"`
			Failure  string   `json:"failure"`
			Expected expected `json:"expected"`
		} `json:"transport"`
	}
	readFixture(t, "tasks_error_mapping.json", &fx)

	check := func(t *testing.T, err error, want expected, wantStatus int, f *fakeAPI, wantAttempts int) {
		t.Helper()
		te := asTasksError(t, err)
		if te.Code != want.Code || te.Status != wantStatus {
			t.Fatalf("error = %+v, want code %s status %d", te, want.Code, wantStatus)
		}
		if got := len(f.apiRequests()); got != wantAttempts {
			t.Fatalf("attempts = %d, want %d", got, wantAttempts)
		}
	}

	for _, tc := range fx.HTTP {
		t.Run(tc.Name, func(t *testing.T) {
			attempts := 1
			if tc.Expected.Transient {
				attempts = 3
			}
			c, f := newRemote(t, respond(tc.Status, tc.Body))
			_, err := c.Get(context.Background(), "t-1")
			check(t, err, tc.Expected, *tc.Expected.Status, f, attempts)

			c, f = newRemote(t, respond(tc.Status, tc.Body))
			_, err = c.Enqueue(context.Background(), "generate-pdf", nil)
			check(t, err, tc.Expected, *tc.Expected.Status, f, 1)
		})
	}

	for _, tc := range fx.Transport {
		t.Run(tc.Name, func(t *testing.T) {
			if !tc.Expected.Transient {
				t.Fatalf("transport case %s must be transient", tc.Name)
			}
			switch tc.Failure {
			case "connection_error":
				c, f := newRemote(t, nil)
				// The metadata server stays up; the runtime API refuses connections.
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				addr := ln.Addr().String()
				_ = ln.Close()
				c.apiBase = "http://" + addr
				_, err = c.Get(context.Background(), "t-1")
				check(t, err, tc.Expected, 0, f, 0)
				if len(f.sleeps) != 2 {
					t.Fatalf("sleeps = %v, want 2 retries", f.sleeps)
				}
			case "timeout":
				orig := requestTimeout
				requestTimeout = 50 * time.Millisecond
				t.Cleanup(func() { requestTimeout = orig })
				c, f := newRemote(t, func(_ http.ResponseWriter, r *http.Request, _ int) {
					<-r.Context().Done()
				})
				_, err := c.Get(context.Background(), "t-1")
				check(t, err, tc.Expected, 0, f, 3)
			default:
				t.Fatalf("unknown transport failure %q", tc.Failure)
			}
		})
	}
}

// TestParity_TasksGetResponse drives the shared success-response fixture
// through Get (200) and Enqueue (202 / 200).
func TestParity_TasksGetResponse(t *testing.T) {
	var fx struct {
		Get []struct {
			Name     string `json:"name"`
			Body     string `json:"body"`
			Expected struct {
				Status    *wantStatus `json:"status"`
				ErrorCode string      `json:"error_code"`
			} `json:"expected"`
		} `json:"get"`
		Enqueue []struct {
			Name     string `json:"name"`
			Status   int    `json:"status"`
			Body     string `json:"body"`
			Expected struct {
				TaskID    string `json:"task_id"`
				ErrorCode string `json:"error_code"`
			} `json:"expected"`
		} `json:"enqueue"`
	}
	readFixture(t, "tasks_get_response.json", &fx)

	for _, tc := range fx.Get {
		t.Run("get/"+tc.Name, func(t *testing.T) {
			c, _ := newRemote(t, respond(http.StatusOK, tc.Body))
			got, err := c.Get(context.Background(), "t-1")
			if tc.Expected.ErrorCode != "" {
				if te := asTasksError(t, err); te.Code != tc.Expected.ErrorCode || te.Status != 200 {
					t.Fatalf("error = %+v, want %s", te, tc.Expected.ErrorCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			assertStatus(t, got, *tc.Expected.Status)
		})
	}
	for _, tc := range fx.Enqueue {
		t.Run("enqueue/"+tc.Name, func(t *testing.T) {
			c, _ := newRemote(t, respond(tc.Status, tc.Body))
			got, err := c.Enqueue(context.Background(), "generate-pdf", nil)
			if tc.Expected.ErrorCode != "" {
				if te := asTasksError(t, err); te.Code != tc.Expected.ErrorCode || te.Status != tc.Status {
					t.Fatalf("error = %+v, want %s", te, tc.Expected.ErrorCode)
				}
				return
			}
			if err != nil || got != tc.Expected.TaskID {
				t.Fatalf("Enqueue = %q, %v; want %q", got, err, tc.Expected.TaskID)
			}
		})
	}
}

// TestParity_TasksLocalCLIResult drives the shared CLI-result fixture through
// a fake `keelson` CLI that prints each case's stdout.
func TestParity_TasksLocalCLIResult(t *testing.T) {
	var fx struct {
		Argv  []string `json:"argv"`
		Cases []struct {
			Name       string          `json:"name"`
			Stdout     json.RawMessage `json:"stdout"`
			StdoutText *string         `json:"stdout_text"`
			SDK        struct {
				TaskID    string      `json:"task_id"`
				Status    *wantStatus `json:"status"`
				ErrorCode string      `json:"error_code"`
			} `json:"sdk"`
		} `json:"cases"`
	}
	readFixture(t, "tasks_local_cli_result.json", &fx)

	for _, tc := range fx.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			cli := installFakeCLI(t)
			out := ""
			if tc.StdoutText != nil {
				out = *tc.StdoutText
			} else {
				var v any
				if err := json.Unmarshal(tc.Stdout, &v); err != nil {
					t.Fatal(err)
				}
				b, _ := json.Marshal(v)
				out = string(b) + "\n"
			}
			t.Setenv("FAKE_CLI_STDOUT", out)
			if tc.Name != "succeeded" {
				// The SDK ignores the exit code; stdout alone decides.
				t.Setenv("FAKE_CLI_EXIT", "1")
			}
			c := newLocalClient(t)
			taskID, err := c.Enqueue(context.Background(), "generate-pdf", map[string]int{"order_id": 1})

			calls := cli.calls(t)
			if len(calls) != 1 {
				t.Fatalf("CLI calls = %d, want 1", len(calls))
			}
			wantArgv := strings.Join(fx.Argv, " ")
			wantArgv = strings.Replace(wantArgv, "<name>", "generate-pdf", 1)
			if got := strings.Join(calls[0].Argv, " "); got != wantArgv {
				t.Fatalf("argv = %q, want %q", got, wantArgv)
			}

			if tc.SDK.ErrorCode != "" {
				te := asTasksError(t, err)
				if te.Code != tc.SDK.ErrorCode || te.Status != 0 {
					t.Fatalf("error = %+v, want %s", te, tc.SDK.ErrorCode)
				}
				if te.Code == codeLocalCLIFailed && !strings.Contains(te.Message, "keelson upgrade") {
					t.Fatalf("message %q lacks the upgrade hint", te.Message)
				}
				return
			}
			if err != nil || taskID != tc.SDK.TaskID {
				t.Fatalf("Enqueue = %q, %v; want %q", taskID, err, tc.SDK.TaskID)
			}
			got, err := c.Get(context.Background(), taskID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			assertStatus(t, got, *tc.SDK.Status)
		})
	}
}
