package config

import (
	"os"
	"path/filepath"
	"testing"
)

const validYAML = `
server:
  port: 9090
  read_timeout: 10s
  write_timeout: 15s
  max_body_size: 2097152
  concurrency_limit: 50

routes:
  - path: /hooks/test
    signature:
      type: hmac-sha256
      header: X-Signature
      secret_env: TEST_SECRET
      prefix: "sha256="
      encoding: hex
    destinations:
      - url: https://dest.example.com/hook
        timeout: 5s
    retry:
      max_attempts: 2
      backoff: exponential
      initial_interval: 500ms
      max_interval: 10s

dead_letter:
  type: file
  path: ./dl
  store_body: false
  max_body_bytes: 1024

logging:
  level: debug
  format: text
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_ValidConfig(t *testing.T) {
	t.Setenv("TEST_SECRET", "mysecret")
	path := writeConfig(t, validYAML)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("port = %d, want 9090", cfg.Server.Port)
	}
	if cfg.Server.ConcurrencyLimit != 50 {
		t.Errorf("concurrency_limit = %d, want 50", cfg.Server.ConcurrencyLimit)
	}
	if len(cfg.Routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(cfg.Routes))
	}
	r := cfg.Routes[0]
	if r.Path != "/hooks/test" {
		t.Errorf("path = %q, want /hooks/test", r.Path)
	}
	if r.Signature.Type != "hmac-sha256" {
		t.Errorf("signature type = %q, want hmac-sha256", r.Signature.Type)
	}
	if r.Signature.Prefix != "sha256=" {
		t.Errorf("prefix = %q, want sha256=", r.Signature.Prefix)
	}
	// secret_env should be resolved to the actual value.
	if r.Signature.SecretEnv != "mysecret" {
		t.Errorf("secret = %q, want mysecret", r.Signature.SecretEnv)
	}
	if r.Retry.MaxAttempts != 2 {
		t.Errorf("max_attempts = %d, want 2", r.Retry.MaxAttempts)
	}
	if *cfg.DeadLetter.StoreBody {
		t.Errorf("store_body should be false")
	}
}

func TestLoad_MissingRoutes(t *testing.T) {
	t.Setenv("TEST_SECRET", "s")
	yaml := `
server:
  port: 8080
routes: []
`
	path := writeConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing routes")
	}
}

func TestLoad_RouteWithNoDestinations(t *testing.T) {
	t.Setenv("TEST_SECRET", "s")
	yaml := `
routes:
  - path: /hooks/test
    signature:
      type: hmac-sha256
      header: X-Sig
      secret_env: TEST_SECRET
    destinations: []
`
	path := writeConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for route with no destinations")
	}
}

func TestLoad_MissingEnvVar(t *testing.T) {
	// Ensure the env var is not set.
	os.Unsetenv("MISSING_SECRET_VAR")
	yaml := `
routes:
  - path: /hooks/test
    signature:
      type: hmac-sha256
      header: X-Sig
      secret_env: MISSING_SECRET_VAR
    destinations:
      - url: https://example.com
`
	path := writeConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing env var")
	}
}

func TestLoad_InvalidDuration(t *testing.T) {
	t.Setenv("TEST_SECRET", "s")
	yaml := `
routes:
  - path: /hooks/test
    signature:
      type: hmac-sha256
      header: X-Sig
      secret_env: TEST_SECRET
    destinations:
      - url: https://example.com
        timeout: notaduration
`
	path := writeConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("TEST_SECRET", "s")
	yaml := `
routes:
  - path: /hooks/test
    signature:
      type: hmac-sha256
      header: X-Sig
      secret_env: TEST_SECRET
    destinations:
      - url: https://example.com
`
	path := writeConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("default port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Server.MaxBodySize != 1<<20 {
		t.Errorf("default max_body_size = %d, want %d", cfg.Server.MaxBodySize, 1<<20)
	}

	r := cfg.Routes[0]
	if r.Signature.Encoding != "hex" {
		t.Errorf("default encoding = %q, want hex", r.Signature.Encoding)
	}
	if r.Retry.MaxAttempts != 1 {
		t.Errorf("default max_attempts = %d, want 1", r.Retry.MaxAttempts)
	}
	if r.Destinations[0].Timeout.Seconds() != 10 {
		t.Errorf("default dest timeout = %v, want 10s", r.Destinations[0].Timeout)
	}
	if cfg.DeadLetter.StoreBody == nil || !*cfg.DeadLetter.StoreBody {
		t.Error("default store_body should be true")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_MissingSigFields(t *testing.T) {
	t.Setenv("TEST_SECRET", "s")

	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "missing signature type",
			yaml: `
routes:
  - path: /hooks/test
    signature:
      header: X-Sig
      secret_env: TEST_SECRET
    destinations:
      - url: https://example.com
`,
		},
		{
			name: "missing signature header",
			yaml: `
routes:
  - path: /hooks/test
    signature:
      type: hmac-sha256
      secret_env: TEST_SECRET
    destinations:
      - url: https://example.com
`,
		},
		{
			name: "missing signature secret_env",
			yaml: `
routes:
  - path: /hooks/test
    signature:
      type: hmac-sha256
      header: X-Sig
    destinations:
      - url: https://example.com
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.yaml)
			_, err := Load(path)
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
