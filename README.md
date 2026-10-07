# Keelson Go SDK

SDK guide: https://keelson.dev/docs/building-apps/sdk/

Go SDK for building apps on the Keelson platform. Provides five packages:

> **Note**: This repository is a read-only release mirror. Development happens in the private Keelson monorepo; issues are welcome here, but pull requests are not accepted — changes land through the next release.

| Package | Import path | Description |
|---------|-------------|-------------|
| `identity` | `github.com/keelsonhq/go-sdk/identity` | Authenticated user identity |
| `directory` | `github.com/keelsonhq/go-sdk/directory` | Workspace member and group directory |
| `media` | `github.com/keelsonhq/go-sdk/media` | Media storage (upload, serve by ID) |
| `files` | `github.com/keelsonhq/go-sdk/files` | Data files (key-addressed, overwrite, private) |
| `tasks` | `github.com/keelsonhq/go-sdk/tasks` | Background tasks (enqueue, get) |

Cross-language parity across Node, Python, and Go is defined by the
[cross-SDK parity contract shipped in this repo](./PARITY.md). APIs below are
labelled as **guaranteed** (same capability in all 3 languages) or
**Go-specific** (convenience helpers unique to this SDK).

## Installation

```bash
go get github.com/keelsonhq/go-sdk@latest
```

Import only the packages you need.

---

## Identity

Get the authenticated user's identity. In production, the Keelson auth gateway
injects trusted `X-Keelson-User-*` headers before requests reach the app.
Use `GetCurrentUser` when the basic user profile is enough, `GetRequestUser`
when the app also needs the user's permissions on this app (`view` /
`manage`), and `GetCurrentIdentity` when the app needs workspace role, app
permissions, app roles, or group attributes.

```go
import (
    "fmt"
    "os"

    "github.com/keelsonhq/go-sdk/identity"
)

client, err := identity.New("") // reads KEELSON_DIRECTORY_BASE_URL (canonical; deprecated KEELSON_IDENTITY_BASE_URL fallback)
if err != nil { /* ... */ }

user, err := client.GetCurrentUser(identity.WithHeaders(r.Header))
if err != nil { /* ... */ }
fmt.Println(user.ID)

requestUser, err := client.GetRequestUser(identity.WithHeaders(r.Header))
if err != nil { /* ... */ }
fmt.Println(requestUser.Perms) // ["view", "manage"]

current, err := client.GetCurrentIdentity(
    identity.WithHeaders(r.Header),
    identity.WithAppToken(os.Getenv("KEELSON_DIRECTORY_TOKEN")),
    identity.WithHost(r.Host),
)
if err != nil { /* ... */ }
fmt.Println(current.Workspace.Role)
fmt.Println(current.App.Permissions) // ["manage", "view"]
```

### Cross-language guaranteed API

| Method | Description |
|--------|-------------|
| `New(baseURL) (*Client, error)` | Create client (falls back to `KEELSON_DIRECTORY_BASE_URL`, then the deprecated `KEELSON_IDENTITY_BASE_URL`) |
| `GetCurrentUser(opts...) (*UserIdentity, error)` | Parse the current user's basic profile from trusted `X-Keelson-User-*` headers; no network call |
| `GetRequestUser(opts...) (*RequestUser, error)` | `GetCurrentUser` plus `Perms` from `X-Keelson-User-App-Perms`; no network call |
| `GetCurrentIdentity(opts...) (*CurrentIdentity, error)` | Fetch the current user's full identity as the app actor |
| `WithHeaders(headers) RequestOption` | Supply incoming request headers for current-user lookup |
| `WithAppToken(token) RequestOption` | App-as-actor current identity lookup via `Authorization: Bearer <token>` |

### Go-specific helpers

| Method | Description |
|--------|-------------|
| `IsLocal() bool` | Whether client is in local mode |

### Request options

| Option | Description |
|--------|-------------|
| `WithHeaders(headers)` | Incoming request headers; `x-keelson-user-id` is required, `x-keelson-user-email` and `x-keelson-user-name` are optional |
| `WithAppToken(token)` | App token for `GetCurrentIdentity` |
| `WithAuthorization(auth)` | Explicit `Bearer keelson_...` app-token authorization for `GetCurrentIdentity` |
| `WithCookie(cookie)` | Rejected by `GetCurrentIdentity`; ignored by `GetCurrentUser` |
| `WithHost(host)` | Host forwarding option for `GetCurrentIdentity`; ignored by `GetCurrentUser` |

