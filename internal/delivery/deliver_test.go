package delivery

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDeliver_Success(t *testing.T) {
	var gotBody []byte
	var gotHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	dest := Destination{URL: srv.URL, Timeout: 5 * time.Second}
	headers := http.Header{"Content-Type": {"application/json"}, "X-Custom": {"val"}}
	body := []byte(`{"test":true}`)

	err := Deliver(context.Background(), dest, headers, body)
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if string(gotBody) != string(body) {
		t.Errorf("body = %q, want %q", gotBody, body)
	}
	if gotHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotHeaders.Get("Content-Type"))
	}
	if gotHeaders.Get("X-Custom") != "val" {
		t.Errorf("X-Custom = %q, want val", gotHeaders.Get("X-Custom"))
	}
}

func TestDeliver_500_Retryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	dest := Destination{URL: srv.URL, Timeout: 5 * time.Second}
	err := Deliver(context.Background(), dest, http.Header{}, []byte("body"))
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if !IsRetryable(err) {
		t.Fatal("expected 500 error to be retryable")
	}
}

func TestDeliver_400_NonRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
	}))
	defer srv.Close()

	dest := Destination{URL: srv.URL, Timeout: 5 * time.Second}
	err := Deliver(context.Background(), dest, http.Header{}, []byte("body"))
	if err == nil {
		t.Fatal("expected error for 400")
	}
	if IsRetryable(err) {
		t.Fatal("expected 400 error to be non-retryable")
	}
}

func TestDeliver_Timeout_Retryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	dest := Destination{URL: srv.URL, Timeout: 50 * time.Millisecond}
	err := Deliver(context.Background(), dest, http.Header{}, []byte("body"))
	if err == nil {
		t.Fatal("expected error for timeout")
	}
	if !IsRetryable(err) {
		t.Fatal("expected timeout error to be retryable")
	}
}

func TestDeliver_HeadersForwarded(t *testing.T) {
	var gotHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		w.WriteHeader(200)
	}))
	defer srv.Close()

	headers := http.Header{
		"Content-Type":                   {"application/json"},
		"X-Webhook-Gateway-Request-Id":   {"req-123"},
		"X-GitHub-Event":                 {"push"},
	}
	dest := Destination{URL: srv.URL, Timeout: 5 * time.Second}
	err := Deliver(context.Background(), dest, headers, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	if gotHeaders.Get("X-Webhook-Gateway-Request-Id") != "req-123" {
		t.Error("X-Webhook-Gateway-Request-Id not forwarded")
	}
	if gotHeaders.Get("X-GitHub-Event") != "push" {
		t.Error("X-GitHub-Event not forwarded")
	}
}
