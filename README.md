# Keelson Go SDK

Go SDK for building apps on the Keelson platform. Provides five packages:

> **Note**: This repository is a read-only release mirror. Development happens in the private Keelson monorepo; issues are welcome here, but pull requests are not accepted — changes land through the next release.

| Package | Import path | Description |
|---------|-------------|-------------|
| `identity` | `github.com/keelsonhq/go-sdk/identity` | Authenticated user identity |
| `directory` | `github.com/keelsonhq/go-sdk/directory` | Tenant member and group directory |
| `media` | `github.com/keelsonhq/go-sdk/media` | Media storage (upload, serve by ID) |
| `files` | `github.com/keelsonhq/go-sdk/files` | Data files (key-addressed, overwrite, private) |
| `email` | `github.com/keelsonhq/go-sdk/email` | Outbound email and inbound webhooks |

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
Use `GetCurrentUser` when the basic user profile is enough; use
`GetCurrentIdentity` when the app needs tenant role, app permissions, app
roles, or group attributes.

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

current, err := client.GetCurrentIdentity(
    identity.WithHeaders(r.Header),
    identity.WithAppToken(os.Getenv("KEELSON_DIRECTORY_TOKEN")),
    identity.WithHost(r.Host),
)
if err != nil { /* ... */ }
fmt.Println(current.Tenant.Role)
fmt.Println(current.App.Permissions) // ["manage", "view"]
```

### Cross-language guaranteed API

| Method | Description |
|--------|-------------|
| `New(baseURL) (*Client, error)` | Create client (falls back to `KEELSON_DIRECTORY_BASE_URL`, then the deprecated `KEELSON_IDENTITY_BASE_URL`) |
| `GetCurrentUser(opts...) (*UserIdentity, error)` | Parse the current user's basic profile from trusted `X-Keelson-User-*` headers; no network call |
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

### Migration note

`GetCurrentUser` no longer forwards browser cookies to `/__keelson/user` and no
longer returns tenant/app authorization fields. Replace old
`GetCurrentUser(WithCookie(...), WithHost(...))` calls with
`GetCurrentUser(WithHeaders(r.Header))` for basic user fields, or
`GetCurrentIdentity(WithHeaders(r.Header), WithAppToken(...), WithHost(r.Host))`
when you need `Tenant.Role`, `App.Permissions`, `App.Roles`, or
`Attributes.Groups`.

### Modes

| Mode | Condition | Behaviour |
|------|-----------|-----------|
| Local | `KEELSON_LOCAL_MODE` set | Returns deterministic fixture data (no HTTP calls) |
| Keelson | Default | Calls the Keelson auth gateway via `KEELSON_DIRECTORY_BASE_URL` (canonical, platform-injected). `KEELSON_IDENTITY_BASE_URL` is a **deprecated** fallback only |

### Environment variables

| Variable | Description |
|----------|-------------|
| `KEELSON_DIRECTORY_BASE_URL` | **Canonical, platform-injected** base URL for `GetCurrentIdentity` and Directory calls. Use this |
| `KEELSON_DIRECTORY_TOKEN` | App token for app-as-actor current identity and Directory access |
| `KEELSON_IDENTITY_BASE_URL` | **Deprecated** compatibility fallback for the base URL, used only when `KEELSON_DIRECTORY_BASE_URL` and an explicit `baseURL` are both absent |
| `KEELSON_LOCAL_MODE` | Set to `1`, `true`, or `yes` to use fixture data |
| `KEELSON_LOCAL_USER_ID` | Override local user ID (default: `local-user-001`) |
| `KEELSON_LOCAL_USER_EMAIL` | Override local user email (default: `dev@localhost`) |
| `KEELSON_LOCAL_USER_NAME` | Override local user name (default: `Local Developer`) |
| `KEELSON_LOCAL_TENANT_ID` | Override local tenant ID (default: `local-tenant-001`) |
| `KEELSON_LOCAL_TENANT_ROLE` | Override local tenant role (default: `OWNER`) |
| `KEELSON_LOCAL_APP_ID` | Override local app ID (default: `local-app-001`) |

`KEELSON_IDENTITY_BASE_URL` is a deprecated alias, but it still works as a
fallback; new code should use `KEELSON_DIRECTORY_BASE_URL`.

---

## Directory

Lookup tenant members and groups. Same forwarding pattern as Identity.
Supports `KEELSON_LOCAL_MODE` with the same fixture data and env overrides.

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
| `ListMembers(params, opts...) (*PaginatedMembers, error)` | List tenant members (paginated, filterable) |
| `GetUser(userID, opts...) (*MemberItem, error)` | Get user by ID |
| `ListGroups(opts...) ([]GroupItem, error)` | List tenant groups |
| `WithAppToken(token) RequestOption` | App-as-actor Directory access via `Authorization: Bearer <token>` |

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
| `KEELSON_LOCAL_MODE` | Set to `1`, `true`, or `yes` to use fixture data |
| `KEELSON_LOCAL_USER_*` / `KEELSON_LOCAL_TENANT_*` / `KEELSON_LOCAL_APP_*` | Same overrides as Identity |

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
| unset | No Media env, platform core env visible (`KEELSON_APP_ID` / `KEELSON_TENANT_ID` / `KEELSON_DEPLOY_ID`) | **`ErrConfig`** — refuses silent local fallback |
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

## Email

Send emails and receive inbound email via webhooks.

```go
import "github.com/keelsonhq/go-sdk/email"

