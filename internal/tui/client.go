package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ryanmoreau/webhook-gateway/internal/stats"
)

// GatewayClient polls a running gateway's /health endpoint.
type GatewayClient struct {
	baseURL   string
	authToken string
	http      *http.Client
}

func NewGatewayClient(baseURL, authToken string) *GatewayClient {
	return &GatewayClient{
		baseURL:   baseURL,
		authToken: authToken,
		http:      &http.Client{Timeout: 5 * time.Second},
	}
}

type healthResponse struct {
	Status string         `json:"status"`
	Stats  stats.Snapshot `json:"stats"`
}

func (c *GatewayClient) FetchStats() (stats.Snapshot, error) {
	resp, err := c.http.Get(c.baseURL + "/health")
	if err != nil {
		return stats.Snapshot{}, fmt.Errorf("connecting to gateway: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return stats.Snapshot{}, fmt.Errorf("gateway returned %d", resp.StatusCode)
	}

	var hr healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return stats.Snapshot{}, fmt.Errorf("decoding health response: %w", err)
	}
	return hr.Stats, nil
}

// AuthGet makes a GET request with the auth token header if configured.
func (c *GatewayClient) AuthGet(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.authToken != "" {
		req.Header.Set("X-Gateway-Token", c.authToken)
	}
	return c.http.Do(req)
}
