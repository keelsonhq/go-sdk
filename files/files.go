// Package files provides a client for the Keelson data-files interface:
// key-addressed, overwrite, whole-value durable file storage for an app's own
// files (state, settings, caches).
//
// It is intentionally distinct from the media package: media is create-only,
// ULID-addressed, immutable and served over HTTP; files is key-addressed,
// overwrite-in-place, and read only by the app itself.
//
// API (parity with the Python keelson_files and Node @keelsonhq/files SDKs):
//
//   - Write(key, data)  — overwrite; write-through.
//   - Read(key)         — (data, ok, err); ok=false when the key is absent.
//   - Delete(key)       — idempotent (no error when missing).
//   - List(prefix)      — lexicographically-sorted list of all keys.
//
// The storage mode is resolved from KEELSON_MODE plus the platform-injected
// data-files env (KEELSON_FILES_BUCKET / KEELSON_FILES_PREFIX). New is
// fail-closed: KEELSON_MODE=keelson without both env values, or a partial
// config, returns an error wrapping ErrConfig rather than silently using
// ephemeral local storage. On Keelson the per-app service account writes
// through to GCS over ADC (the GCE metadata server), so no auth env is wired.
package files

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// MaxObjectSizeBytes is the one-object soft size limit (10 MiB). Enforced on
// the SDK side of Write; per-app total / object-count quota is soft and metered
// server-side.
const MaxObjectSizeBytes = 10 * 1024 * 1024

const (
	sdkUserAgent = "Keelson-Go-SDK/0.2.1"
	requestTO    = 30 * time.Second

	defaultStorageBase = "https://storage.googleapis.com"
	defaultMetadataURL = "http://metadata.google.internal/computeMetadata/v1/" +
		"instance/service-accounts/default/token"
)

// ErrConfig is returned by New when the client cannot be constructed because
// the platform data-files configuration is missing or inconsistent. Use
// errors.Is(err, files.ErrConfig) to detect it.
var ErrConfig = errors.New("files: configuration error")

type configError struct{ msg string }

func (e *configError) Error() string { return e.msg }
func (e *configError) Unwrap() error { return ErrConfig }

func newConfigError(format string, args ...any) error {
	return &configError{msg: fmt.Sprintf(format, args...)}
}

var coreIdentifierEnvs = []string{
	"KEELSON_APP_ID",
	"KEELSON_WORKSPACE_ID",
	"KEELSON_TENANT_ID",
	"KEELSON_DEPLOY_ID",
}

