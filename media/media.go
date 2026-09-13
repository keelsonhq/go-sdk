// Package media provides a client for the Keelson Media API
// (put, get, delete, exists, head/stat, url).
//
// The client resolves its storage mode from KEELSON_MODE plus the platform
// Media env (KEELSON_INTERNAL_MEDIA_BASE_URL / KEELSON_APP_MEDIA_TOKEN):
//
//   - KEELSON_MODE=keelson requires both Media env values; if either is
//     missing the constructor returns a configuration error (ErrConfig)
//     rather than silently writing to the local filesystem. This keeps
//     Keelson deployments fail-closed: a misconfiguration or a disabled
//     Media capability surfaces immediately instead of as delayed data loss.
//   - An incomplete remote config (exactly one of base URL / token) is a
//     configuration error regardless of mode.
//   - KEELSON_MODE=local uses local filesystem storage under MEDIA_DIR
//     (default ./media).
//   - When KEELSON_MODE is unset (local development), the client uses the
//     Keelson media service if both Media env values are present, otherwise falls
//     back to local storage — but refuses that fallback when a platform
//     environment is detected (any of KEELSON_APP_ID / KEELSON_WORKSPACE_ID /
//     KEELSON_DEPLOY_ID set; KEELSON_TENANT_ID remains a deprecated alias), to
//     avoid silent ephemeral writes on Keelson.
//   - Any other non-empty KEELSON_MODE value is a configuration error.
package media

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/keelsonhq/go-sdk/internal/httpclient"
)

// ErrConfig is returned by New when the Media client cannot be constructed
// because the platform Media configuration is missing or inconsistent.
// Use errors.Is(err, media.ErrConfig) to detect it.
var ErrConfig = errors.New("media: configuration error")

// configError carries a fixed, human-readable message while still matching
// ErrConfig via errors.Is. Its Error() is exactly the message (no wrapping
// suffix), so callers/tests can assert the message verbatim.
type configError struct{ msg string }

func (e *configError) Error() string { return e.msg }
func (e *configError) Unwrap() error { return ErrConfig }

// newConfigError builds a *configError with a formatted message.
func newConfigError(format string, args ...any) error {
	return &configError{msg: fmt.Sprintf(format, args...)}
}

// coreIdentifierEnvs are the platform-owned identifiers whose presence means
// the app is running on Keelson and must not fall back to local storage.
var coreIdentifierEnvs = []string{
	"KEELSON_APP_ID",
	"KEELSON_WORKSPACE_ID",
	"KEELSON_TENANT_ID",
	"KEELSON_DEPLOY_ID",
}