`GetCurrentIdentity` is app-token only. It reads the subject user id from
trusted headers and rejects explicit `WithCookie`.

`RequestUser` has `ID`, `Email *string`, `Name *string`, and `Perms []string`.
`Perms` is `X-Keelson-User-App-Perms` split on `,` with blanks dropped, in
header order (empty when the header is absent, as on machine and webhook
calls). A missing `X-Keelson-User-Id` is the same error as `GetCurrentUser`.
`GetRequestUser` also restores non-ASCII values (such as a Japanese name) that
a framework handed over as raw UTF-8 bytes read as latin-1;
`GetCurrentUser` does not.

### Migration note

`GetCurrentUser` no longer forwards browser cookies to `/__keelson/user` and no
longer returns workspace/app authorization fields. Replace old
`GetCurrentUser(WithCookie(...), WithHost(...))` calls with
`GetCurrentUser(WithHeaders(r.Header))` for basic user fields, or
`GetCurrentIdentity(WithHeaders(r.Header), WithAppToken(...), WithHost(r.Host))`
when you need `Workspace.Role`, `App.Permissions`, `App.Roles`, or
`Attributes.Groups`.

### Modes

| Mode | Condition | Behaviour |
|------|-----------|-----------|
| Local | `KEELSON_LOCAL_MODE` set | Returns deterministic fixture data (no HTTP calls, headers ignored) |
| Keelson | Default | Calls the Keelson auth gateway via `KEELSON_DIRECTORY_BASE_URL` (canonical, platform-injected). `KEELSON_IDENTITY_BASE_URL` is a **deprecated** fallback only |

### Local mode

Local mode is for local development only. `identity.New` and `directory.New`
return an error when `KEELSON_LOCAL_MODE` is set in a Keelson deployment
(`KEELSON_MODE=keelson`, or a non-blank `KEELSON_APP_ID`,
`KEELSON_WORKSPACE_ID`, `KEELSON_TENANT_ID`, `KEELSON_DEPLOY_ID`, or
`KEELSON_APP_URL`); they never fall back to the production path.

The local users come from a users file: `KEELSON_LOCAL_USERS_FILE`, or
`./.keelson/dev-users.json` when that exists (relative paths resolve from the
process working directory). `New` returns an error when the file is missing
(when named explicitly), unreadable, or malformed. Restart the app after
editing it.

```json
{
  "users": [
    { "id": "sample-tanaka", "email": "tanaka@example.com", "name": "Tanaka Taro", "perms": ["view", "manage"] },
    { "id": "sample-sato", "email": "sato@example.com", "name": "Sato Hanako", "perms": ["view"], "image_url": null }
  ]
}
```

- `users` has at least one entry; `id` is non-empty and unique; `email` and
  `name` are strings (may be empty); `perms` is `["view"]` or
  `["view", "manage"]` in any order; `image_url` is optional. Unknown keys are
  ignored.
- The current user (`GetCurrentUser` / `GetRequestUser` /
  `GetCurrentIdentity`) is the first user with `manage`, or the first user.
- A user with `manage` has role `ADMIN` and groups `admins` and `everyone`;
  otherwise `APP_USER` and `everyone`. `ListGroups` returns `admins` and
  `everyone`. `ListMembers` keeps the file order.
- With a users file, `KEELSON_LOCAL_USER_*` and `KEELSON_LOCAL_WORKSPACE_ROLE`
  are ignored; `KEELSON_LOCAL_WORKSPACE_ID` and `KEELSON_LOCAL_APP_ID` still
  apply.

Without a users file, local mode returns the built-in fixture data:
the `KEELSON_LOCAL_USER_*` user, plus Alice, Bob, and Carol as members, and
`GetRequestUser` reports `Perms` `["view", "manage"]`.

### Environment variables

