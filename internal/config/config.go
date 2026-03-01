package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Routes     []RouteConfig    `yaml:"routes"`
	DeadLetter DeadLetterConfig `yaml:"dead_letter"`
	Logging    LoggingConfig    `yaml:"logging"`
}

type ServerConfig struct {
	Port             int           `yaml:"port"`
	ReadTimeout      time.Duration `yaml:"read_timeout"`
	WriteTimeout     time.Duration `yaml:"write_timeout"`
	MaxBodySize      int64         `yaml:"max_body_size"`
	ConcurrencyLimit int           `yaml:"concurrency_limit"`
}

type RouteConfig struct {
	Path           string            `yaml:"path"`
	Signature      SignatureConfig   `yaml:"signature"`
	Idempotency    IdempotencyConfig `yaml:"idempotency"`
	Destinations   []DestConfig      `yaml:"destinations"`
	ForwardHeaders []string          `yaml:"forward_headers"`
	Retry          RetryConfig       `yaml:"retry"`
}

type SignatureConfig struct {
	Type      string        `yaml:"type"`
	Header    string        `yaml:"header"`
	SecretEnv string        `yaml:"secret_env"`
	Prefix    string        `yaml:"prefix"`
	Encoding  string        `yaml:"encoding"`
	Tolerance time.Duration `yaml:"tolerance"`
}

type IdempotencyConfig struct {
	Enabled bool          `yaml:"enabled"`
	TTL     time.Duration `yaml:"ttl"`
	KeyPath string        `yaml:"key_path"`
}

type DestConfig struct {
	URL     string        `yaml:"url"`
	Timeout time.Duration `yaml:"timeout"`
}

type RetryConfig struct {
	MaxAttempts     int           `yaml:"max_attempts"`
	Backoff         string        `yaml:"backoff"`
	InitialInterval time.Duration `yaml:"initial_interval"`
	MaxInterval     time.Duration `yaml:"max_interval"`
}

type DeadLetterConfig struct {
	Type         string `yaml:"type"`
	Path         string `yaml:"path"`
	StoreBody    *bool  `yaml:"store_body"`
	MaxBodyBytes int64  `yaml:"max_body_bytes"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Load reads a YAML config file, resolves secret_env references, and validates.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	setDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	if err := resolveSecrets(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func setDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.ReadTimeout == 0 {
		cfg.Server.ReadTimeout = 30 * time.Second
	}
	if cfg.Server.WriteTimeout == 0 {
		cfg.Server.WriteTimeout = 30 * time.Second
	}
	if cfg.Server.MaxBodySize == 0 {
		cfg.Server.MaxBodySize = 1 << 20 // 1MB
	}
	if cfg.DeadLetter.StoreBody == nil {
		t := true
		cfg.DeadLetter.StoreBody = &t
	}

	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if r.Signature.Encoding == "" {
			r.Signature.Encoding = "hex"
		}
		if r.Retry.MaxAttempts == 0 {
			r.Retry.MaxAttempts = 1
		}
		if r.Retry.Backoff == "" {
			r.Retry.Backoff = "exponential"
		}
		if r.Retry.InitialInterval == 0 {
			r.Retry.InitialInterval = 1 * time.Second
		}
		if r.Retry.MaxInterval == 0 {
			r.Retry.MaxInterval = 30 * time.Second
		}
		for j := range r.Destinations {
			if r.Destinations[j].Timeout == 0 {
				r.Destinations[j].Timeout = 10 * time.Second
			}
		}
	}
}

func validate(cfg *Config) error {
	if len(cfg.Routes) == 0 {
		return fmt.Errorf("at least one route is required")
	}

	for i, r := range cfg.Routes {
		if r.Path == "" {
			return fmt.Errorf("route %d: path is required", i)
		}
		if len(r.Destinations) == 0 {
			return fmt.Errorf("route %q: at least one destination is required", r.Path)
		}
		if r.Signature.Type == "" {
			return fmt.Errorf("route %q: signature type is required", r.Path)
		}
		if r.Signature.Header == "" {
			return fmt.Errorf("route %q: signature header is required", r.Path)
		}
		if r.Signature.SecretEnv == "" {
			return fmt.Errorf("route %q: signature secret_env is required", r.Path)
		}
		for j, d := range r.Destinations {
			if d.URL == "" {
				return fmt.Errorf("route %q: destination %d: url is required", r.Path, j)
			}
		}
	}

	if cfg.DeadLetter.Type != "" && cfg.DeadLetter.Type != "file" {
		return fmt.Errorf("dead_letter type %q is not supported (only \"file\")", cfg.DeadLetter.Type)
	}

	return nil
}

// resolveSecrets reads secret_env values from the environment. The resolved
// secret is stored back into SecretEnv so callers can use it directly as the
// secret value.
func resolveSecrets(cfg *Config) error {
	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		envKey := r.Signature.SecretEnv
		val := os.Getenv(envKey)
		if val == "" {
			return fmt.Errorf("route %q: environment variable %q is not set", r.Path, envKey)
		}
		r.Signature.SecretEnv = val // now holds the actual secret
	}
	return nil
}
