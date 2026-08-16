package httpclient_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelsonhq/go-sdk/internal/httpclient"
)

func TestDoJSON_Success(t *testing.T) {
	type resp struct {
		OK bool `json:"ok"`
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-token")
		}
		if got := r.Method; got != "GET" {
			t.Errorf("Method = %q, want GET", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp{OK: true})
	}))
	defer ts.Close()

	c := httpclient.New(ts.URL, "test-token", nil)
	var got resp
	if err := c.DoJSON("GET", "/v1/test", nil, &got); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if !got.OK {
		t.Error("expected OK=true")
	}
}

func TestDoJSON_PostWithBody(t *testing.T) {
	type req struct {
		Name string `json:"name"`
	}
	type resp struct {
		ID string `json:"id"`
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		var body req
		json.NewDecoder(r.Body).Decode(&body)
		if body.Name != "test" {
			t.Errorf("body.Name = %q, want %q", body.Name, "test")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp{ID: "id-1"})
	}))
	defer ts.Close()

	c := httpclient.New(ts.URL, "tok", nil)
	payload := `{"name":"test"}`
	var got resp
	if err := c.DoJSON("POST", "/v1/items", strings.NewReader(payload), &got); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if got.ID != "id-1" {
		t.Errorf("ID = %q, want %q", got.ID, "id-1")
	}
}

func TestDoJSON_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte("forbidden"))
	}))
	defer ts.Close()

	c := httpclient.New(ts.URL, "tok", nil)
	err := c.DoJSON("GET", "/v1/secret", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var apiErr *httpclient.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 403 {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
	if apiErr.Body != "forbidden" {
		t.Errorf("Body = %q, want %q", apiErr.Body, "forbidden")
	}
}

func TestDoJSON_NoToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("Authorization should be empty, got %q", auth)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	c := httpclient.New(ts.URL, "", nil)
	if err := c.DoJSON("GET", "/v1/open", nil, &struct{}{}); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
}
