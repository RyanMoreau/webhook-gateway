package delivery

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsBlockedIP(t *testing.T) {
	tests := []struct {
		ip      string
		blocked bool
	}{
		// Loopback
		{"127.0.0.1", true},
		{"::1", true},

		// RFC 1918
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},

		// Link-local
		{"169.254.169.254", true},
		{"fe80::1", true},

		// Unspecified
		{"0.0.0.0", true},
		{"::", true},

		// Public IPs — should not be blocked
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false},
		{"2607:f8b0:4004:800::200e", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tt.ip)
			}
			got := isBlockedIP(ip)
			if got != tt.blocked {
				t.Errorf("isBlockedIP(%s) = %v, want %v", tt.ip, got, tt.blocked)
			}
		})
	}
}

func TestClient_LoopbackBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	old := httpClient
	defer func() { httpClient = old }()

	httpClient = NewClient(false)

	dest := Destination{URL: srv.URL, Timeout: 5 * time.Second}
	err := Deliver(context.Background(), dest, nil, []byte("body"))
	if err == nil {
		t.Fatal("expected error for loopback destination")
	}
	if !strings.Contains(err.Error(), "blocked address") {
		t.Errorf("error = %q, want mention of 'blocked address'", err)
	}
}

func TestClient_AllowPrivateBypass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	old := httpClient
	defer func() { httpClient = old }()

	httpClient = NewClient(true)

	dest := Destination{URL: srv.URL, Timeout: 5 * time.Second}
	err := Deliver(context.Background(), dest, nil, []byte("body"))
	if err != nil {
		t.Fatalf("expected success with allowPrivate=true: %v", err)
	}
}

func TestClient_PublicIPNotBlocked(t *testing.T) {
	publicIPs := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34"}
	for _, ipStr := range publicIPs {
		ip := net.ParseIP(ipStr)
		if isBlockedIP(ip) {
			t.Errorf("isBlockedIP(%s) = true, want false for public IP", ipStr)
		}
	}
}

func TestClient_BlocksAllResolvedIPs(t *testing.T) {
	// When the safe client is active, connecting to any loopback address
	// should fail. We verify by attempting a connection to localhost which
	// resolves to 127.0.0.1 (and possibly ::1), all of which are blocked.
	old := httpClient
	defer func() { httpClient = old }()

	httpClient = NewClient(false)

	// Use a listener on loopback to ensure the port is valid.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	dest := Destination{
		URL:     "http://localhost:" + portFromAddr(ln.Addr().String()),
		Timeout: 5 * time.Second,
	}
	err = Deliver(context.Background(), dest, nil, []byte("body"))
	if err == nil {
		t.Fatal("expected error when all resolved IPs are blocked")
	}
	if !strings.Contains(err.Error(), "blocked address") {
		t.Errorf("error = %q, want mention of 'blocked address'", err)
	}
}

func portFromAddr(addr string) string {
	_, port, _ := net.SplitHostPort(addr)
	return port
}
