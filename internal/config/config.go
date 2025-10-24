package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig              `yaml:"server"`
	Cache     CacheConfig               `yaml:"cache"`
	Policies  []PolicyConfig            `yaml:"policies"`
	Upstreams map[string]UpstreamConfig `yaml:"upstreams"`
	Admin     AdminConfig               `yaml:"admin"`
	Logging   LoggingConfig             `yaml:"logging"`
	Proxy     ProxyConfig               `yaml:"proxy,omitempty"` // Egress proxy for upstream connections
	Auth      AuthConfig                `yaml:"auth,omitempty"`  // Ingress authentication
}

type ServerConfig struct {
	Listeners []ListenerConfig `yaml:"listeners"`
}

type ListenerConfig struct {
	Addr string     `yaml:"addr"`
	TLS  *TLSConfig `yaml:"tls,omitempty"`
}

type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

type CacheConfig struct {
	Dir               string        `yaml:"dir"`
	MaxSizeBytes      int64         `yaml:"max_size_bytes"`
	InactiveTTL       time.Duration `yaml:"inactive_ttl"`
	RevalidateETag    bool          `yaml:"revalidate_etag"`
	RevalidateLastMod bool          `yaml:"revalidate_last_modified"`
	LockTimeout       time.Duration `yaml:"lock_timeout"`
}

type PolicyConfig struct {
	Name                      string        `yaml:"name"`
	Regex                     string        `yaml:"regex"`
	CacheTTL                  time.Duration `yaml:"cache_ttl"`
	AllowStaleWhileRevalidate bool          `yaml:"allow_stale_while_revalidate"`

	// Compiled regex (set during validation)
	CompiledRegex *regexp.Regexp `yaml:"-"`
}

type UpstreamConfig struct {
	BaseURL    string            `yaml:"base_url"`
	PathPrefix string            `yaml:"path_prefix"`
	Headers    map[string]string `yaml:"headers,omitempty"` // Custom headers (e.g., Authorization)
}

