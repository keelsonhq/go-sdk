package media_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelsonhq/go-sdk/internal/testfixtures"
	"github.com/keelsonhq/go-sdk/media"
)

// TestParity_MediaStat parses the shared parity fixture by mocking
// an HTTP HEAD response and asserting the same semantic field values
// that Node and Python parity tests assert.
//
// Go now strips Content-Type parameters (e.g. "; charset=utf-8") in Head,
// matching Node and Python which return bare media types like "text/plain".
func TestParity_MediaStat(t *testing.T) {
	fixture, err := testfixtures.ReadFile("media_stat.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var f struct {
		ContentType   string `json:"content_type"`
		ContentLength int64  `json:"content_length"`
	}
	if err := json.Unmarshal(fixture, &f); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", f.ContentType)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", f.ContentLength))
		w.WriteHeader(200)
	}))
	defer ts.Close()

	client, err := media.New(ts.URL, "test-token")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	meta, err := client.Head("parity-test-file")
	if err != nil {
		t.Fatalf("Head: %v", err)
	}

	// --- Cross-language parity assertions ---
	// All three SDKs strip Content-Type parameters, so "text/plain; charset=utf-8"
	// becomes the bare media type "text/plain".
	if meta.ContentType != "text/plain" {
		t.Errorf("content_type = %q, want %q", meta.ContentType, "text/plain")
	}
	if meta.ContentLength != 204800 {
		t.Errorf("content_length = %d, want 204800", meta.ContentLength)
	}
}