// platformEnvVisible reports whether any platform-owned core identifier is
// present in the environment.
func platformEnvVisible() bool {
	for _, name := range coreIdentifierEnvs {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

// MediaMeta holds metadata returned by Head.
type MediaMeta struct {
	ContentType   string
	ContentLength int64
}

// MediaContent holds the response from Get.
// The caller must call Close when done reading Body.
type MediaContent struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
}

// Close releases the underlying HTTP response body.
func (fc *MediaContent) Close() error {
	return fc.Body.Close()
}

// PutOption configures a Put request.
type PutOption func(*putConfig)

type putConfig struct {
	contentType      string
	contentLength    int64
	hasContentLength bool
	filename         string
}

// WithContentType sets the Content-Type header on upload.
func WithContentType(ct string) PutOption {
	return func(c *putConfig) { c.contentType = ct }
}

// WithContentLength sets the Content-Length header on upload.
// If not provided, the server determines the length from the body.
func WithContentLength(n int64) PutOption {
	return func(c *putConfig) {
		c.contentLength = n
		c.hasContentLength = true
	}
}

// WithFilename sets the X-Keelson-Filename header on upload.
func WithFilename(name string) PutOption {
	return func(c *putConfig) { c.filename = name }
}

// Client provides access to the Keelson Media API.
// In Keelson mode it talks to the Keelson media service; in local mode it
// reads/writes to the local filesystem.
type Client struct {
	hc        *httpclient.Client // nil in local mode
	urlPrefix string             // normalized public URL prefix
	mediaDir  string             // local mode storage directory
	local     bool               // true when running outside Keelson
}

// New creates a Media client.
// baseURL is the platform-injected internal media endpoint (for example,
// "http://media.example:8787").
// If empty, KEELSON_INTERNAL_MEDIA_BASE_URL is used.
// token is the app-scoped media token. If empty, KEELSON_APP_MEDIA_TOKEN is used.
//
// The storage mode is resolved from KEELSON_MODE and the Media env; see the
// package documentation for the full runtime-mode contract. New returns an
// error wrapping ErrConfig when the configuration is missing or inconsistent
// (for example KEELSON_MODE=keelson without Media env, or exactly one of the
// base URL / token set). It never silently falls back to local storage on a
// misconfigured Keelson deployment.
func New(baseURL, token string) (*Client, error) {
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("KEELSON_INTERNAL_MEDIA_BASE_URL"))
	} else {
		baseURL = strings.TrimSpace(baseURL)
	}
	if token == "" {
		token = strings.TrimSpace(os.Getenv("KEELSON_APP_MEDIA_TOKEN"))
	} else {
		token = strings.TrimSpace(token)
	}

	prefix := normalizePrefix(os.Getenv("KEELSON_MEDIA_URL_PREFIX"))
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("KEELSON_MODE")))
	hasBase := baseURL != ""
	hasToken := token != ""

	// Partial remote configuration is always an error, regardless of mode:
	// a single missing half almost always means a broken deployment, and
	// falling back to local storage would hide it.
	if hasBase != hasToken {
		missing := "KEELSON_APP_MEDIA_TOKEN"
		if !hasBase {
			missing = "KEELSON_INTERNAL_MEDIA_BASE_URL"
		}
		return nil, newConfigError(
			"media: incomplete remote configuration: both KEELSON_INTERNAL_MEDIA_BASE_URL "+
				"and KEELSON_APP_MEDIA_TOKEN are required, but %s is missing",
			missing)
	}

	switch mode {
	case "keelson":
		if hasBase && hasToken {
			return newRemoteClient(baseURL, token, prefix), nil
		}
		return nil, newConfigError(
			"media: KEELSON_MODE=keelson but Media is not configured " +
				"(KEELSON_INTERNAL_MEDIA_BASE_URL and KEELSON_APP_MEDIA_TOKEN are unset); " +
				"the Media capability is unavailable for this deployment")
	case "local":
		// Explicit local development mode: always use the local filesystem.
		return newLocalClient(prefix), nil
	case "":
		// KEELSON_MODE unset — local development / backward compatibility.
		if hasBase && hasToken {
			return newRemoteClient(baseURL, token, prefix), nil
		}
		// Refuse to silently use ephemeral local storage when platform-owned
		// env is visible (running on Keelson but Media not wired). Developers
		// running locally should set KEELSON_MODE=local explicitly.
		if platformEnvVisible() {
			return nil, newConfigError(
				"media: platform environment detected " +
					"(KEELSON_APP_ID / KEELSON_WORKSPACE_ID (or deprecated " +
					"KEELSON_TENANT_ID alias) / KEELSON_DEPLOY_ID set) but Media is " +
					"not configured; refusing to fall back to local storage. " +
					"Set KEELSON_MODE=local for local development")
		}
		return newLocalClient(prefix), nil
	default:
		// Any other non-empty KEELSON_MODE is a misconfiguration; fail closed
		// rather than treating an unknown mode as local development.
		return nil, newConfigError(
			"media: unrecognized KEELSON_MODE=%q; expected \"keelson\" or \"local\" "+
				"(or unset for local development)",
			mode)
	}
}

// newRemoteClient builds a client that talks to the Keelson media service.
func newRemoteClient(baseURL, token, prefix string) *Client {
	return &Client{
		hc:        httpclient.New(baseURL, token, nil),
		urlPrefix: prefix,
	}
}

// newLocalClient builds a client backed by the local filesystem under MEDIA_DIR.
func newLocalClient(prefix string) *Client {
	dir := strings.TrimSpace(os.Getenv("MEDIA_DIR"))
	if dir == "" {
		dir = "./media"
	}
	return &Client{
		urlPrefix: prefix,
		mediaDir:  dir,
		local:     true,
	}
}

// IsLocal reports whether the client is operating in local filesystem mode.
func (c *Client) IsLocal() bool {
	return c.local
}

// Put uploads a file.
// fileID must be 1-64 characters and must not contain "/".
func (c *Client) Put(fileID string, body io.Reader, opts ...PutOption) error {
	if err := validateFileID(fileID); err != nil {
		return fmt.Errorf("media.Put: %w", err)
	}

	if c.local {
		return c.localPut(fileID, body, opts...)
	}

	cfg := &putConfig{}
	for _, o := range opts {
		o(cfg)
	}

	headers := make(map[string]string)
	if cfg.contentType != "" {
		headers["Content-Type"] = cfg.contentType
	}
	if cfg.hasContentLength {
		headers["Content-Length"] = strconv.FormatInt(cfg.contentLength, 10)
	}
	if cfg.filename != "" {
		headers["X-Keelson-Filename"] = cfg.filename
	}

	path := "/__keelson/internal/files/" + url.PathEscape(fileID)
	resp, err := c.hc.DoRaw("PUT", path, body, headers)
	if err != nil {
		return fmt.Errorf("media.Put: %w", err)
	}
	resp.Body.Close()
	return nil
}

