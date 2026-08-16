package email_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keelsonhq/go-sdk/email"
	"github.com/keelsonhq/go-sdk/internal/httpclient"
)

func newTestClient(t *testing.T, url string) *email.Client {
	t.Helper()
	c, err := email.New(url, "test-token")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// --- Constructor ---

func TestNew_RequiresBaseURL(t *testing.T) {
	t.Setenv("KEELSON_EMAIL_API_URL", "")
	_, err := email.New("", "tok")
	if err == nil {
		t.Fatal("expected error for empty base_url, got nil")
	}
}

func TestNew_RequiresToken(t *testing.T) {
	t.Setenv("KEELSON_EMAIL_TOKEN", "")
	_, err := email.New("http://localhost", "")
	if err == nil {
		t.Fatal("expected error for empty token, got nil")
	}
}

func TestNew_EnvFallback(t *testing.T) {
	t.Setenv("KEELSON_EMAIL_API_URL", "http://env-host")
	t.Setenv("KEELSON_EMAIL_TOKEN", "env-token")
	c, err := email.New("", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

// --- Send ---

func TestSend(t *testing.T) {
	text := "Hello"
	tests := []struct {
		name       string
		req        *email.SendRequest
		status     int
		response   any
		wantErr    bool
		wantAPIErr bool
		check      func(t *testing.T, resp *email.SendResponse)
	}{
		{
			name: "success",
			req: &email.SendRequest{
				To:      []string{"user@example.com"},
				Subject: "Test",
				Text:    &text,
			},
			status: 202,
			response: map[string]any{
				"send_id": "550e8400-e29b-41d4-a716-446655440000",
				"status":  "queued",
			},
			check: func(t *testing.T, resp *email.SendResponse) {
				if resp.SendID != "550e8400-e29b-41d4-a716-446655440000" {
					t.Errorf("SendID = %q, want UUID", resp.SendID)
				}
				if resp.Status != "queued" {
					t.Errorf("Status = %q, want queued", resp.Status)
				}
			},
		},
		{
			name: "401 unauthorized",
			req: &email.SendRequest{
				To:      []string{"user@example.com"},
				Subject: "Test",
				Text:    &text,
			},
			status:     401,
			response:   map[string]string{"detail": "invalid token"},
			wantErr:    true,
			wantAPIErr: true,
		},
		{
			name:    "nil request",
			req:     nil,
			wantErr: true,
		},
		{
			name: "empty to",
			req: &email.SendRequest{
				To:      []string{},
				Subject: "Test",
				Text:    &text,
			},
			wantErr: true,
		},
		{
			name: "empty subject",
			req: &email.SendRequest{
				To:      []string{"user@example.com"},
				Subject: "",
				Text:    &text,
			},
			wantErr: true,
		},
		{
			name: "no body",
			req: &email.SendRequest{
				To:      []string{"user@example.com"},
				Subject: "Test",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					t.Errorf("method = %q, want POST", r.Method)
				}
				if r.URL.Path != "/v1/email/send" {
					t.Errorf("path = %q, want /v1/email/send", r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
					t.Errorf("Authorization = %q, want Bearer test-token", got)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer ts.Close()

			client := newTestClient(t, ts.URL)
			resp, err := client.Send(tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantAPIErr {
					var apiErr *httpclient.APIError
					if !errors.As(err, &apiErr) {
						t.Fatalf("expected *APIError, got %T: %v", err, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			if tt.check != nil {
				tt.check(t, resp)
			}
		})
	}
}

func TestSend_RequestBody(t *testing.T) {
	text := "Hello, world!"
	html := "<p>Hello</p>"
	from := "sender@example.com"
	fromName := "Sender"
	replyTo := "reply@example.com"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		// Check required fields.
		to, ok := body["to"].([]any)
		if !ok || len(to) != 2 {
			t.Errorf("to = %v, want 2 recipients", body["to"])
		}
		if body["subject"] != "Test Subject" {
			t.Errorf("subject = %v, want Test Subject", body["subject"])
		}
		if body["text"] != text {
			t.Errorf("text = %v, want %q", body["text"], text)
		}
		if body["html"] != html {
			t.Errorf("html = %v, want %q", body["html"], html)
		}
		if body["from"] != from {
			t.Errorf("from = %v, want %q", body["from"], from)
		}
		if body["from_name"] != fromName {
			t.Errorf("from_name = %v, want %q", body["from_name"], fromName)
		}
		if body["reply_to"] != replyTo {
			t.Errorf("reply_to = %v, want %q", body["reply_to"], replyTo)
		}

		// Check attachments.
		atts, ok := body["attachments"].([]any)
		if !ok || len(atts) != 1 {
			t.Errorf("attachments = %v, want 1 attachment", body["attachments"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(map[string]any{
			"send_id": "abc",
			"status":  "queued",
		})
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	_, err := client.Send(&email.SendRequest{
		To:       []string{"a@example.com", "b@example.com"},
		Subject:  "Test Subject",
		Text:     &text,
		HTML:     &html,
		From:     &from,
		FromName: &fromName,
		ReplyTo:  &replyTo,
		Attachments: []email.Attachment{
			{
				Filename:    "test.txt",
				Content:     base64.StdEncoding.EncodeToString([]byte("file content")),
				ContentType: "text/plain",
			},
		},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestSend_WithContext(t *testing.T) {
	text := "Hello"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(map[string]any{
			"send_id": "abc",
			"status":  "queued",
		})
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	resp, err := client.SendCtx(context.Background(), &email.SendRequest{
		To:      []string{"user@example.com"},
		Subject: "Test",
		Text:    &text,
	})
	if err != nil {
		t.Fatalf("SendCtx: %v", err)
	}
	if resp.SendID != "abc" {
		t.Errorf("SendID = %q, want abc", resp.SendID)
	}
}

// --- DownloadAttachment ---

func TestDownloadAttachment(t *testing.T) {
	fileContent := "binary-data-here"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/v1/email/attachments/att_550e8400-e29b-41d4-a716-446655440000" {
			t.Errorf("path = %q, want /v1/email/attachments/att_550e8400-e29b-41d4-a716-446655440000", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want Bearer test-token", got)
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(fileContent)))
		w.Header().Set("Content-Disposition", `attachment; filename="report.pdf"`)
		w.WriteHeader(200)
		w.Write([]byte(fileContent))
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	att, err := client.DownloadAttachment("att_550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatalf("DownloadAttachment: %v", err)
	}
	defer att.Body.Close()

	if att.ContentType != "application/pdf" {
		t.Errorf("ContentType = %q, want application/pdf", att.ContentType)
	}
	if att.ContentLength != int64(len(fileContent)) {
		t.Errorf("ContentLength = %d, want %d", att.ContentLength, len(fileContent))
	}
	if att.Filename != "report.pdf" {
		t.Errorf("Filename = %q, want report.pdf", att.Filename)
	}

	data, err := io.ReadAll(att.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != fileContent {
		t.Errorf("body = %q, want %q", string(data), fileContent)
	}
}

func TestDownloadAttachment_EmptyID(t *testing.T) {
	client := newTestClient(t, "http://localhost")
	_, err := client.DownloadAttachment("")
	if err == nil {
		t.Fatal("expected error for empty attachment_id, got nil")
	}
}

func TestDownloadAttachment_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"detail":"Attachment not found."}`))
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	_, err := client.DownloadAttachment("att_550e8400-e29b-41d4-a716-446655440000")
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	var apiErr *httpclient.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

// --- Inbound Webhook Verification ---

// signWebhook creates valid Svix webhook headers for testing.
func signWebhook(t *testing.T, body []byte, secret string) (msgID, timestamp, signature string) {
	t.Helper()
	msgID = "msg_test123"
	timestamp = fmt.Sprintf("%d", time.Now().Unix())

	secretKey := secret
	if strings.HasPrefix(secretKey, "whsec_") {
		secretKey = secretKey[len("whsec_"):]
	}
	key, err := base64.StdEncoding.DecodeString(secretKey)
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}

	toSign := fmt.Sprintf("%s.%s.%s", msgID, timestamp, string(body))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(toSign))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	signature = "v1," + sig
	return
}

func TestVerifyWebhook_Success(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret-key-1234567890"))

	payload := email.InboundEmail{
		DeliveryID:     "del_abc123",
		Attempt:        1,
		ReceivedAt:     time.Now().UTC().Truncate(time.Second),
		From:           email.Address{Name: "Sender", Address: "sender@example.com"},
		To:             []email.Address{{Name: "Recv", Address: "recv@example.com"}},
		CC:             []email.Address{},
		Subject:        "Test Email",
		EnvelopeTo:     "app@inbound.example.com",
		Authentication: email.AuthenticationResult{},
		Spam:           email.SpamAssessment{Score: 0.0, Verdict: "clean", Reasons: []string{}},
		Attachments:    []email.AttachmentMeta{},
		References:     []string{},
	}
	body, _ := json.Marshal(payload)

	msgID, ts, sig := signWebhook(t, body, secret)

	req := httptest.NewRequest("POST", "/api/webhooks/email", strings.NewReader(string(body)))
	req.Header.Set("svix-id", msgID)
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", sig)

	result, err := email.VerifyWebhook(req, secret)
	if err != nil {
		t.Fatalf("VerifyWebhook: %v", err)
	}
	if result.DeliveryID != "del_abc123" {
		t.Errorf("DeliveryID = %q, want del_abc123", result.DeliveryID)
	}
	if result.Subject != "Test Email" {
		t.Errorf("Subject = %q, want Test Email", result.Subject)
	}
	if result.From.Address != "sender@example.com" {
		t.Errorf("From.Address = %q, want sender@example.com", result.From.Address)
	}
}

type memIdemEntry struct {
	state string // "pending" | "completed"
	token string
}

type memIdemStore struct {
	state      map[string]memIdemEntry
	counter    int
	reserveErr error
}

func (s *memIdemStore) Reserve(id string) (email.ReserveResult, error) {
	if s.reserveErr != nil {
		return email.ReserveResult{}, s.reserveErr
	}
	if s.state == nil {
		s.state = map[string]memIdemEntry{}
	}
	if e, ok := s.state[id]; ok {
		if e.state == "completed" {
			return email.ReserveResult{Status: email.StatusCompleted}, nil
		}
		return email.ReserveResult{Status: email.StatusPending}, nil
	}
	s.counter++
	token := fmt.Sprintf("%d", s.counter)
	s.state[id] = memIdemEntry{state: "pending", token: token}
	return email.ReserveResult{Status: email.StatusAcquired, Token: token}, nil
}

func (s *memIdemStore) Commit(id, token string) error {
	if e, ok := s.state[id]; ok && e.token == token { // CAS
		s.state[id] = memIdemEntry{state: "completed", token: token}
	}
	return nil
}

func (s *memIdemStore) Release(id, token string) error {
	if e, ok := s.state[id]; ok && e.token == token { // CAS
		delete(s.state, id)
	}
	return nil
}

// VerifyWebhookOnce reserves the delivery ID in an IdempotencyStore so a
// replay or retry with the same delivery ID is reported as a duplicate. A
// shared durable store extends this guarantee across app instances.
func TestVerifyWebhookOnce_DedupesReplay(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret-key-1234567890"))
	payload := email.InboundEmail{
		DeliveryID:     "del_once",
		Attempt:        1,
		ReceivedAt:     time.Now().UTC().Truncate(time.Second),
		From:           email.Address{Name: "S", Address: "s@example.com"},
		To:             []email.Address{{Name: "R", Address: "r@example.com"}},
		CC:             []email.Address{},
		Subject:        "Once",
		EnvelopeTo:     "app@inbound.example.com",
		Authentication: email.AuthenticationResult{},
		Spam:           email.SpamAssessment{Verdict: "clean", Reasons: []string{}},
		Attachments:    []email.AttachmentMeta{},
		References:     []string{},
	}
	body, _ := json.Marshal(payload)
	msgID, ts, sig := signWebhook(t, body, secret)
	store := &memIdemStore{}

	newReq := func() *http.Request {
		req := httptest.NewRequest("POST", "/api/webhooks/email", strings.NewReader(string(body)))
		req.Header.Set("svix-id", msgID)
		req.Header.Set("svix-timestamp", ts)
		req.Header.Set("svix-signature", sig)
		return req
	}

	msg, res, err := email.VerifyWebhookOnce(newReq(), secret, store)
	if err != nil {
		t.Fatalf("first VerifyWebhookOnce: %v", err)
	}
	if res.Status != email.StatusAcquired {
		t.Fatalf("first delivery should be acquired, got %q", res.Status)
	}
	// Handler succeeded → commit; a replay now sees the completed reservation.
	if err := store.Commit(msg.DeliveryID, res.Token); err != nil {
		t.Fatalf("commit: %v", err)
	}

	_, res2, err := email.VerifyWebhookOnce(newReq(), secret, store)
	if err != nil {
		t.Fatalf("second VerifyWebhookOnce: %v", err)
	}
	if res2.Status != email.StatusCompleted {
		t.Fatalf("replayed delivery should be completed (duplicate), got %q", res2.Status)
	}
}

// An unexpired reservation held by another attempt is reported as
// StatusPending (retryable), not StatusCompleted (duplicate).
func TestVerifyWebhookOnce_PendingIsNotCompleted(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret-key-1234567890"))
	body := []byte(`{"delivery_id":"del_pend","attempt":1,"received_at":"2024-01-01T00:00:00Z","from":{"name":"S","address":"s@example.com"},"to":[{"name":"R","address":"r@example.com"}],"cc":[],"subject":"Pend","envelope_to":"app@inbound.example.com","authentication":{},"spam":{"score":0,"verdict":"clean","reasons":[]},"attachments":[],"references":[]}`)
	msgID, ts, sig := signWebhook(t, body, secret)
	store := &memIdemStore{}
	store.Reserve("del_pend") // attempt A holds a pending reservation
	req := httptest.NewRequest("POST", "/api/webhooks/email", strings.NewReader(string(body)))
	req.Header.Set("svix-id", msgID)
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", sig)

	_, res, err := email.VerifyWebhookOnce(req, secret, store)
	if err != nil {
		t.Fatalf("VerifyWebhookOnce: %v", err)
	}
	if res.Status != email.StatusPending {
		t.Fatalf("un-expired pending must be StatusPending (retryable), got %q", res.Status)
	}
}

// After a handler failure, releasing the reservation with its fencing token
// allows a retry to acquire it again.
func TestVerifyWebhookOnce_ReleaseAllowsRetry(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret-key-1234567890"))
	body := []byte(`{"delivery_id":"del_rel","attempt":1,"received_at":"2024-01-01T00:00:00Z","from":{"name":"S","address":"s@example.com"},"to":[{"name":"R","address":"r@example.com"}],"cc":[],"subject":"Rel","envelope_to":"app@inbound.example.com","authentication":{},"spam":{"score":0,"verdict":"clean","reasons":[]},"attachments":[],"references":[]}`)
	msgID, ts, sig := signWebhook(t, body, secret)
	store := &memIdemStore{}
	newReq := func() *http.Request {
		req := httptest.NewRequest("POST", "/api/webhooks/email", strings.NewReader(string(body)))
		req.Header.Set("svix-id", msgID)
		req.Header.Set("svix-timestamp", ts)
		req.Header.Set("svix-signature", sig)
		return req
	}
	msg, res, err := email.VerifyWebhookOnce(newReq(), secret, store)
	if err != nil || res.Status != email.StatusAcquired {
		t.Fatalf("first: status=%v err=%v", res.Status, err)
	}
	// Handler failed → release.
	if err := store.Release(msg.DeliveryID, res.Token); err != nil {
		t.Fatalf("release: %v", err)
	}
	_, res2, err := email.VerifyWebhookOnce(newReq(), secret, store)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if res2.Status != email.StatusAcquired {
		t.Fatalf("after release the retry must re-acquire, got %q", res2.Status)
	}
}

// A stale attempt's commit or release uses an old fencing token, making it a
// compare-and-set no-op that cannot mutate a newer attempt's reservation.
func TestIdempotencyStore_TokenFence(t *testing.T) {
	store := &memIdemStore{}
	rA, _ := store.Reserve("id") // A
	if rA.Status != email.StatusAcquired {
		t.Fatalf("A should acquire")
	}
	// Simulate lease takeover: force the entry back to a pending held by B.
	store.state["id"] = memIdemEntry{state: "pending", token: "B"}
	// Stale A release with the OLD token → must NOT delete B's reservation.
	_ = store.Release("id", rA.Token)
	if _, ok := store.state["id"]; !ok {
		t.Fatalf("stale release deleted B's reservation")
	}
	// Stale A commit with the OLD token → must NOT complete B's reservation.
	_ = store.Commit("id", rA.Token)
	if store.state["id"].state != "pending" {
		t.Fatalf("stale commit completed B's reservation")
	}
	// B's own commit works.
	_ = store.Commit("id", "B")
	if store.state["id"].state != "completed" {
		t.Fatalf("B commit failed")
	}
}

// A store backend failure is returned as *StoreError so the app can map it to
// a retryable 5xx response instead of a terminal 401.
func TestVerifyWebhookOnce_StoreErrorIsTyped(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret-key-1234567890"))
	body := []byte(`{"delivery_id":"del_err","attempt":1,"received_at":"2024-01-01T00:00:00Z","from":{"name":"S","address":"s@example.com"},"to":[{"name":"R","address":"r@example.com"}],"cc":[],"subject":"Err","envelope_to":"app@inbound.example.com","authentication":{},"spam":{"score":0,"verdict":"clean","reasons":[]},"attachments":[],"references":[]}`)
	msgID, ts, sig := signWebhook(t, body, secret)
	store := &memIdemStore{reserveErr: errors.New("DB down")}
	req := httptest.NewRequest("POST", "/api/webhooks/email", strings.NewReader(string(body)))
	req.Header.Set("svix-id", msgID)
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", sig)

	_, _, err := email.VerifyWebhookOnce(req, secret, store)
	if err == nil {
		t.Fatalf("expected a store error")
	}
	var se *email.StoreError
	if !errors.As(err, &se) {
		t.Fatalf("store failure must be a *StoreError, got %T: %v", err, err)
	}
}

func TestVerifyWebhook_EmptySecret(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/webhooks/email", strings.NewReader("{}"))
	_, err := email.VerifyWebhook(req, "")
	if err == nil {
		t.Fatal("expected error for empty secret, got nil")
	}
}

func TestVerifyWebhook_MissingHeaders(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/webhooks/email", strings.NewReader("{}"))
	_, err := email.VerifyWebhook(req, "whsec_dGVzdA==")
	if err == nil {
		t.Fatal("expected error for missing svix headers, got nil")
	}
}

func TestVerifyWebhook_InvalidSignature(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret-key-1234567890"))
	body := []byte(`{"delivery_id":"del_abc"}`)

	req := httptest.NewRequest("POST", "/api/webhooks/email", strings.NewReader(string(body)))
	req.Header.Set("svix-id", "msg_test")
	req.Header.Set("svix-timestamp", fmt.Sprintf("%d", time.Now().Unix()))
	req.Header.Set("svix-signature", "v1,invalidsignature")

	_, err := email.VerifyWebhook(req, secret)
	if err == nil {
		t.Fatal("expected error for invalid signature, got nil")
	}
}

func TestVerifyWebhook_ExpiredTimestamp(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret-key-1234567890"))
	body := []byte(`{"delivery_id":"del_abc"}`)

	// Timestamp 10 minutes ago — should be rejected (>5 min tolerance).
	oldTimestamp := fmt.Sprintf("%d", time.Now().Add(-10*time.Minute).Unix())

	msgID := "msg_test"
	secretKey := secret[len("whsec_"):]
	key, _ := base64.StdEncoding.DecodeString(secretKey)
	toSign := fmt.Sprintf("%s.%s.%s", msgID, oldTimestamp, string(body))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(toSign))
	sig := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/api/webhooks/email", strings.NewReader(string(body)))
	req.Header.Set("svix-id", msgID)
	req.Header.Set("svix-timestamp", oldTimestamp)
	req.Header.Set("svix-signature", sig)

	_, err := email.VerifyWebhook(req, secret)
	if err == nil {
		t.Fatal("expected error for expired timestamp, got nil")
	}
}

func TestVerifyWebhookBytes_Success(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret-key-1234567890"))

	body := []byte(`{"delivery_id":"del_bytes","attempt":1,"received_at":"2024-01-01T00:00:00Z","from":{"name":"S","address":"s@example.com"},"to":[{"name":"R","address":"r@example.com"}],"cc":[],"subject":"Bytes Test","envelope_to":"app@inbound.example.com","authentication":{},"spam":{"score":0,"verdict":"clean","reasons":[]},"attachments":[],"references":[]}`)

	msgID, ts, sig := signWebhook(t, body, secret)

	headers := http.Header{}
	headers.Set("svix-id", msgID)
	headers.Set("svix-timestamp", ts)
	headers.Set("svix-signature", sig)

	result, err := email.VerifyWebhookBytes(body, headers, secret)
	if err != nil {
		t.Fatalf("VerifyWebhookBytes: %v", err)
	}
	if result.DeliveryID != "del_bytes" {
		t.Errorf("DeliveryID = %q, want del_bytes", result.DeliveryID)
	}
}

// --- Event Webhook Verification ---

func TestVerifyEventWebhook_Success(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret-key-1234567890"))

	payload := email.EmailEventPayload{
		EventID:      "evt_abc123",
		EventType:    "bounce",
		EmailAddress: "bounced@example.com",
		Timestamp:    "2026-04-01T12:00:00Z",
	}
	body, _ := json.Marshal(payload)

	msgID, ts, sig := signWebhook(t, body, secret)

	req := httptest.NewRequest("POST", "/api/webhooks/email-events", strings.NewReader(string(body)))
	req.Header.Set("svix-id", msgID)
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", sig)

	result, err := email.VerifyEventWebhook(req, secret)
	if err != nil {
		t.Fatalf("VerifyEventWebhook: %v", err)
	}
	if result.EventID != "evt_abc123" {
		t.Errorf("EventID = %q, want evt_abc123", result.EventID)
	}
	if result.EventType != "bounce" {
		t.Errorf("EventType = %q, want bounce", result.EventType)
	}
	if result.EmailAddress != "bounced@example.com" {
		t.Errorf("EmailAddress = %q, want bounced@example.com", result.EmailAddress)
	}
}

func TestVerifyEventWebhook_EmptySecret(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/webhooks/email-events", strings.NewReader("{}"))
	_, err := email.VerifyEventWebhook(req, "")
	if err == nil {
		t.Fatal("expected error for empty secret, got nil")
	}
}

func TestVerifyEventWebhook_InvalidSignature(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret-key-1234567890"))
	body := []byte(`{"event_id":"evt_abc"}`)

	req := httptest.NewRequest("POST", "/api/webhooks/email-events", strings.NewReader(string(body)))
	req.Header.Set("svix-id", "msg_test")
	req.Header.Set("svix-timestamp", fmt.Sprintf("%d", time.Now().Unix()))
	req.Header.Set("svix-signature", "v1,invalidsignature")

	_, err := email.VerifyEventWebhook(req, secret)
	if err == nil {
		t.Fatal("expected error for invalid signature, got nil")
	}
}

func TestVerifyEventWebhookBytes_Success(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret-key-1234567890"))

	body := []byte(`{"event_id":"evt_bytes","event_type":"delivered","email_address":"user@example.com","timestamp":"2026-04-01T12:00:00Z"}`)

	msgID, ts, sig := signWebhook(t, body, secret)

	headers := http.Header{}
	headers.Set("svix-id", msgID)
	headers.Set("svix-timestamp", ts)
	headers.Set("svix-signature", sig)

	result, err := email.VerifyEventWebhookBytes(body, headers, secret)
	if err != nil {
		t.Fatalf("VerifyEventWebhookBytes: %v", err)
	}
	if result.EventID != "evt_bytes" {
		t.Errorf("EventID = %q, want evt_bytes", result.EventID)
	}
	if result.EventType != "delivered" {
		t.Errorf("EventType = %q, want delivered", result.EventType)
	}
}

func TestVerifyWebhook_MultipleSignatures(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret-key-1234567890"))

	body := []byte(`{"delivery_id":"del_multi","attempt":1,"received_at":"2024-01-01T00:00:00Z","from":{"name":"S","address":"s@example.com"},"to":[{"name":"R","address":"r@example.com"}],"cc":[],"subject":"Multi Sig","envelope_to":"app@inbound.example.com","authentication":{},"spam":{"score":0,"verdict":"clean","reasons":[]},"attachments":[],"references":[]}`)

	msgID, ts, sig := signWebhook(t, body, secret)

	// Prepend an invalid signature; the valid one should still match.
	multiSig := "v1,invalidsig " + sig

	req := httptest.NewRequest("POST", "/api/webhooks/email", strings.NewReader(string(body)))
	req.Header.Set("svix-id", msgID)
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", multiSig)

	result, err := email.VerifyWebhook(req, secret)
	if err != nil {
		t.Fatalf("VerifyWebhook with multiple signatures: %v", err)
	}
	if result.DeliveryID != "del_multi" {
		t.Errorf("DeliveryID = %q, want del_multi", result.DeliveryID)
	}
}