| Variable | Description |
|----------|-------------|
| `KEELSON_DIRECTORY_BASE_URL` | **Canonical, platform-injected** base URL for `GetCurrentIdentity` and Directory calls. Use this |
| `KEELSON_DIRECTORY_TOKEN` | App token for app-as-actor current identity and Directory access |
| `KEELSON_IDENTITY_BASE_URL` | **Deprecated** compatibility fallback for the base URL, used only when `KEELSON_DIRECTORY_BASE_URL` and an explicit `baseURL` are both absent |
| `KEELSON_LOCAL_MODE` | Set to `1`, `true`, or `yes` to use fixture data (local development only; refused in a Keelson deployment) |
| `KEELSON_LOCAL_USERS_FILE` | Local mode users file (default: `./.keelson/dev-users.json` when it exists) |
| `KEELSON_LOCAL_USER_ID` | Override local user ID (default: `local-user-001`) |
| `KEELSON_LOCAL_USER_EMAIL` | Override local user email (default: `dev@localhost`) |
| `KEELSON_LOCAL_USER_NAME` | Override local user name (default: `Local Developer`) |
| `KEELSON_LOCAL_WORKSPACE_ID` | Override local workspace ID (the compatibility-preserving default remains `local-tenant-001`) |
| `KEELSON_LOCAL_WORKSPACE_ROLE` | Override local workspace role (default: `OWNER`) |
| `KEELSON_LOCAL_APP_ID` | Override local app ID (default: `local-app-001`) |

`KEELSON_IDENTITY_BASE_URL` is a deprecated alias, but it still works as a
fallback; new code should use `KEELSON_DIRECTORY_BASE_URL`.

The former `TenantIdentity` type, `CurrentIdentity.Tenant` field, `tenant` wire
key, `KEELSON_TENANT_ID`, and `KEELSON_LOCAL_TENANT_ID` /
`KEELSON_LOCAL_TENANT_ROLE` remain deprecated aliases through at least the next
major SDK version.

---

## Directory

