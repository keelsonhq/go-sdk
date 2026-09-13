package email_test

import (
	"encoding/json"
	"testing"

	"github.com/keelsonhq/go-sdk/email"
	"github.com/keelsonhq/go-sdk/internal/testfixtures"
)

// TestParity_InboundEmail parses the shared parity fixture via json.Unmarshal
// and asserts the same semantic field values that Node and Python parity tests assert.
func TestParity_InboundEmail(t *testing.T) {
	fixture, err := testfixtures.ReadFile("email_inbound.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var msg email.InboundEmail
	if err := json.Unmarshal(fixture, &msg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// --- Cross-language parity assertions ---
	if msg.DeliveryID != "dlv_parity01" {
		t.Errorf("delivery_id = %q, want %q", msg.DeliveryID, "dlv_parity01")
	}
	if msg.Attempt != 1 {
		t.Errorf("attempt = %d, want 1", msg.Attempt)
	}
	// Go parses timestamps into time.Time; Node/Python keep ISO strings.
	// Assert exact values to catch timezone or precision drift.
	if got := msg.ReceivedAt.UTC().Format("2006-01-02T15:04:05Z"); got != "2026-01-15T10:00:00Z" {
		t.Errorf("received_at = %q, want %q", got, "2026-01-15T10:00:00Z")
	}
	if msg.SentAt == nil {
		t.Fatal("sent_at is nil, want non-nil")
	}
	if got := msg.SentAt.UTC().Format("2006-01-02T15:04:05Z"); got != "2026-01-15T09:59:00Z" {
		t.Errorf("sent_at = %q, want %q", got, "2026-01-15T09:59:00Z")
	}

	if msg.From.Name != "Sender" {
		t.Errorf("from.name = %q, want %q", msg.From.Name, "Sender")
	}
	if msg.From.Address != "sender@example.com" {
		t.Errorf("from.address = %q, want %q", msg.From.Address, "sender@example.com")
	}

	if len(msg.To) != 1 {
		t.Fatalf("to length = %d, want 1", len(msg.To))
	}
	if msg.To[0].Address != "receiver@example.com" {
		t.Errorf("to[0].address = %q, want %q", msg.To[0].Address, "receiver@example.com")
	}

	if len(msg.CC) != 0 {
		t.Errorf("cc length = %d, want 0", len(msg.CC))
	}
	if msg.ReplyTo != nil {
		t.Errorf("reply_to = %v, want nil", msg.ReplyTo)
	}

	if msg.Subject != "Parity test" {
		t.Errorf("subject = %q, want %q", msg.Subject, "Parity test")
	}
	if msg.Text == nil || *msg.Text != "Hello from parity test" {
		t.Errorf("text = %v, want %q", msg.Text, "Hello from parity test")
	}
	if msg.HTML != nil {
		t.Errorf("html = %v, want nil", msg.HTML)
	}

	if msg.ProviderMessageID == nil || *msg.ProviderMessageID != "msg_provider_01" {
		t.Errorf("provider_message_id = %v, want %q", msg.ProviderMessageID, "msg_provider_01")
	}
	if msg.InReplyTo != nil {
		t.Errorf("in_reply_to = %v, want nil", msg.InReplyTo)
	}
	if len(msg.References) != 1 || msg.References[0] != "ref-001" {
		t.Errorf("references = %v, want [ref-001]", msg.References)
	}
	if msg.EnvelopeTo != "receiver@example.com" {
		t.Errorf("envelope_to = %q, want %q", msg.EnvelopeTo, "receiver@example.com")
	}

	// Authentication
	if msg.Authentication.SPF == nil || *msg.Authentication.SPF != "pass" {
		t.Errorf("authentication.spf = %v, want %q", msg.Authentication.SPF, "pass")
	}
	if msg.Authentication.DKIM == nil || *msg.Authentication.DKIM != "pass" {
		t.Errorf("authentication.dkim = %v, want %q", msg.Authentication.DKIM, "pass")
	}
	if msg.Authentication.DMARC == nil || *msg.Authentication.DMARC != "pass" {
		t.Errorf("authentication.dmarc = %v, want %q", msg.Authentication.DMARC, "pass")
	}

	// Spam
	if msg.Spam.Score != 0.1 {
		t.Errorf("spam.score = %f, want 0.1", msg.Spam.Score)
	}
	if msg.Spam.Verdict != "clean" {
		t.Errorf("spam.verdict = %q, want %q", msg.Spam.Verdict, "clean")
	}
	if len(msg.Spam.Reasons) != 0 {
		t.Errorf("spam.reasons = %v, want empty", msg.Spam.Reasons)
	}

	// Attachments
	if len(msg.Attachments) != 1 {
		t.Fatalf("attachments length = %d, want 1", len(msg.Attachments))
	}
	att := msg.Attachments[0]
	if att.ID != "att_001" {
		t.Errorf("attachments[0].id = %q, want %q", att.ID, "att_001")
	}
	if att.Filename != "document.pdf" {
		t.Errorf("attachments[0].filename = %q, want %q", att.Filename, "document.pdf")
	}
	if att.ContentType != "application/pdf" {
		t.Errorf("attachments[0].content_type = %q, want %q", att.ContentType, "application/pdf")
	}
	if att.SizeBytes != 1024 {
		t.Errorf("attachments[0].size_bytes = %d, want 1024", att.SizeBytes)
	}
}

// TestParity_EmailEvent parses the shared parity fixture.
func TestParity_EmailEvent(t *testing.T) {
	fixture, err := testfixtures.ReadFile("email_event.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var evt email.EmailEventPayload
	if err := json.Unmarshal(fixture, &evt); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// --- Cross-language parity assertions ---
	if evt.EventID != "evt_parity01" {
		t.Errorf("event_id = %q, want %q", evt.EventID, "evt_parity01")
	}
	if evt.EventType != "bounce" {
		t.Errorf("event_type = %q, want %q", evt.EventType, "bounce")
	}
	if evt.EmailAddress != "bounced@example.com" {
		t.Errorf("email_address = %q, want %q", evt.EmailAddress, "bounced@example.com")
	}
	if evt.Provider == nil || *evt.Provider != "resend" {
		t.Errorf("provider = %v, want %q", evt.Provider, "resend")
	}
	if evt.SendID == nil || *evt.SendID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("send_id = %v, want expected UUID", evt.SendID)
	}
	if evt.ResendEmailID == nil || *evt.ResendEmailID != "re_001" {
		t.Errorf("resend_email_id = %v, want %q", evt.ResendEmailID, "re_001")
	}
	if evt.BounceType == nil || *evt.BounceType != "hard" {
		t.Errorf("bounce_type = %v, want %q", evt.BounceType, "hard")
	}
	if evt.Detail == nil || *evt.Detail != "Mailbox not found" {
		t.Errorf("detail = %v, want %q", evt.Detail, "Mailbox not found")
	}
	if evt.Timestamp != "2026-01-15T11:00:00Z" {
		t.Errorf("timestamp = %q, want %q", evt.Timestamp, "2026-01-15T11:00:00Z")
	}
}