func platformEnvVisible() bool {
	for _, name := range coreIdentifierEnvs {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

// Client provides access to the Keelson data-files store. In Keelson mode it
// talks to GCS over ADC; in local mode it reads/writes the local filesystem.
type Client struct {
	local bool

	// local mode
	dir string

	// remote (GCS) mode
	bucket      string
	prefix      string // trailing slash
	storageBase string
	metadataURL string
	hc          *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// New constructs a data-files client from the environment. See the package
// documentation for the fail-closed runtime-mode contract.
func New() (*Client, error) {
	bucket := strings.TrimSpace(os.Getenv("KEELSON_FILES_BUCKET"))
	prefix := filesPrefixEnv()
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("KEELSON_MODE")))
	hasBucket := bucket != ""
	hasPrefix := prefix != ""

	if hasBucket != hasPrefix {
		missing := "KEELSON_FILES_PREFIX"
		if !hasBucket {
			missing = "KEELSON_FILES_BUCKET"
		}
		return nil, newConfigError(
			"files: incomplete remote configuration: both KEELSON_FILES_BUCKET "+
				"and KEELSON_FILES_PREFIX are required, but %s is missing",
			missing)
	}

	// KEELSON_MODE is the only backend-selection signal; the presence of remote
	// configuration alone never switches storage modes.
	switch mode {
	case "keelson":
		if !(hasBucket && hasPrefix) {
			return nil, newConfigError(
				"files: KEELSON_MODE=keelson but Files is not configured " +
					"(KEELSON_FILES_BUCKET and KEELSON_FILES_PREFIX are unset); " +
					"the Files capability is unavailable for this deployment")
		}
		if !hasIdentity() {
			return nil, newConfigError(
				"files: KEELSON_MODE=keelson but the platform identity is missing " +
					"(KEELSON_APP_ID and KEELSON_WORKSPACE_ID must be set; " +
					"KEELSON_TENANT_ID remains a deprecated alias); " +
					"the Files capability is unavailable for this deployment")
		}
		return newRemoteClient(bucket, prefix), nil
	case "local":
		return newLocalClient(), nil
	case "":
		if platformEnvVisible() {
			return nil, newConfigError(
				"files: platform environment detected " +
					"(KEELSON_APP_ID / KEELSON_WORKSPACE_ID (or deprecated " +
					"KEELSON_TENANT_ID alias) / KEELSON_DEPLOY_ID set) but " +
					"KEELSON_MODE is unset; refusing to fall back to local storage. " +
					"Set KEELSON_MODE=local for local development or KEELSON_MODE=keelson " +
					"for platform storage")
		}
		return newLocalClient(), nil
	default:
		return nil, newConfigError(
			"files: unrecognized KEELSON_MODE=%q; expected \"keelson\" or \"local\" "+
				"(or unset for local development)",
			mode)
	}
}

// hasIdentity reports whether the workspace + app identity that composes the
// prefix is present in the environment.
func hasIdentity() bool {
	return strings.TrimSpace(os.Getenv("KEELSON_APP_ID")) != "" &&
		workspaceID() != ""
}

func workspaceID() string {
	if id := strings.TrimSpace(os.Getenv("KEELSON_WORKSPACE_ID")); id != "" {
		return id
	}
	return strings.TrimSpace(os.Getenv("KEELSON_TENANT_ID"))
}

func newLocalClient() *Client {
	dir := strings.TrimSpace(os.Getenv("KEELSON_FILES_DIR"))
	if dir == "" {
		dir = "./.keelson/files"
	}
	return &Client{local: true, dir: dir}
}

func newRemoteClient(bucket, prefix string) *Client {
	storageBase := strings.TrimSpace(os.Getenv("KEELSON_FILES_STORAGE_BASE"))
	if storageBase == "" {
		storageBase = defaultStorageBase
	}
	metadataURL := strings.TrimSpace(os.Getenv("KEELSON_FILES_METADATA_URL"))
	if metadataURL == "" {
		metadataURL = defaultMetadataURL
	}
	return &Client{
		bucket:      bucket,
		prefix:      prefix,
		storageBase: strings.TrimRight(storageBase, "/"),
		metadataURL: metadataURL,
		hc:          &http.Client{Timeout: requestTO},
	}
}

// IsLocal reports whether the client is operating in local filesystem mode.
func (c *Client) IsLocal() bool { return c.local }

func filesPrefixEnv() string {
	prefix := strings.TrimSpace(os.Getenv("KEELSON_FILES_PREFIX"))
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return prefix
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Write stores data at key, overwriting any existing value. Write-through:
// once it returns nil, the data is persisted.
func (c *Client) Write(key string, data []byte) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if len(data) > MaxObjectSizeBytes {
		return fmt.Errorf("files.Write: object is %d bytes, exceeding the %d-byte limit",
			len(data), MaxObjectSizeBytes)
	}
	if c.local {
		return c.writeLocal(key, data)
	}
	return c.writeRemote(key, data)
}

// Read returns the bytes stored at key. ok is false when the key does not
// exist (only a 404 / missing file; 401 / 403 / 429 / 5xx return an error).
func (c *Client) Read(key string) (data []byte, ok bool, err error) {
	if err := validateKey(key); err != nil {
		return nil, false, err
	}
	if c.local {
		return c.readLocal(key)
	}
	return c.readRemote(key)
}

// Delete removes key. Idempotent: no error when the key does not exist.
func (c *Client) Delete(key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if c.local {
		return c.deleteLocal(key)
	}
	return c.deleteRemote(key)
}

// List returns every key (optionally under prefix) in lexicographic order.
// Paging is absorbed internally.
func (c *Client) List(prefix string) ([]string, error) {
	if err := validatePrefix(prefix); err != nil {
		return nil, err
	}
	if c.local {
		return c.listLocal(prefix)
	}
	return c.listRemote(prefix)
}

// ---------------------------------------------------------------------------
// Key and prefix validation rules
// ---------------------------------------------------------------------------

// segmentMaxBytes is NAME_MAX (255 bytes on ext4 / APFS / most POSIX
// filesystems); the literal-file layout maps each key segment to a filename.
const segmentMaxBytes = 255

func hasControlChar(s string) bool {
	for _, r := range s {
		// Unicode control chars (category Cc): C0, DEL, and C1 (U+0080–U+009F).
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return true
		}
	}
	return false
}