// Upload uploads a file with an auto-generated ULID as the file ID.
// This matches the high-level API in the Node and Python SDKs where
// the caller provides data and receives a generated file ID back.
//
// If no content type is set via WithContentType, it is guessed from
// the filename (if provided via WithFilename), falling back to
// "application/octet-stream".
func (c *Client) Upload(body io.Reader, opts ...PutOption) (string, error) {
	cfg := &putConfig{}
	for _, o := range opts {
		o(cfg)
	}

	// Guess content type from filename when not explicitly set.
	if cfg.contentType == "" && cfg.filename != "" {
		if guessed := mime.TypeByExtension(filepath.Ext(cfg.filename)); guessed != "" {
			cfg.contentType = stripMIMEParams(guessed)
		}
	}

	// Default to application/octet-stream, matching Node/Python SDKs.
	if cfg.contentType == "" {
		cfg.contentType = "application/octet-stream"
	}

	fileID := newULID()
	if err := c.Put(fileID, body, toPutOptions(cfg)...); err != nil {
		return "", err
	}
	return fileID, nil
}

// stripMIMEParams removes parameters (e.g. "; charset=utf-8") from a
// MIME type string, returning only the media type. This keeps parity
// with Node/Python which use bare types like "text/plain".
func stripMIMEParams(mimeType string) string {
	mt, _, _ := mime.ParseMediaType(mimeType)
	if mt == "" {
		return mimeType
	}
	return mt
}

// toPutOptions converts a resolved putConfig back into PutOption slice.
func toPutOptions(cfg *putConfig) []PutOption {
	var opts []PutOption
	if cfg.contentType != "" {
		opts = append(opts, WithContentType(cfg.contentType))
	}
	if cfg.hasContentLength {
		opts = append(opts, WithContentLength(cfg.contentLength))
	}
	if cfg.filename != "" {
		opts = append(opts, WithFilename(cfg.filename))
	}
	return opts
}

// Get downloads a file and returns its content.
// The caller must call MediaContent.Close when done.
func (c *Client) Get(fileID string) (*MediaContent, error) {
	if err := validateFileID(fileID); err != nil {
		return nil, fmt.Errorf("media.Get: %w", err)
	}

	if c.local {
		return c.localGet(fileID)
	}

	path := "/__keelson/internal/files/" + url.PathEscape(fileID)
	resp, err := c.hc.DoRaw("GET", path, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("media.Get: %w", err)
	}

	contentLength, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	return &MediaContent{
		Body:          resp.Body,
		ContentType:   resp.Header.Get("Content-Type"),
		ContentLength: contentLength,
	}, nil
}