Lookup workspace members and groups. Same forwarding pattern as Identity.
Supports `KEELSON_LOCAL_MODE` with the same fixture data, users file, and env
overrides (see Identity's "Local mode").

```go
import "github.com/keelsonhq/go-sdk/directory"

client, err := directory.New("") // local mode or KEELSON_DIRECTORY_BASE_URL (canonical; deprecated KEELSON_IDENTITY_BASE_URL fallback)
if err != nil { /* ... */ }

opts := []directory.RequestOption{
    directory.WithCookie(r.Header.Get("Cookie")),
    directory.WithHost(r.Host),
}

// List members with search
page, err := client.ListMembers(&directory.ListMembersParams{
    Q:     "alice",
    Limit: intPtr(25),
}, opts...)

for _, m := range page.Items {
    fmt.Println(m.Name, m.Email, m.Role)
}

// Get user by ID
user, err := client.GetUser("user-id", opts...)

// List groups
groups, err := client.ListGroups(opts...)
```

### App-as-actor Directory access

`ListMembers`, `GetUser`, and `ListGroups` can run as the app itself instead of
the logged-in user, using a Directory-scoped app token. This is the right mode
for cron runs and bulk operations where there is no user request to forward.

```go
// reads KEELSON_DIRECTORY_BASE_URL (or KEELSON_IDENTITY_BASE_URL)
client, err := directory.New("")
if err != nil { /* ... */ }

// Explicit app token:
page, err := client.ListMembers(nil,
    directory.WithAppToken(os.Getenv("KEELSON_DIRECTORY_TOKEN")),
)

// Or omit it — the token is read from KEELSON_DIRECTORY_TOKEN automatically:
page, err = client.ListMembers(nil)
```

`WithAppToken` sends `Authorization: Bearer <token>` and makes the app the
actor. `WithAppToken` and `WithAuthorization` both set the credential — pass
only one; if both are given, the option applied last wins. `WithAppToken` also
drops any Cookie set by `WithCookie`, so a user-context cookie cannot ride
along and silently turn the call into app-as-actor. When `WithCookie` is used
without `WithAppToken` (user-as-actor), the `KEELSON_DIRECTORY_TOKEN` fallback
is skipped. Directory app tokens must never reach the browser; keep them on
the server.

### Filtering members by group

`ListMembersParams` narrows `ListMembers` to a single group via either
`GroupID` or `GroupKey`:

- `GroupKey` — the group's code-facing identifier. Always present, stable, and
  immutable. **Prefer this for code references.** Keys may be non-ASCII (e.g. a
  Japanese `経理`).
- `GroupID` — a stable UUID for machine integration / internal wiring.

Setting both is rejected. On `GroupItem`, `Key` is a `*string` kept nullable
for backward compatibility, but the server always populates it; `ID` is the
UUID.

### Cross-language guaranteed API

| Method | Description |
|--------|-------------|
| `New(baseURL) (*Client, error)` | Create client (local mode, or `KEELSON_DIRECTORY_BASE_URL` → `KEELSON_IDENTITY_BASE_URL`) |
| `ListMembers(params, opts...) (*PaginatedMembers, error)` | List workspace members (paginated, filterable) |
| `GetUser(userID, opts...) (*MemberItem, error)` | Get user by ID |
| `ListGroups(opts...) ([]GroupItem, error)` | List workspace groups |
| `WithAppToken(token) RequestOption` | App-as-actor Directory access via `Authorization: Bearer <token>` |

`MemberItem` carries `ID`, `Email`, `Name`, `Role`, and `ImageURL`
(JSON `image_url`). `ImageURL` is a `*string`: the member's profile image URL
served by Clerk (`img.clerk.com`), or `nil` when the member has not uploaded an
image (render initials instead). Append `width` / `height` query parameters to
get a resized image. Store only the member `ID` in your app's DB and re-fetch
`ImageURL` on display rather than relying on the URL to change when the member
replaces their image.
Local mode returns `nil` for every member, except the `image_url` of a users
file entry.

### Go-specific helpers

| Method | Description |
|--------|-------------|
| `IsLocal() bool` | Whether client is in local mode |

### Environment variables

| Variable | Description |
|----------|-------------|
| `KEELSON_DIRECTORY_BASE_URL` | **Canonical, platform-injected** base URL for Directory calls. Use this |
| `KEELSON_DIRECTORY_TOKEN` | App token for app-as-actor Directory access; used when no `WithAppToken` / `WithAuthorization` / `WithCookie` is given |
| `KEELSON_IDENTITY_BASE_URL` | **Deprecated** compatibility fallback for the base URL; new code should use `KEELSON_DIRECTORY_BASE_URL` |
| `KEELSON_LOCAL_MODE` | Set to `1`, `true`, or `yes` to use fixture data (local development only; refused in a Keelson deployment) |
| `KEELSON_LOCAL_USERS_FILE` | Same users file as Identity |
| `KEELSON_LOCAL_USER_*` / `KEELSON_LOCAL_WORKSPACE_*` / `KEELSON_LOCAL_APP_*` | Same overrides as Identity |

---

## Media

Upload immutable media (images, PDFs, generated assets) and serve it by ID. On
Keelson it uses the managed media service; for local
development it uses the local filesystem. The runtime-mode contract is
**fail-closed**: it never silently writes to ephemeral local storage when platform
Media configuration is missing or incomplete (see Modes below). Misconfiguration
returns `ErrConfig` from `New`.

```go
import "github.com/keelsonhq/go-sdk/media"

client, err := media.New("", "") // reads env vars
if err != nil { /* ... */ }

// Upload (high-level — generates a ULID file ID, matches Node/Python)
fileID, err := client.Upload(strings.NewReader("hello"),
    media.WithFilename("readme.txt"),
)
fmt.Println(fileID) // e.g. "01JRZX5G7H0000ABCDEFGHJKMN"

// Download
content, err := client.Get(fileID)
defer content.Close()
data, _ := io.ReadAll(content.Body)

// Metadata
meta, err := client.Head(fileID)
fmt.Println(meta.ContentType, meta.ContentLength)

// Public URL path
url := client.URL(fileID) // "/media/01JRZX5G7H..."

// Check existence
exists, err := client.Exists(fileID)

// Delete (idempotent)
err = client.Delete(fileID)
```

### Cross-language guaranteed API

| Method | Description |
|--------|-------------|
| `New(baseURL, token) (*Client, error)` | Create client (falls back to env vars) |
| `Upload(body, opts...) (string, error)` | Upload file with auto-generated ULID (matches Node/Python `put`) |
| `Get(fileID) (*MediaContent, error)` | Download file content |
| `Delete(fileID) error` | Delete file (no-op if not found) |
| `Exists(fileID) (bool, error)` | Check if file exists |
| `Head(fileID) (*MediaMeta, error)` | Get metadata (content type, length) |
| `URL(fileID) string` | Generate public URL path |

### Go-specific helpers

| Method | Description |
|--------|-------------|
| `Put(fileID, body, opts...) error` | Upload file with caller-supplied ID (optional low-level API) |
| `IsLocal() bool` | Whether client is in local filesystem mode |

### Upload / Put options

| Option | Description |
|--------|-------------|
| `WithContentType(ct)` | Set Content-Type header (Upload defaults to `application/octet-stream`) |
| `WithContentLength(n)` | Set Content-Length header |
| `WithFilename(name)` | Set X-Keelson-Filename header; Upload auto-detects content type from extension |

### Modes (fail-closed runtime-mode contract)

| `KEELSON_MODE` | Condition | Behaviour |
|------|-----------|-----------|
| `keelson` | Both Media env set | Remote (Keelson media service) |
| `keelson` | Media env missing | **`ErrConfig`** — Media capability unavailable; never local |
| any | Exactly one of base URL / token set | **`ErrConfig`** — incomplete remote config |
| `local` | — | Local filesystem (`MEDIA_DIR`, default `./media`) |
| unset | Both Media env set | Remote (backward compatibility) |
| unset | No Media env, platform core env visible (`KEELSON_APP_ID` / `KEELSON_WORKSPACE_ID` / `KEELSON_DEPLOY_ID`) | **`ErrConfig`** — refuses silent local fallback |
| unset | No Media env, no platform env | Local filesystem (local development) |

The SDK never silently falls back to ephemeral local storage on Keelson: set
`KEELSON_MODE=local` explicitly for local development.

### Environment variables

| Variable | Description |
|----------|-------------|
| `KEELSON_MODE` | `keelson` (remote, fail-closed) / `local` (local FS) / unset (local development). Platform injects `keelson` |
| `KEELSON_INTERNAL_MEDIA_BASE_URL` | Platform-injected internal media endpoint (Keelson mode; required with the token) |
| `KEELSON_APP_MEDIA_TOKEN` | App-scoped media token validated by the platform (Keelson mode) |
| `KEELSON_MEDIA_URL_PREFIX` | Public URL prefix used by `URL()` (default: `/media/`) |
| `MEDIA_DIR` | Local storage directory (default: `./media`); used whenever the SDK resolves to local mode — either explicit `KEELSON_MODE=local`, or zero-config local development (`KEELSON_MODE` unset with no Media env and no platform core env) |

---

## Files (data)

Durable file storage for your app's own files — state, settings, caches. Reads
and writes are always whole-file, and `Write` is write-through: once it returns,
the data is persisted. There is no background sync and nothing is stored on
ephemeral local disk. Overwriting an existing key is the normal case. For
user-uploaded or generated media referenced by ID and served over HTTP, use the
`media` package; for data read/written on every request, use the database.

```go
import "github.com/keelsonhq/go-sdk/files"

client, _ := files.New()
_ = client.Write("seen_urls.json", data)
got, ok, _ := client.Read("seen_urls.json") // ok=false when absent
keys, _ := client.List("")                  // sorted []string
_ = client.Delete("seen_urls.json")         // idempotent
```

### Cross-language guaranteed API

| Function | Description |
|----------|-------------|
| `New()` | Construct a client; errors wrap `files.ErrConfig` on misconfiguration |
| `Write(key, data)` | Overwrite `key` with `[]byte`; write-through |
| `Read(key)` | `(data, ok, err)`; `ok=false` when the key is absent (only a 404 is missing) |
| `Delete(key)` | Idempotent delete |
| `List(prefix)` | Full, lexicographically-sorted key list; paging absorbed |

Key grammar: `/`-separated relative path, well-formed UTF-8 ≤ 512 bytes total and
≤ 255 bytes per segment, no leading/trailing `/`, no empty / `.` / `..` segments,
no control characters. One-object soft limit 10 MiB. There is no `Exists` — the
`ok=false` return from `Read` covers it.

### Environment variables

| Variable | Description |
|----------|-------------|
| `KEELSON_MODE` | `keelson` (remote) or `local`; the single mode signal |
| `KEELSON_FILES_BUCKET` / `KEELSON_FILES_PREFIX` | Platform-injected in `keelson` mode (managed object storage) |
| `KEELSON_FILES_DIR` | Local-mode directory (default `./.keelson/files`) |

Fail-closed: `KEELSON_MODE=keelson` requires bucket + prefix + platform identity;
missing config returns an error wrapping `files.ErrConfig`. See the
[cross-SDK parity contract shipped in this repo](./PARITY.md) for the full contract.

---

## Tasks

Enqueue a run of a command declared under `tasks:` in `keelson.yaml`, and
read its state. On Keelson the platform runs the command once on a separate
instance, passes the payload as one JSON line on stdin, and retries failed
attempts (at-least-once: make the command safe to run twice). The SDK does not
receive tasks; the command is an ordinary program that reads stdin.

```yaml
# keelson.yaml
tasks:
  - name: generate-pdf
    command: python make_pdf.py
    timeout: 300      # seconds; optional
```

```go
import "github.com/keelsonhq/go-sdk/tasks"

client, err := tasks.New() // errors.Is(err, tasks.ErrConfig) on misconfiguration
if err != nil {
	return err
}
taskID, err := client.Enqueue(ctx, "generate-pdf", map[string]int{"order_id": 1},
	tasks.WithIdempotencyKey("order-1-pdf"))
if err != nil {
	var te *tasks.Error
	if errors.As(err, &te) && te.Code == "TASK_NOT_DECLARED" {
		// ...
	}
	return err
}
status, err := client.Get(ctx, taskID)
```

### Cross-language guaranteed API

| Function | Description |
|----------|-------------|
| `tasks.New()` | Construct a client; resolves the mode once. Errors wrap `tasks.ErrConfig` on misconfiguration |
| `(*Client).Enqueue(ctx, name, payload, ...EnqueueOption)` | Enqueue one run; returns the `task_id`. `payload` is any value `json.Marshal` accepts (`nil` is JSON `null`; pass a `json.RawMessage` for pre-encoded JSON) |
| `tasks.WithIdempotencyKey(key)` | Idempotency key option (1–128 printable ASCII characters) |
| `(*Client).Get(ctx, taskID)` | `*TaskStatus`: `TaskID`, `Name`, `Status` (`queued` / `running` / `succeeded` / `failed` / `cancelled`), `ClaimedAttempts`, `LastFailureCode` (`*string`), `CreatedAt` (`time.Time`), `FinishedAt` (`*time.Time`) |
| `*tasks.Error` | The single error type (`Code`, `Status`, `Message`) |

A repeat with the same idempotency key returns the existing `task_id` instead
of enqueueing again. In local mode, cancelling `ctx` sends `SIGTERM` to the CLI
(which forwards it to the command), force-kills it after 130 s, and `Enqueue`
returns `ctx.Err()` itself, so `errors.Is(err, context.Canceled)` works.

### Errors

Every failure is one `*tasks.Error` with `code`, `status` (the HTTP status, or
`0` when there was no HTTP response), and `message`. Branch on `code`:

| `code` | Meaning |
|--------|---------|
| `TASK_NOT_DECLARED` | The name is not under `tasks:` in the deployed (or local) `keelson.yaml` |
| `TASK_INVALID_REQUEST` | Empty name, malformed idempotency key, or a payload that is not JSON-serializable |
| `TASK_PAYLOAD_TOO_LARGE` | The request body is over 65,536 bytes (checked before sending) |
| `TASK_NOT_FOUND` | `get` of an unknown task ID (local mode: not enqueued in this process) |
| `TASK_BACKLOG_LIMIT_EXCEEDED` / `TASK_MONTHLY_QUOTA_EXCEEDED` | Plan limits; not retried by the SDK |
| `TASKS_UNAVAILABLE` | Intake is closed on the platform. Retrying does not help |
| `TASKS_UNAVAILABLE_TRANSIENT` | A passing outage (502/503/504, connection failure, 15 s timeout), after the SDK's own retries |
| `TASKS_FORBIDDEN` | 403 from Cloud Run. Right after the first deploy that declares `tasks:`, the permission can take a few minutes to propagate |
| `TASKS_UNAUTHORIZED` / `TASKS_IDENTITY_TOKEN_ERROR` | The id token was rejected / could not be fetched from the metadata server |
| `TASKS_SERVER_ERROR` / `TASKS_HTTP_ERROR` / `TASKS_UNEXPECTED_RESPONSE` | Other unexpected responses |
| `TASKS_NOT_CONFIGURED` | Mode resolution failed (below) — wraps `tasks.ErrConfig` |
| `TASKS_LOCAL_CLI_NOT_FOUND` / `TASKS_LOCAL_CLI_FAILED` | Local mode: no `keelson` on `PATH` (install: `https://keelson.dev/install.sh`) / the CLI failed (try `keelson upgrade`) |

Retries: only `TASKS_UNAVAILABLE_TRANSIENT` is retried (3 attempts in total,
waiting 0.5 s then 1 s). `get` always retries; enqueue retries **only with an
idempotency key**, because without one a request the server already accepted
would be enqueued twice. The payload never appears in an error message.

### Modes (fail-closed runtime-mode contract)

| Condition | Result |
|-----------|--------|
| `KEELSON_MODE=keelson` + `KEELSON_TASKS_BASE_URL` set | Keelson (runtime API); `KEELSON_APP_ID` is also required |
| `KEELSON_MODE=keelson` + `KEELSON_TASKS_BASE_URL` missing | `TASKS_NOT_CONFIGURED` (declare `tasks:` in `keelson.yaml` and deploy) |
| `KEELSON_MODE=local` | Local (runs the command through the CLI) |
| `KEELSON_MODE` unset + a platform variable (`KEELSON_APP_ID` / `KEELSON_WORKSPACE_ID` / `KEELSON_DEPLOY_ID`) | `TASKS_NOT_CONFIGURED` (never falls back to local on Keelson) |
| `KEELSON_MODE` unset + none of those | Local (zero-config development) |
| Any other `KEELSON_MODE` value | `TASKS_NOT_CONFIGURED` |

### Environment variables

| Variable | Description |
|----------|-------------|
| `KEELSON_MODE` | `keelson` (remote) or `local`; the single mode signal |
| `KEELSON_TASKS_BASE_URL` | Platform-injected runtime API URL when the app declares `tasks:`; also the id-token audience |
| `KEELSON_APP_ID` | Platform-injected app ID; the `/internal/apps/{app_id}/...` path segment |

There is no token variable: the id token comes from the Cloud Run metadata
server on every call.

### Local mode

In local mode, enqueue runs `keelson dev task run <name> --payload - --json`
(the `keelson` CLI on `PATH`) from the app's working directory, so start your
dev server in the directory that has `keelson.yaml`. The command receives the
same stdin document as on Keelson, its output goes to your app's stderr, and
enqueue returns **after the command has finished** — a request handler that
enqueues waits for it. A command that exits non-zero or times out is not an
enqueue error: `get` reports `status` `failed` with `last_failure_code`
`exit_nonzero` or `timed_out`.

Differences from Keelson:

- synchronous: the command runs before enqueue returns, in the same machine
- no retry: one attempt only
- no concurrency, backlog, or monthly-quota limits
- the declared `timeout` applies as written (on Keelson it is capped by your
  plan's limit)
- `get` knows only tasks enqueued in the same process; others are
  `TASK_NOT_FOUND`, and a running task is never visible
- the same name + idempotency key returns the existing task ID without running
  again, but two concurrent calls with the same key both run the command

See the [cross-SDK parity contract shipped in this repo](./PARITY.md) for the full contract.

---

## Development

```bash
# Run all tests
go test ./...

# Run tests for a single package
go test ./media/
go test ./identity/
go test ./directory/
go test ./tasks/

# Build check (verify compilation)
go build ./...

# Vet
go vet ./...
```