func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("files: key is required")
	}
	// Reject ill-formed UTF-8 (invalid byte sequences): they would round-trip
	// inconsistently across the local FS and the GCS backend. Typed error,
	// matching Node's well-formed check and Python's encode() guard.
	if !utf8.ValidString(key) {
		return fmt.Errorf("files: key must be well-formed UTF-8")
	}
	if len(key) > 512 {
		return fmt.Errorf("files: key must be at most 512 UTF-8 bytes")
	}
	if strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") {
		return fmt.Errorf("files: key must not start or end with '/'")
	}
	if hasControlChar(key) {
		return fmt.Errorf("files: key must not contain control characters")
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" {
			return fmt.Errorf("files: key must not contain empty segments")
		}
		if seg == "." || seg == ".." {
			return fmt.Errorf("files: key must not contain '.' or '..' segments")
		}
		if len(seg) > segmentMaxBytes {
			return fmt.Errorf("files: each key segment must be at most %d UTF-8 bytes",
				segmentMaxBytes)
		}
	}
	return nil
}

func validatePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if !utf8.ValidString(prefix) {
		return fmt.Errorf("files: prefix must be well-formed UTF-8")
	}
	if len(prefix) > 512 {
		return fmt.Errorf("files: prefix must be at most 512 UTF-8 bytes")
	}
	if strings.HasPrefix(prefix, "/") {
		return fmt.Errorf("files: prefix must not start with '/'")
	}
	if hasControlChar(prefix) {
		return fmt.Errorf("files: prefix must not contain control characters")
	}
	for _, seg := range strings.Split(prefix, "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("files: prefix must not contain '.' or '..' segments")
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Local filesystem backend
// ---------------------------------------------------------------------------
//
// Storage layout: each key maps to a literal file at
// <KEELSON_FILES_DIR>/<key> — the key is the real file path, for visual
// debuggability. Nested keys create parent directories.
//
// A key and a nested key that shadows it (both `cache` and `cache/item`) cannot
// coexist on a filesystem; that is an inherent limitation of the required
// literal layout and is surfaced as an explicit error on Write, while
// Read/Delete of a key shadowed by a directory are treated as missing /
// idempotent (matching the GCS 404).
//
// Confinement is TOCTOU-safe: all filesystem operations go through os.Root,
// which resolves every path component relative to a held directory descriptor
// and refuses any name that references a location outside the root — even if an
// ancestor directory is swapped to a symlink concurrently, mid-operation. Temp
// files use a control-char prefix (never a valid key segment) in the target's
// own directory, so the atomic rename is always same-filesystem and List never
// surfaces them.

const tmpPrefix = "\x01tmp"

// openRoot returns an os.Root confined to the files dir. When create is false
// and the dir does not exist it returns an fs.ErrNotExist error.
func (c *Client) openRoot(create bool) (*os.Root, error) {
	base, err := filepath.Abs(c.dir)
	if err != nil {
		return nil, fmt.Errorf("files: resolve dir: %w", err)
	}
	if create {
		if err := os.MkdirAll(base, 0o755); err != nil {
			return nil, fmt.Errorf("files: create dir: %w", err)
		}
	}
	return os.OpenRoot(base)
}

func randTempName() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return tmpPrefix + hex.EncodeToString(b[:]), nil
}

func collisionError(key string) error {
	return fmt.Errorf(
		"files.Write: cannot write key %q in local mode: it collides with a "+
			"nested key on the filesystem (the object store allows both a key and "+
			"keys under it, but the literal local file layout cannot "+
			"represent both)", key)
}

func (c *Client) writeLocal(key string, data []byte) error {
	root, err := c.openRoot(true)
	if err != nil {
		return fmt.Errorf("files.Write: %w", err)
	}
	defer root.Close()
	name := filepath.FromSlash(key)
	if info, err := root.Stat(name); err == nil && info.IsDir() {
		return collisionError(key) // a nested key occupies this path as a dir
	}
	if dir := filepath.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			// A parent key occupies a segment as a file (EEXIST for a non-dir at
			// a segment, ENOTDIR when traversing through one).
			if errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.EEXIST) {
				return collisionError(key)
			}
			return fmt.Errorf("files.Write: create dir: %w", err)
		}
	}
	// temp + atomic rename, in the target's own directory (same filesystem).
	tmpBase, err := randTempName()
	if err != nil {
		return fmt.Errorf("files.Write: temp name: %w", err)
	}
	tmpName := filepath.Join(filepath.Dir(name), tmpBase)
	f, err := root.OpenFile(tmpName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("files.Write: create temp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		root.Remove(tmpName)
		return fmt.Errorf("files.Write: write temp: %w", err)
	}
	if err := f.Close(); err != nil {
		root.Remove(tmpName)
		return fmt.Errorf("files.Write: close temp: %w", err)
	}
	if err := root.Rename(tmpName, name); err != nil {
		root.Remove(tmpName)
		if errors.Is(err, syscall.EISDIR) || errors.Is(err, syscall.ENOTEMPTY) {
			return collisionError(key)
		}
		return fmt.Errorf("files.Write: rename: %w", err)
	}
	return nil
}

