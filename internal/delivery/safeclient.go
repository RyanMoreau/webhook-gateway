package delivery

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// httpClient is the HTTP client used for all webhook deliveries. It defaults to
// http.DefaultClient so that existing tests (which never call SetClient) are
// unaffected.
var httpClient = http.DefaultClient

// SetClient replaces the package-level HTTP client used by Deliver.
func SetClient(c *http.Client) {
	httpClient = c
}

// NewClient returns an *http.Client whose transport blocks connections to
// private/loopback/link-local IP addresses (SSRF protection). When
// allowPrivate is true the IP check is skipped, which is useful for local
// development.
func NewClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if allowPrivate {
				return dialer.DialContext(ctx, network, addr)
			}

			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("splitting host/port: %w", err)
			}

			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("resolving %s: %w", host, err)
			}

			for _, ip := range ips {
				if isBlockedIP(ip.IP) {
					return nil, fmt.Errorf("blocked address: %s resolves to %s", host, ip.IP)
				}
			}

			// Dial the first resolved IP.
			target := net.JoinHostPort(ips[0].IP.String(), port)
			return dialer.DialContext(ctx, network, target)
		},
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	return &http.Client{Transport: transport}
}

// isBlockedIP returns true if the IP is loopback, private (RFC 1918 / RFC 4193),
// link-local, or unspecified.
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}