client, err := email.New("", "") // reads env vars
if err != nil { /* ... */ }

// Send
textBody := "Plain text body"
htmlBody := "<p>HTML body</p>"
resp, err := client.Send(&email.SendRequest{
    To:      []string{"user@example.com"},
    Subject: "Hello",
    Text:    &textBody,
    HTML:    &htmlBody,
})
fmt.Println(resp.SendID, resp.Status)

// Verify inbound webhook (in HTTP handler)
msg, err := email.VerifyWebhook(r, webhookSecret)
fmt.Println(msg.Subject, msg.From.Address)

// Download attachment
att, err := client.DownloadAttachment(msg.Attachments[0].ID)
defer att.Body.Close()

// Verify event webhook (bounce/complaint/delivery)
event, err := email.VerifyEventWebhook(r, webhookSecret)
if event.EventType == "bounce" {
    fmt.Println("Bounced:", event.EmailAddress)
}
```

### Cross-language guaranteed API

| Method | Description |
|--------|-------------|
| `New(baseURL, token) (*Client, error)` | Create client (falls back to env vars) |
| `Send(req) (*SendResponse, error)` | Send an email |
| `DownloadAttachment(id) (*AttachmentContent, error)` | Download attachment by ID |
| `email.VerifyWebhook(r, secret) (*InboundEmail, error)` | Verify Svix signature and parse inbound email |
| `email.VerifyWebhookBytes(body, headers, secret) (*InboundEmail, error)` | Verify inbound email from pre-read body |
| `email.VerifyEventWebhook(r, secret) (*EmailEventPayload, error)` | Verify Svix signature and parse event |
| `email.VerifyEventWebhookBytes(body, headers, secret) (*EmailEventPayload, error)` | Verify event from pre-read body |

### Go-specific helpers

| Method | Description |
|--------|-------------|
| `SendCtx(ctx, req) (*SendResponse, error)` | Send with context |
| `DownloadAttachmentCtx(ctx, id) (*AttachmentContent, error)` | Download with context |

### Environment variables

| Variable | Description |
|----------|-------------|
| `KEELSON_EMAIL_API_URL` | Email API endpoint (required) |
| `KEELSON_EMAIL_TOKEN` | Bearer token (required) |

---

## Development

```bash
# Run all tests
go test ./...

# Run tests for a single package
go test ./media/
go test ./identity/
go test ./directory/
go test ./email/

# Build check (verify compilation)
go build ./...

# Vet
go vet ./...
```