func (c *Client) readLocal(key string) ([]byte, bool, error) {
	root, err := c.openRoot(false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil // files dir does not exist yet
		}
		return nil, false, fmt.Errorf("files.Read: %w", err)
	}
	defer root.Close()
	data, err := root.ReadFile(filepath.FromSlash(key))
	if err != nil {
		// Absent, a non-dir path component, or a directory at the key path →
		// missing (GCS 404). A symlink escaping the root surfaces as an error
		// (os.Root refuses it) — never leaked as content.
		if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) ||
			errors.Is(err, syscall.EISDIR) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("files.Read: %w", err)
	}
	return data, true, nil
}

func (c *Client) deleteLocal(key string) error {
	root, err := c.openRoot(false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // files dir does not exist → nothing to delete
		}
		return fmt.Errorf("files.Delete: %w", err)
	}
	defer root.Close()
	if err := root.Remove(filepath.FromSlash(key)); err != nil {
		// Absent, or shadowed by a directory → idempotent no-op.
		if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) ||
			errors.Is(err, syscall.EISDIR) || errors.Is(err, syscall.EPERM) ||
			errors.Is(err, syscall.ENOTEMPTY) {
			return nil
		}
		return fmt.Errorf("files.Delete: %w", err)
	}
	return nil
}

func (c *Client) listLocal(prefix string) ([]string, error) {
	root, err := c.openRoot(false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("files.List: %w", err)
	}
	defer root.Close()
	var keys []string
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." || d.IsDir() {
			return nil
		}
		// WalkDir does not descend into symlinked directories; never list a
		// symlink as a key. Only real regular files are keys.
		if !d.Type().IsRegular() {
			return nil
		}
		// Temp files use a control-char prefix that no valid key can have.
		if strings.HasPrefix(pathBase(path), tmpPrefix) {
			return nil
		}
		if strings.HasPrefix(path, prefix) {
			keys = append(keys, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("files.List: %w", err)
	}
	// sort.Strings is byte order == UTF-8 order == the shared cross-language
	// ordering (Python code-point order and Node's compareUtf8 agree).
	sort.Strings(keys)
	return keys, nil
}

// pathBase returns the last "/"-separated element of an fs.FS path.
func pathBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// ---------------------------------------------------------------------------
// GCS (remote) backend — ADC over the GCE metadata server
// ---------------------------------------------------------------------------

func (c *Client) accessToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp) {
		return c.token, nil
	}
	req, err := http.NewRequest(http.MethodGet, c.metadataURL, nil)
	if err != nil {
		return "", fmt.Errorf("files: build metadata request: %w", err)
	}
	req.Header.Set("Metadata-Flavor", "Google")
	req.Header.Set("User-Agent", sdkUserAgent)
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("files: obtain ADC access token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("files: metadata token request failed with %d", resp.StatusCode)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("files: decode metadata token: %w", err)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("files: metadata server returned no access_token")
	}
	c.token = payload.AccessToken
	margin := payload.ExpiresIn - 60
	if margin < 0 {
		margin = 0
	}
	c.tokenExp = time.Now().Add(time.Duration(margin) * time.Second)
	return c.token, nil
}

func (c *Client) objectName(key string) string { return c.prefix + key }

// gcsDo performs an authenticated GCS request. When allowNotFound is set a 404
// returns (404, nil, nil); every other non-2xx status returns an error (only a
// 404 is "missing" — 401 / 403 / 429 / 5xx are real failures).
func (c *Client) gcsDo(method, rawURL string, body io.Reader, contentType string, allowNotFound bool) (int, []byte, error) {
	token, err := c.accessToken()
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return 0, nil, fmt.Errorf("files: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", sdkUserAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s failed: %w", method, rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && allowNotFound {
		return resp.StatusCode, nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, nil, fmt.Errorf("%s %s failed with %d: %s",
			method, rawURL, resp.StatusCode, string(detail))
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("%s %s: read body: %w", method, rawURL, err)
	}
	return resp.StatusCode, data, nil
}

