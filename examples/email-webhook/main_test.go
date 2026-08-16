package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/keelsonhq/go-sdk/email"
)

// deterministic fake secret for tests
const testSecret = "whsec_ZmFrZS10ZXN0LXdlYmhvb2sta2V5ISE="

func signBody(body string) http.Header {
	msgID := "msg_test_123"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	key, _ := base64.StdEncoding.DecodeString(testSecret[len("whsec_"):])
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(msgID + "." + ts + "." + body))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	h := http.Header{}
	h.Set("svix-id", msgID)
	h.Set("svix-timestamp", ts)
	h.Set("svix-signature", "v1,"+sig)
	return h
}

// commitFailStore acquires normally but always fails Commit — modelling a commit
// transaction that rolls back so the completed record is not durable.
type commitFailStore struct{ counter int }

func (s *commitFailStore) Reserve(id string) (email.ReserveResult, error) {
	s.counter++
	return email.ReserveResult{Status: email.StatusAcquired, Token: fmt.Sprintf("%d", s.counter)}, nil
}
func (s *commitFailStore) Commit(id, token string) error  { return errors.New("commit DB error") }
func (s *commitFailStore) Release(id, token string) error { return nil }

// If the handler succeeds but Commit fails, the example must fail closed with
// a retryable 500. Returning 200 would mark delivery complete without a durable
// reservation and could let a replay process the message twice.
func TestInboundHandler_CommitFailureReturns500(t *testing.T) {
	client, err := email.New("http://example.invalid", "tok")
	if err != nil {
		t.Fatalf("email.New: %v", err)
	}
	body := `{"delivery_id":"del_c","attempt":1,"received_at":"2024-01-01T00:00:00Z","from":{"name":"S","address":"s@example.com"},"to":[{"name":"R","address":"r@example.com"}],"cc":[],"subject":"C","envelope_to":"app@inbound.example.com","authentication":{},"spam":{"score":0,"verdict":"clean","reasons":[]},"attachments":[],"references":[]}`
	req := httptest.NewRequest("POST", "/api/webhooks/email", strings.NewReader(body))
	for k, v := range signBody(body) {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()

	inboundWebhookHandler(client, testSecret, &commitFailStore{})(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("commit failure must return 500, got %d", rec.Code)
	}
}

func TestEventHandler_CommitFailureReturns500(t *testing.T) {
	body := `{"event_id":"evt_c","event_type":"bounce","email_address":"a@b.com","timestamp":"2024-01-01T00:00:00Z"}`
	req := httptest.NewRequest("POST", "/api/webhooks/email-events", strings.NewReader(body))
	for k, v := range signBody(body) {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()

	eventWebhookHandler(testSecret, &commitFailStore{})(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("event commit failure must return 500, got %d", rec.Code)
	}
}
