// Package email provides a client for the Keelson Email API
// (send, inbound webhook verification, attachment download).
package email

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/keelsonhq/go-sdk/internal/httpclient"
)

// ---------------------------------------------------------------------------
// Send types
// ---------------------------------------------------------------------------

// SendRequest is the payload for POST /v1/email/send.
type SendRequest struct {
	To          []string     `json:"to"`
	CC          []string     `json:"cc,omitempty"`
	BCC         []string     `json:"bcc,omitempty"`
	Subject     string       `json:"subject"`
	Text        *string      `json:"text,omitempty"`
	HTML        *string      `json:"html,omitempty"`
	From        *string      `json:"from,omitempty"`
	FromName    *string      `json:"from_name,omitempty"`
	ReplyTo     *string      `json:"reply_to,omitempty"`
	InReplyTo   *string      `json:"in_reply_to,omitempty"`
	References  []string     `json:"references,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment represents a Base64-encoded email attachment.
type Attachment struct {
	Filename    string `json:"filename"`
	Content     string `json:"content"` // Base64-encoded
	ContentType string `json:"content_type"`
}

// SendResponse is the response from POST /v1/email/send.
type SendResponse struct {
	SendID string `json:"send_id"`
	Status string `json:"status"`
}

// ---------------------------------------------------------------------------
// Inbound webhook types
// ---------------------------------------------------------------------------

// InboundEmail represents the webhook payload delivered to user apps
// at POST /api/webhooks/email.
type InboundEmail struct {
	DeliveryID        string               `json:"delivery_id"`
	Attempt           int                  `json:"attempt"`
	ReceivedAt        time.Time            `json:"received_at"`
	SentAt            *time.Time           `json:"sent_at,omitempty"`
	From              Address              `json:"from"`
	To                []Address            `json:"to"`
	CC                []Address            `json:"cc"`
	ReplyTo           *Address             `json:"reply_to,omitempty"`
	Subject           string               `json:"subject"`
	Text              *string              `json:"text,omitempty"`
	HTML              *string              `json:"html,omitempty"`
	ProviderMessageID *string              `json:"provider_message_id,omitempty"`
	InReplyTo         *string              `json:"in_reply_to,omitempty"`
	References        []string             `json:"references"`
	EnvelopeTo        string               `json:"envelope_to"`
	Authentication    AuthenticationResult `json:"authentication"`
	Spam              SpamAssessment       `json:"spam"`
	Attachments       []AttachmentMeta     `json:"attachments"`
}

// Address is an email name/address pair.
type Address struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// AuthenticationResult contains SPF/DKIM/DMARC results.
type AuthenticationResult struct {
	SPF   *string `json:"spf,omitempty"`
	DKIM  *string `json:"dkim,omitempty"`
	DMARC *string `json:"dmarc,omitempty"`
}

// SpamAssessment contains spam scoring information.
type SpamAssessment struct {
	Score   float64  `json:"score"`
	Verdict string   `json:"verdict"`
	Reasons []string `json:"reasons"`
}

// AttachmentMeta describes an inbound email attachment.
type AttachmentMeta struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int    `json:"size_bytes"`
	DownloadURL string `json:"download_url"`
}

// AttachmentContent is the response from downloading an attachment.
type AttachmentContent struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
	Filename      string
}

// ---------------------------------------------------------------------------
// Email event webhook types
// ---------------------------------------------------------------------------

// EmailEventPayload represents a bounce/complaint/delivery event
// delivered to user apps at POST /api/webhooks/email-events.
type EmailEventPayload struct {
	EventID      string  `json:"event_id"`
	EventType    string  `json:"event_type"` // "bounce", "complaint", "delivered"
	EmailAddress string  `json:"email_address"`
	Provider     *string `json:"provider,omitempty"`
	SendID       *string `json:"send_id,omitempty"`
	// Deprecated: Use SendID to correlate an event with a send.
	ResendEmailID *string `json:"resend_email_id,omitempty"`
	BounceType    *string `json:"bounce_type,omitempty"`
	Detail        *string `json:"detail,omitempty"`
	Timestamp     string  `json:"timestamp"`
}

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

// Client provides access to the Keelson Email API.
type Client struct {
	hc             *httpclient.Client
	useGatewayPath bool
}

// New creates an Email client.
// If baseURL is empty, KEELSON_EMAIL_BASE_URL is preferred, then
// KEELSON_EMAIL_API_URL is used. An explicit baseURL always uses legacy paths.
// If token is empty, KEELSON_EMAIL_TOKEN is used.
func New(baseURL, token string) (*Client, error) {
	useGatewayPath := false
	if baseURL == "" {
		gatewayBaseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("KEELSON_EMAIL_BASE_URL")), "/")
		if gatewayBaseURL != "" {
			baseURL = gatewayBaseURL
			useGatewayPath = true
		} else {
			baseURL = os.Getenv("KEELSON_EMAIL_API_URL")
		}
	}
	if baseURL == "" {
		return nil, fmt.Errorf("email: base_url is required; pass it or set KEELSON_EMAIL_API_URL")
	}
	if token == "" {
		token = os.Getenv("KEELSON_EMAIL_TOKEN")
	}
	if token == "" {
		return nil, fmt.Errorf("email: token is required; pass it or set KEELSON_EMAIL_TOKEN")
	}
	return &Client{
		hc:             httpclient.New(baseURL, token, nil),
		useGatewayPath: useGatewayPath,
	}, nil
}

// ---------------------------------------------------------------------------
// Send
// ---------------------------------------------------------------------------

// Send sends an email. Returns the send response.
func (c *Client) Send(req *SendRequest) (*SendResponse, error) {
	return c.SendCtx(context.Background(), req)
}

// SendCtx is like Send but accepts a context.
func (c *Client) SendCtx(ctx context.Context, req *SendRequest) (*SendResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("email.Send: request is required")
	}
	if len(req.To) == 0 {
		return nil, fmt.Errorf("email.Send: to is required (at least one recipient)")
	}
	if req.Subject == "" {
		return nil, fmt.Errorf("email.Send: subject is required")
	}
	if req.Text == nil && req.HTML == nil {
		return nil, fmt.Errorf("email.Send: at least one of text or html is required")
	}

	body, err := jsonBody(req)
	if err != nil {
		return nil, fmt.Errorf("email.Send: %w", err)
	}

	path := "/v1/email/send"
	if c.useGatewayPath {
		path = "/__keelson/email/send"
	}

	var resp SendResponse
	if err := c.hc.DoJSONCtx(ctx, "POST", path, body, &resp); err != nil {
		return nil, fmt.Errorf("email.Send: %w", err)
	}
	return &resp, nil
}

// ---------------------------------------------------------------------------
// Attachment download
// ---------------------------------------------------------------------------

// DownloadAttachment downloads an inbound email attachment by its ID.
// The caller is responsible for closing the returned Body.
func (c *Client) DownloadAttachment(attachmentID string) (*AttachmentContent, error) {
	return c.DownloadAttachmentCtx(context.Background(), attachmentID)
}

// DownloadAttachmentCtx is like DownloadAttachment but accepts a context.
func (c *Client) DownloadAttachmentCtx(ctx context.Context, attachmentID string) (*AttachmentContent, error) {
	if attachmentID == "" {
		return nil, fmt.Errorf("email.DownloadAttachment: attachment_id is required")
	}

	path := "/v1/email/attachments/" + url.PathEscape(attachmentID)
	if c.useGatewayPath {
		path = "/__keelson/email/attachments/" + url.PathEscape(attachmentID)
	}

	// Use DoRawCtx because the response is binary, not JSON.
	resp, err := c.hc.DoRawCtx(ctx, "GET", path, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("email.DownloadAttachment: %w", err)
	}

	contentLength, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)

	// Extract filename from Content-Disposition header.
	filename := ""
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		// Parse simple `attachment; filename="..."` format.
		if idx := strings.Index(cd, `filename="`); idx >= 0 {
			rest := cd[idx+len(`filename="`):]
			if end := strings.Index(rest, `"`); end >= 0 {
				filename = rest[:end]
			}
		}
	}

	return &AttachmentContent{
		Body:          resp.Body,
		ContentType:   resp.Header.Get("Content-Type"),
		ContentLength: contentLength,
		Filename:      filename,
	}, nil
}