// encodeQuery percent-encodes a query value, encoding space as %20 (not "+")
// so the wire form matches Python (urllib.quote) and Node (encodeURIComponent).
// All three decode to the same object name; keeping %20 avoids any ambiguity.
func encodeQuery(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func (c *Client) objectURL(key string) string {
	return fmt.Sprintf("%s/storage/v1/b/%s/o/%s",
		c.storageBase, c.bucket, url.PathEscape(c.objectName(key)))
}

func (c *Client) writeRemote(key string, data []byte) error {
	rawURL := fmt.Sprintf("%s/upload/storage/v1/b/%s/o?uploadType=media&name=%s",
		c.storageBase, c.bucket, encodeQuery(c.objectName(key)))
	_, _, err := c.gcsDo(http.MethodPost, rawURL, strings.NewReader(string(data)),
		"application/octet-stream", false)
	if err != nil {
		return fmt.Errorf("files.Write: %w", err)
	}
	return nil
}

func (c *Client) readRemote(key string) ([]byte, bool, error) {
	status, data, err := c.gcsDo(http.MethodGet, c.objectURL(key)+"?alt=media", nil, "", true)
	if err != nil {
		return nil, false, fmt.Errorf("files.Read: %w", err)
	}
	if status == http.StatusNotFound {
		return nil, false, nil
	}
	return data, true, nil
}

func (c *Client) deleteRemote(key string) error {
	_, _, err := c.gcsDo(http.MethodDelete, c.objectURL(key), nil, "", true)
	if err != nil {
		return fmt.Errorf("files.Delete: %w", err)
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// parseListPage validates the GCS list-page schema identically to the
// Node/Python SDKs: a present `items` must be a non-null array, each item a
// non-null object with a present string `name`, and a present `nextPageToken`
// must be a string. Absence is allowed; any present-but-wrong-type value
// (including an explicit `null`) is an error, never a silent empty page.
func parseListPage(data []byte) (names []string, next string, err error) {
	var top *map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, "", fmt.Errorf("malformed response: %w", err)
	}
	if top == nil {
		return nil, "", fmt.Errorf("malformed response: not a JSON object")
	}
	m := *top
	if raw, ok := m["items"]; ok {
		if isJSONNull(raw) {
			return nil, "", fmt.Errorf("malformed response: 'items' is null")
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, "", fmt.Errorf("malformed response: 'items' is not an array")
		}
		for _, itemRaw := range arr {
			if isJSONNull(itemRaw) {
				return nil, "", fmt.Errorf("malformed response: list item is null")
			}
			var im map[string]json.RawMessage
			if err := json.Unmarshal(itemRaw, &im); err != nil {
				return nil, "", fmt.Errorf("malformed response: list item is not an object")
			}
			nameRaw, ok := im["name"]
			if !ok || isJSONNull(nameRaw) {
				return nil, "", fmt.Errorf("malformed response: item 'name' is missing or null")
			}
			var name string
			if err := json.Unmarshal(nameRaw, &name); err != nil {
				return nil, "", fmt.Errorf("malformed response: item 'name' is not a string")
			}
			names = append(names, name)
		}
	}
	if raw, ok := m["nextPageToken"]; ok {
		if isJSONNull(raw) {
			return nil, "", fmt.Errorf("malformed response: 'nextPageToken' is null")
		}
		if err := json.Unmarshal(raw, &next); err != nil {
			return nil, "", fmt.Errorf("malformed response: 'nextPageToken' is not a string")
		}
	}
	return names, next, nil
}

func (c *Client) listRemote(prefix string) ([]string, error) {
	fullPrefix := c.prefix + prefix
	base := fmt.Sprintf("%s/storage/v1/b/%s/o", c.storageBase, c.bucket)
	var keys []string
	pageToken := ""
	for {
		rawURL := base + "?prefix=" + encodeQuery(fullPrefix)
		if pageToken != "" {
			rawURL += "&pageToken=" + encodeQuery(pageToken)
		}
		_, data, err := c.gcsDo(http.MethodGet, rawURL, nil, "", false)
		if err != nil {
			return nil, fmt.Errorf("files.List: %w", err)
		}
		if len(data) == 0 {
			break // empty body → empty page
		}
		names, next, err := parseListPage(data)
		if err != nil {
			return nil, fmt.Errorf("files.List: %w", err)
		}
		for _, name := range names {
			if !strings.HasPrefix(name, c.prefix) {
				continue
			}
			key := strings.TrimPrefix(name, c.prefix)
			if key != "" {
				keys = append(keys, key)
			}
		}
		if next == "" {
			break
		}
		pageToken = next
	}
	sort.Strings(keys)
	return keys, nil
}