// Delete removes a file.
func (c *Client) Delete(fileID string) error {
	if err := validateFileID(fileID); err != nil {
		return fmt.Errorf("media.Delete: %w", err)
	}

	if c.local {
		return c.localDelete(fileID)
	}

	path := "/__keelson/internal/files/" + url.PathEscape(fileID)
	resp, err := c.hc.DoRaw("DELETE", path, nil, nil)
	if err != nil {
		// Treat 404 as success (idempotent delete), consistent with
		// local mode and the Node/Python SDKs.
		var apiErr *httpclient.APIError
		if isAPIError(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("media.Delete: %w", err)
	}
	resp.Body.Close()
	return nil
}

// Exists checks whether a file exists by issuing a HEAD request (Keelson mode)
// or checking the filesystem (local mode).
// Returns true if the file exists, false if 404 / not on disk.
func (c *Client) Exists(fileID string) (bool, error) {
	if err := validateFileID(fileID); err != nil {
		return false, fmt.Errorf("media.Exists: %w", err)
	}

	if c.local {
		return c.localExists(fileID)
	}

	_, err := c.Head(fileID)
	if err != nil {
		var apiErr *httpclient.APIError
		if isAPIError(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Head retrieves file metadata without downloading the body.
// In local mode it stats the file and reads the companion .meta.json.
func (c *Client) Head(fileID string) (*MediaMeta, error) {
	if err := validateFileID(fileID); err != nil {
		return nil, fmt.Errorf("media.Head: %w", err)
	}

	if c.local {
		return c.localHead(fileID)
	}

	path := "/__keelson/internal/files/" + url.PathEscape(fileID)
	resp, err := c.hc.DoRaw("HEAD", path, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("media.Head: %w", err)
	}
	resp.Body.Close()

	contentLength, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	return &MediaMeta{
		// Strip Content-Type parameters (e.g. "; charset=utf-8") for parity
		// with the Node and Python SDKs, which return bare media types.
		ContentType:   stripMIMEParams(resp.Header.Get("Content-Type")),
		ContentLength: contentLength,
	}, nil
}

// URL returns the public URL path for a file.
// The returned value is a relative path (e.g. "/media/my-file") suitable
// for use in HTML or redirects. It uses KEELSON_MEDIA_URL_PREFIX (default "/media/").
func (c *Client) URL(fileID string) string {
	return c.urlPrefix + url.PathEscape(fileID)
}

// ---------------------------------------------------------------------------
// Local filesystem operations
// ---------------------------------------------------------------------------

func (c *Client) localPath(fileID string) string {
	return filepath.Join(c.mediaDir, fileID)
}

func (c *Client) localMetaPath(fileID string) string {
	return filepath.Join(c.mediaDir, fileID+".meta.json")
}

type localMeta struct {
	ContentType string `json:"content_type"`
}

func (c *Client) localPut(fileID string, body io.Reader, opts ...PutOption) error {
	cfg := &putConfig{}
	for _, o := range opts {
		o(cfg)
	}

	if err := os.MkdirAll(c.mediaDir, 0o755); err != nil {
		return fmt.Errorf("media.Put: create dir: %w", err)
	}

	target := c.localPath(fileID)
	f, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("media.Put: create file: %w", err)
	}
	if _, err := io.Copy(f, body); err != nil {
		f.Close()
		return fmt.Errorf("media.Put: write file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("media.Put: close file: %w", err)
	}

	ct := cfg.contentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	meta := localMeta{ContentType: ct}
	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(c.localMetaPath(fileID), metaBytes, 0o644); err != nil {
		return fmt.Errorf("media.Put: write meta: %w", err)
	}
	return nil
}

func (c *Client) localGet(fileID string) (*MediaContent, error) {
	target := c.localPath(fileID)
	f, err := os.Open(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("media.Get: file not found: %s", fileID)
		}
		return nil, fmt.Errorf("media.Get: open file: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("media.Get: stat file: %w", err)
	}

	ct := c.readLocalContentType(fileID)
	return &MediaContent{
		Body:          f,
		ContentType:   ct,
		ContentLength: info.Size(),
	}, nil
}

func (c *Client) localDelete(fileID string) error {
	target := c.localPath(fileID)
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("media.Delete: %w", err)
	}
	// Best-effort remove meta file.
	os.Remove(c.localMetaPath(fileID))
	return nil
}

func (c *Client) localExists(fileID string) (bool, error) {
	_, err := os.Stat(c.localPath(fileID))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("media.Exists: %w", err)
	}
	return true, nil
}

func (c *Client) localHead(fileID string) (*MediaMeta, error) {
	info, err := os.Stat(c.localPath(fileID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("media.Head: file not found: %s", fileID)
		}
		return nil, fmt.Errorf("media.Head: %w", err)
	}
	ct := c.readLocalContentType(fileID)
	return &MediaMeta{
		ContentType:   ct,
		ContentLength: info.Size(),
	}, nil
}

func (c *Client) readLocalContentType(fileID string) string {
	data, err := os.ReadFile(c.localMetaPath(fileID))
	if err != nil {
		return "application/octet-stream"
	}
	var m localMeta
	if err := json.Unmarshal(data, &m); err != nil || m.ContentType == "" {
		return "application/octet-stream"
	}
	return m.ContentType
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// isAPIError extracts an *httpclient.APIError from err via unwrapping.
func isAPIError(err error, target **httpclient.APIError) bool {
	for err != nil {
		if e, ok := err.(*httpclient.APIError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func validateFileID(fileID string) error {
	if fileID == "" {
		return fmt.Errorf("file_id is required")
	}
	if len(fileID) > 64 {
		return fmt.Errorf("file_id must be at most 64 characters")
	}
	for _, ch := range fileID {
		if ch == '/' {
			return fmt.Errorf("file_id must not contain '/'")
		}
	}
	return nil
}

// normalizePrefix ensures the URL prefix starts and ends with "/".
// Default is "/media/".
func normalizePrefix(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/media/"
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	if !strings.HasSuffix(raw, "/") {
		raw = raw + "/"
	}
	return raw
}