type AdminConfig struct {
	EnablePurgeAPI bool   `yaml:"enable_purge_api"`
	Token          string `yaml:"token"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
	JSON  bool   `yaml:"json"`
}

// ProxyConfig configures egress proxy for upstream connections
type ProxyConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Type     string `yaml:"type"`     // "http" or "socks5"
	URL      string `yaml:"url"`      // e.g., "http://proxy:8080" or "socks5://proxy:1080"
	Username string `yaml:"username"` // Optional
	Password string `yaml:"password"` // Optional
}

// AuthConfig configures ingress authentication for client requests
type AuthConfig struct {
	Enabled bool   `yaml:"enabled"`
	Type    string `yaml:"type"` // "basic", "bearer", or "token"
	// For basic auth
	Users map[string]string `yaml:"users,omitempty"` // username -> password
	// For bearer/token auth
	Tokens []string `yaml:"tokens,omitempty"` // List of valid tokens
}

// UnmarshalYAML custom unmarshaler for duration fields and size units
func (c *CacheConfig) UnmarshalYAML(node *yaml.Node) error {
	type rawConfig CacheConfig
	raw := rawConfig{
		RevalidateETag:    true,
		RevalidateLastMod: true,
	}

	// Create a temporary struct to hold string durations and sizes
	var temp struct {
		Dir               string `yaml:"dir"`
		MaxSizeBytes      string `yaml:"max_size_bytes"` // Accept string for size units
		InactiveTTL       string `yaml:"inactive_ttl"`
		RevalidateETag    bool   `yaml:"revalidate_etag"`
		RevalidateLastMod bool   `yaml:"revalidate_last_modified"`
		LockTimeout       string `yaml:"lock_timeout"`
	}

	if err := node.Decode(&temp); err != nil {
		return err
	}

	raw.Dir = temp.Dir
	raw.RevalidateETag = temp.RevalidateETag
	raw.RevalidateLastMod = temp.RevalidateLastMod

	// Parse max_size_bytes with size units (e.g., "200GB", "1TB")
	if temp.MaxSizeBytes != "" {
		size, err := parseSize(temp.MaxSizeBytes)
		if err != nil {
			return fmt.Errorf("invalid max_size_bytes: %w", err)
		}
		raw.MaxSizeBytes = size
	}

	if temp.InactiveTTL != "" {
		dur, err := parseDuration(temp.InactiveTTL)
		if err != nil {
			return fmt.Errorf("invalid inactive_ttl: %w", err)
		}
		raw.InactiveTTL = dur
	}

	if temp.LockTimeout != "" {
		dur, err := parseDuration(temp.LockTimeout)
		if err != nil {
			return fmt.Errorf("invalid lock_timeout: %w", err)
		}
		raw.LockTimeout = dur
	}

	*c = CacheConfig(raw)
	return nil
}

func (p *PolicyConfig) UnmarshalYAML(node *yaml.Node) error {
	type rawPolicy PolicyConfig
	raw := rawPolicy{}

	var temp struct {
		Name                      string `yaml:"name"`
		Regex                     string `yaml:"regex"`
		CacheTTL                  string `yaml:"cache_ttl"`
		AllowStaleWhileRevalidate bool   `yaml:"allow_stale_while_revalidate"`
	}

	if err := node.Decode(&temp); err != nil {
		return err
	}

	raw.Name = temp.Name
	raw.Regex = temp.Regex
	raw.AllowStaleWhileRevalidate = temp.AllowStaleWhileRevalidate

	if temp.CacheTTL != "" {
		dur, err := parseDuration(temp.CacheTTL)
		if err != nil {
			return fmt.Errorf("invalid cache_ttl: %w", err)
		}
		raw.CacheTTL = dur
	}

	*p = PolicyConfig(raw)
	return nil
}

// parseDuration extends time.ParseDuration to support days (d)
func parseDuration(s string) (time.Duration, error) {
	// Try standard parsing first
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}

	// Check for day suffix
	if len(s) > 1 && s[len(s)-1] == 'd' {
		days := s[:len(s)-1]
		var d int64
		if _, err := fmt.Sscanf(days, "%d", &d); err != nil {
			return 0, fmt.Errorf("invalid duration format: %s", s)
		}
		return time.Duration(d) * 24 * time.Hour, nil
	}

	return 0, fmt.Errorf("invalid duration format: %s", s)
}

// parseSize parses size strings with units (e.g., "200GB", "1TB", "512MB")
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)

	// Try parsing as raw number first (backwards compatibility)
	var rawSize int64
	if _, err := fmt.Sscanf(s, "%d", &rawSize); err == nil {
		// Check if there's no unit (pure number)
		if strings.IndexAny(s, "KkMmGgTtPpBb") == -1 {
			return rawSize, nil
		}
	}

	// Parse with unit
	s = strings.ToUpper(s)
	var value float64
	var unit string

	n, err := fmt.Sscanf(s, "%f%s", &value, &unit)
	if err != nil || n != 2 {
		return 0, fmt.Errorf("invalid size format: %s (expected format: '200GB', '1.5TB', etc.)", s)
	}

	// Convert to bytes based on unit
	var multiplier int64
	switch unit {
	case "B", "BYTES":
		multiplier = 1
	case "KB", "K":
		multiplier = 1024
	case "MB", "M":
		multiplier = 1024 * 1024
	case "GB", "G":
		multiplier = 1024 * 1024 * 1024
	case "TB", "T":
		multiplier = 1024 * 1024 * 1024 * 1024
	case "PB", "P":
		multiplier = 1024 * 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("unknown size unit: %s (use B, KB, MB, GB, TB, PB)", unit)
	}

	return int64(value * float64(multiplier)), nil
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) Validate() error {
	// Set default listener if none specified
	if len(c.Server.Listeners) == 0 {
		c.Server.Listeners = []ListenerConfig{{Addr: ":8080"}}
	}

	if c.Cache.Dir == "" {
		return fmt.Errorf("cache.dir is required")
	}

	if c.Cache.MaxSizeBytes <= 0 {
		return fmt.Errorf("cache.max_size_bytes must be positive")
	}

	if len(c.Policies) == 0 {
		return fmt.Errorf("at least one policy is required")
	}

	// Compile regex patterns
	for i := range c.Policies {
		re, err := regexp.Compile(c.Policies[i].Regex)
		if err != nil {
			return fmt.Errorf("policy %s: invalid regex: %w", c.Policies[i].Name, err)
		}
		c.Policies[i].CompiledRegex = re
	}

	if len(c.Upstreams) == 0 {
		return fmt.Errorf("at least one upstream is required")
	}

	return nil
}

func (c *Config) MatchPolicy(path string) *PolicyConfig {
	for i := range c.Policies {
		if c.Policies[i].CompiledRegex.MatchString(path) {
			return &c.Policies[i]
		}
	}
	return nil
}

// MatchUpstream matches an upstream by path, considering path_prefix
func (c *Config) MatchUpstream(path string) (string, *UpstreamConfig, string) {
	for name, upstream := range c.Upstreams {
		prefix := upstream.PathPrefix
		if prefix == "" {
			prefix = "/" + name + "/"
		}

		// Ensure prefix starts with / and ends with /
		if prefix[0] != '/' {
			prefix = "/" + prefix
		}
		if prefix[len(prefix)-1] != '/' {
			prefix = prefix + "/"
		}

		// Check if path matches this upstream
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			rest := path[len(prefix):]
			return name, &upstream, rest
		}
	}
	return "", nil, ""
}