// ---------------------------------------------------------------------------
// Replay / duplicate suppression (idempotency)
// ---------------------------------------------------------------------------

// ReserveStatus is the outcome of IdempotencyStore.Reserve.
type ReserveStatus string

const (
	// StatusAcquired: a fresh reservation (nothing prior, or the prior pending
	// lease expired = crash takeover). Process, then Commit/Release with the Token.
	StatusAcquired ReserveStatus = "acquired"
	// StatusPending: another attempt holds an un-expired reservation. The caller
	// returns a RETRYABLE 5xx (never a 200 duplicate) so the platform keeps
	// retrying until that attempt completes (dedup) or its lease expires (retake).
	StatusPending ReserveStatus = "pending"
	// StatusCompleted: already handled. ACK 200 without re-processing.
	StatusCompleted ReserveStatus = "completed"
)

// ReserveResult is returned by IdempotencyStore.Reserve. Token is set only for
// StatusAcquired and fences the subsequent Commit/Release (compare-and-set).
type ReserveResult struct {
	Status ReserveStatus
	Token  string
}

// IdempotencyStore implements a token-fenced reserve → handler → commit (release
// on failure) state machine so a duplicate delivery (a platform retry — delivery
// is at-least-once — or a captured replay within the signature window) is not
// handled twice. Go has no built-in webhook server, so the app owns this flow:
//
//	msg, res, err := email.VerifyWebhookOnce(r, secret, store)
//	// on *StoreError -> 500 (retryable); other err -> 401
//	switch res.Status {
//	case email.StatusCompleted: return 200            // duplicate
//	case email.StatusPending:   return 503            // in-flight, retryable
//	}
//	// StatusAcquired: ...process...
//	if handlerErr != nil { store.Release(msg.DeliveryID, res.Token); return 500 }
//	store.Commit(msg.DeliveryID, res.Token); return 200
//
// Reserve MUST be atomic, return the three states with a fencing Token, expire a
// stale PENDING reservation (a prior attempt that crashed before commit/release)
// so a retry can re-acquire it, and return an error on a backend failure (wrapped
// as *StoreError so the caller returns a retryable 5xx). Commit/Release are a
// compare-and-set on the Token: a stale attempt (taken over) can NOT complete or
// delete a newer reservation. Back it with storage shared across ALL app instances
// (your database), ideally recording the id in the SAME transaction as the
// handler's side effects for true exactly-once processing.
type IdempotencyStore interface {
	Reserve(id string) (ReserveResult, error)
	Commit(id, token string) error
	Release(id, token string) error
}

