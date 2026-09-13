// Example: Email sending and delivery event webhook handler.
//
// Demonstrates sending an email and setting up an HTTP handler that receives
// delivery events (bounce / complaint / delivery) via Svix webhook with
// signature verification and idempotent processing.
//
// Run:
//
//	export KEELSON_EMAIL_API_URL=http://localhost:8787
//	export KEELSON_EMAIL_TOKEN=<your-app-token>
//	export KEELSON_EMAIL_WEBHOOK_SECRET=whsec_<base64key>
//	go run ./examples/email-webhook
package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/keelsonhq/go-sdk/email"
)

// dbStore is a demo email.IdempotencyStore implementing the token-fenced 3-state
// machine. In production, back it with your database — Reserve as an atomic upsert
// that returns acquired/pending/completed + a fencing token and expires a stale
// pending lease; Commit/Release as a compare-and-set on the token, ideally in the
// SAME transaction as the handler's side effects — so duplicate deliveries are
// rejected durably across every app instance and a crashed attempt's retry
// re-acquires after the lease.
type dbEntry struct {
	state  string // "pending" | "completed"
	expiry time.Time
	token  string
}

type dbStore struct {
	mu      sync.Mutex
	state   map[string]dbEntry
	counter int
	lease   time.Duration
}

func (s *dbStore) Reserve(id string) (email.ReserveResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.state[id]; ok && e.expiry.After(time.Now()) {
		if e.state == "completed" {
			return email.ReserveResult{Status: email.StatusCompleted}, nil
		}
		return email.ReserveResult{Status: email.StatusPending}, nil
	}
	s.counter++
	token := fmt.Sprintf("%d", s.counter)
	s.state[id] = dbEntry{state: "pending", expiry: time.Now().Add(s.lease), token: token}
	return email.ReserveResult{Status: email.StatusAcquired, Token: token}, nil
}

func (s *dbStore) Commit(id, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.state[id]; ok && e.token == token { // CAS
		s.state[id] = dbEntry{state: "completed", expiry: time.Now().Add(5 * time.Minute), token: token}
	}
	return nil
}

func (s *dbStore) Release(id, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.state[id]; ok && e.token == token { // CAS
		delete(s.state, id)
	}
	return nil
}

func main() {
	client, err := email.New("", "")
	if err != nil {
		log.Fatal(err)
	}

	// Send an email.
	textBody := "Your app is live."
	htmlBody := "<p>Your app is <strong>live</strong>.</p>"
	resp, err := client.Send(&email.SendRequest{
		To:      []string{"recipient@example.com"},
		Subject: "Welcome to Keelson",
		Text:    &textBody,
		HTML:    &htmlBody,
	})
	if err != nil {
		log.Printf("send failed (no valid token configured): %v", err)
	} else {
		fmt.Printf("sent email %s (status: %s)\n", resp.SendID, resp.Status)
	}

	// Delivery event webhook handler.
	// The secret must be in Svix format: "whsec_<base64-encoded-key>".
	webhookSecret := os.Getenv("KEELSON_EMAIL_WEBHOOK_SECRET")
	if webhookSecret == "" {
		log.Fatal("KEELSON_EMAIL_WEBHOOK_SECRET is required (format: whsec_<base64key>)")
	}

	// Svix verification bounds replays to a five-minute window but does not
	// reject a duplicate within it because delivery is at least once. The app
	// owns the reserve → handler → commit flow, releasing on failure, through an
	// email.IdempotencyStore. Back it with a shared durable store for a
	// cross-instance guarantee.
	store := &dbStore{state: map[string]dbEntry{}, lease: 5 * time.Minute}

	http.HandleFunc("/api/webhooks/email-events", eventWebhookHandler(webhookSecret, store))

	log.Println("webhook server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// mapReserveErr maps a VerifyWebhook*Once error to a status code: a store backend
// failure is retryable (500), a signature error is terminal (401). The platform
// treats 4xx as non-retryable, so a DB hiccup must NOT be a 401.
func mapReserveErr(w http.ResponseWriter, err error) {
	var se *email.StoreError
	if errors.As(err, &se) {
		log.Printf("idempotency store error: %v", err)
		http.Error(w, "idempotency store error", http.StatusInternalServerError)
		return
	}
	log.Printf("verification failed: %v", err)
	http.Error(w, "invalid signature", http.StatusUnauthorized)
}

func eventWebhookHandler(secret string, store email.IdempotencyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		event, res, err := email.VerifyEventWebhookOnce(r, secret, store)
		if err != nil {
			mapReserveErr(w, err)
			return
		}
		switch res.Status {
		case email.StatusCompleted:
			w.WriteHeader(http.StatusOK)
			return
		case email.StatusPending:
			http.Error(w, "delivery already in progress", http.StatusServiceUnavailable)
			return
		}

		switch event.EventType {
		case "bounce":
			fmt.Printf("bounced: %s (type: %v)\n", event.EmailAddress, event.BounceType)
		case "complaint":
			fmt.Printf("complaint: %s\n", event.EmailAddress)
		case "delivered":
			fmt.Printf("delivered: %s\n", event.EmailAddress)
		}

		if err := store.Commit(event.EventID, res.Token); err != nil {
			log.Printf("commit failed: %v", err)
			http.Error(w, "idempotency commit failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