// StoreError wraps an idempotency-store BACKEND failure (e.g. the DB is down) so a
// caller can map it to a retryable 5xx — as opposed to a signature error, which is
// a terminal 4xx. Delivery must NOT be dropped on a transient store failure.
type StoreError struct{ Err error }

func (e *StoreError) Error() string {
	return "email: idempotency store error: " + e.Err.Error()
}

func (e *StoreError) Unwrap() error { return e.Err }

// VerifyWebhookOnce verifies the inbound webhook AND reserves its delivery id in
// store. Returns (email, ReserveResult, err). A store backend failure is returned
// as *StoreError so the caller returns 500 (retryable), not 401. The caller then
// switches on ReserveResult.Status: Completed → 200 duplicate; Pending → 503
// retryable; Acquired → process, then Commit (success) / Release (failure) with
// ReserveResult.Token.
func VerifyWebhookOnce(r *http.Request, secret string, store IdempotencyStore) (*InboundEmail, ReserveResult, error) {
	email, err := VerifyWebhook(r, secret)
	if err != nil {
		return nil, ReserveResult{}, err
	}
	res, err := store.Reserve(email.DeliveryID)
	if err != nil {
		return nil, ReserveResult{}, &StoreError{Err: err}
	}
	return email, res, nil
}

// VerifyEventWebhookOnce is VerifyWebhookOnce for bounce/complaint/delivery
// events, reserving the event id.
func VerifyEventWebhookOnce(r *http.Request, secret string, store IdempotencyStore) (*EmailEventPayload, ReserveResult, error) {
	event, err := VerifyEventWebhook(r, secret)
	if err != nil {
		return nil, ReserveResult{}, err
	}
	res, err := store.Reserve(event.EventID)
	if err != nil {
		return nil, ReserveResult{}, &StoreError{Err: err}
	}
	return event, res, nil
}

// ---------------------------------------------------------------------------
// Inbound webhook verification
// ---------------------------------------------------------------------------

// VerifyWebhook verifies and parses an inbound email webhook request.
// The secret is the Svix webhook signing secret (whsec_...).
// Returns the parsed InboundEmail payload.
func VerifyWebhook(r *http.Request, secret string) (*InboundEmail, error) {
	if secret == "" {
		return nil, fmt.Errorf("email.VerifyWebhook: secret is required")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("email.VerifyWebhook: read body: %w", err)
	}

	msgID := r.Header.Get("svix-id")
	timestamp := r.Header.Get("svix-timestamp")
	signature := r.Header.Get("svix-signature")

	if msgID == "" || timestamp == "" || signature == "" {
		return nil, fmt.Errorf("email.VerifyWebhook: missing svix headers")
	}

	if err := verifySvixSignature(body, msgID, timestamp, signature, secret); err != nil {
		return nil, fmt.Errorf("email.VerifyWebhook: %w", err)
	}

	var email InboundEmail
	if err := json.Unmarshal(body, &email); err != nil {
		return nil, fmt.Errorf("email.VerifyWebhook: decode payload: %w", err)
	}
	return &email, nil
}

// VerifyWebhookBytes verifies and parses an inbound email webhook from
// raw body bytes and headers. Useful when the body has already been read.
func VerifyWebhookBytes(body []byte, headers http.Header, secret string) (*InboundEmail, error) {
	if secret == "" {
		return nil, fmt.Errorf("email.VerifyWebhookBytes: secret is required")
	}

	msgID := headers.Get("svix-id")
	timestamp := headers.Get("svix-timestamp")
	signature := headers.Get("svix-signature")

	if msgID == "" || timestamp == "" || signature == "" {
		return nil, fmt.Errorf("email.VerifyWebhookBytes: missing svix headers")
	}

	if err := verifySvixSignature(body, msgID, timestamp, signature, secret); err != nil {
		return nil, fmt.Errorf("email.VerifyWebhookBytes: %w", err)
	}

	var email InboundEmail
	if err := json.Unmarshal(body, &email); err != nil {
		return nil, fmt.Errorf("email.VerifyWebhookBytes: decode payload: %w", err)
	}
	return &email, nil
}

// ---------------------------------------------------------------------------
// Email event webhook verification
// ---------------------------------------------------------------------------

// VerifyEventWebhook verifies and parses a bounce/complaint/delivery
// event webhook request. Same Svix signature verification as VerifyWebhook.
func VerifyEventWebhook(r *http.Request, secret string) (*EmailEventPayload, error) {
	if secret == "" {
		return nil, fmt.Errorf("email.VerifyEventWebhook: secret is required")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("email.VerifyEventWebhook: read body: %w", err)
	}

	msgID := r.Header.Get("svix-id")
	timestamp := r.Header.Get("svix-timestamp")
	signature := r.Header.Get("svix-signature")

	if msgID == "" || timestamp == "" || signature == "" {
		return nil, fmt.Errorf("email.VerifyEventWebhook: missing svix headers")
	}

	if err := verifySvixSignature(body, msgID, timestamp, signature, secret); err != nil {
		return nil, fmt.Errorf("email.VerifyEventWebhook: %w", err)
	}

	var event EmailEventPayload
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("email.VerifyEventWebhook: decode payload: %w", err)
	}
	return &event, nil
}

// VerifyEventWebhookBytes is like VerifyEventWebhook but accepts raw body
// bytes and headers. Useful when the body has already been read.
func VerifyEventWebhookBytes(body []byte, headers http.Header, secret string) (*EmailEventPayload, error) {
	if secret == "" {
		return nil, fmt.Errorf("email.VerifyEventWebhookBytes: secret is required")
	}

	msgID := headers.Get("svix-id")
	timestamp := headers.Get("svix-timestamp")
	signature := headers.Get("svix-signature")

	if msgID == "" || timestamp == "" || signature == "" {
		return nil, fmt.Errorf("email.VerifyEventWebhookBytes: missing svix headers")
	}

	if err := verifySvixSignature(body, msgID, timestamp, signature, secret); err != nil {
		return nil, fmt.Errorf("email.VerifyEventWebhookBytes: %w", err)
	}

	var event EmailEventPayload
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("email.VerifyEventWebhookBytes: decode payload: %w", err)
	}
	return &event, nil
}

// verifySvixSignature verifies the Svix webhook signature.
// Svix signs with HMAC-SHA256 over "{msg_id}.{timestamp}.{body}".
// The secret is base64-encoded after stripping the "whsec_" prefix.
// The signature header may contain multiple signatures separated by spaces.
func verifySvixSignature(body []byte, msgID, timestamp, signature, secret string) error {
	// Validate timestamp to prevent replay attacks (±5 minutes).
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}
	now := time.Now().Unix()
	if math.Abs(float64(now-ts)) > 300 {
		return fmt.Errorf("timestamp too old or too far in the future")
	}

	// Decode the secret key.
	secretKey := secret
	if strings.HasPrefix(secretKey, "whsec_") {
		secretKey = secretKey[len("whsec_"):]
	}
	key, err := base64.StdEncoding.DecodeString(secretKey)
	if err != nil {
		return fmt.Errorf("decode secret: %w", err)
	}

	// Compute expected signature.
	toSign := fmt.Sprintf("%s.%s.%s", msgID, timestamp, string(body))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(toSign))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	// The signature header contains space-separated "v1,<sig>" entries.
	sigs := strings.Split(signature, " ")
	for _, sig := range sigs {
		parts := strings.SplitN(sig, ",", 2)
		if len(parts) != 2 {
			continue
		}
		if parts[0] == "v1" && hmac.Equal([]byte(parts[1]), []byte(expected)) {
			return nil
		}
	}

	return fmt.Errorf("no matching signature found")
}

func jsonBody(v any) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(v); err != nil {
		return nil, fmt.Errorf("encode JSON: %w", err)
	}
	return buf, nil
}
