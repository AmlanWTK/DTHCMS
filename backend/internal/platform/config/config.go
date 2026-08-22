// Package config loads and validates every setting the application needs.
//
// The governing rule: a misconfigured process refuses to start. Discovering a wrong
// setting at deploy time costs minutes; discovering it at 11:40 on a clinic morning,
// through a failure nobody can explain, costs a great deal more.
//
// Load reports every problem it finds rather than stopping at the first, so a bad
// deployment is fixed in one pass instead of five.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment names a deployment.
type Environment string

const (
	EnvLocal      Environment = "local"
	EnvTest       Environment = "test"
	EnvDev        Environment = "dev"
	EnvStaging    Environment = "staging"
	EnvProduction Environment = "production"
)

// IsProduction reports whether real patient data may be present.
func (e Environment) IsProduction() bool { return e == EnvProduction }

// AITier records which Gemini tier credentials belong to (ADR-0007).
type AITier string

const (
	// TierFree may only ever see synthetic data. Google's terms permit training on and
	// human review of content submitted to the free tier.
	TierFree AITier = "free"
	// TierPaid covers paid Gemini or Vertex AI, where no training occurs.
	TierPaid AITier = "paid"
	// TierMock is the local stand-in; no model is contacted at all.
	TierMock AITier = "mock"
)

// Config is the complete, validated configuration.
type Config struct {
	Env     Environment
	Service string
	Version string

	HTTP     HTTPConfig
	Postgres PostgresConfig
	Redis    RedisConfig
	Blob     BlobConfig
	AI       AIConfig

	Log LogConfig
}

// HTTPConfig configures the HTTP server.
type HTTPConfig struct {
	Addr            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	MaxBodyBytes    int64
	AllowedOrigins  []string
}

// PostgresConfig configures the database pool.
type PostgresConfig struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	ConnectTimeout  time.Duration
}

// RedisConfig configures the cache and pub/sub client.
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

// BlobConfig configures object storage.
type BlobConfig struct {
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
	UseSSL    bool
	Buckets   map[string]string
}

// AIConfig configures access to the model provider.
type AIConfig struct {
	BaseURL string
	APIKey  string
	Tier    AITier
	Model   string
	Timeout time.Duration
}

// LogConfig configures the logger.
type LogConfig struct {
	Level  string
	Format string
}

// Load reads configuration from the environment for the named service.
func Load(service, version string) (*Config, error) {
	l := &loader{}

	cfg := &Config{
		Env:     Environment(l.str("DTHCMS_ENV", string(EnvLocal))),
		Service: service,
		Version: version,

		HTTP: HTTPConfig{
			Addr:            l.str("DTHCMS_HTTP_ADDR", ":8080"),
			ReadTimeout:     l.duration("DTHCMS_HTTP_READ_TIMEOUT", 15*time.Second),
			WriteTimeout:    l.duration("DTHCMS_HTTP_WRITE_TIMEOUT", 30*time.Second),
			IdleTimeout:     l.duration("DTHCMS_HTTP_IDLE_TIMEOUT", 90*time.Second),
			ShutdownTimeout: l.duration("DTHCMS_HTTP_SHUTDOWN_TIMEOUT", 20*time.Second),
			MaxBodyBytes:    l.bytes("DTHCMS_HTTP_MAX_BODY_BYTES", 4<<20),
			AllowedOrigins:  l.list("DTHCMS_HTTP_ALLOWED_ORIGINS", "http://localhost:3000"),
		},

		Postgres: PostgresConfig{
			// Port 5433, and 127.0.0.1 rather than localhost, both deliberately.
			// A PostgreSQL installed natively on the host commonly holds 5432 and wins
			// it, so a client reaches that server instead of the container and fails
			// authentication with credentials that are entirely correct. Publishing the
			// container elsewhere removes the ambiguity rather than papering over it.
			URL:             l.str("DTHCMS_POSTGRES_URL", "postgres://dthcms:dthcms_local_only@127.0.0.1:5433/dthcms?sslmode=disable"),
			MaxConns:        int32(l.intVal("DTHCMS_POSTGRES_MAX_CONNS", 10)),
			MinConns:        int32(l.intVal("DTHCMS_POSTGRES_MIN_CONNS", 2)),
			MaxConnLifetime: l.duration("DTHCMS_POSTGRES_MAX_CONN_LIFETIME", time.Hour),
			ConnectTimeout:  l.duration("DTHCMS_POSTGRES_CONNECT_TIMEOUT", 5*time.Second),
		},

		Redis: RedisConfig{
			// 6380 for the same reason Postgres uses 5433: a Redis-compatible server
			// installed on the host (Memurai, for instance) answers on 6379 and is
			// indistinguishable from ours until something silently reads the wrong data.
			Addr:     l.str("DTHCMS_REDIS_ADDR", "127.0.0.1:6380"),
			Password: l.str("DTHCMS_REDIS_PASSWORD", ""),
			DB:       l.intVal("DTHCMS_REDIS_DB", 0),
		},

		Blob: BlobConfig{
			Endpoint:  l.str("DTHCMS_BLOB_ENDPOINT", "127.0.0.1:9000"),
			Region:    l.str("DTHCMS_BLOB_REGION", "us-east-1"),
			AccessKey: l.str("DTHCMS_BLOB_ACCESS_KEY", "dthcms"),
			SecretKey: l.str("DTHCMS_BLOB_SECRET_KEY", "dthcms_local_only"),
			UseSSL:    l.boolVal("DTHCMS_BLOB_USE_SSL", false),
			Buckets: map[string]string{
				"identifier": l.str("DTHCMS_BLOB_BUCKET_IDENTIFIER", "dthcms-identifier"),
				"document":   l.str("DTHCMS_BLOB_BUCKET_DOCUMENT", "dthcms-document"),
				"derived":    l.str("DTHCMS_BLOB_BUCKET_DERIVED", "dthcms-derived"),
			},
		},

		AI: AIConfig{
			BaseURL: l.str("DTHCMS_AI_BASE_URL", "http://127.0.0.1:8090"),
			APIKey:  l.str("DTHCMS_AI_API_KEY", ""),
			Tier:    AITier(l.str("DTHCMS_AI_TIER", string(TierMock))),
			Model:   l.str("DTHCMS_AI_MODEL", "mock"),
			Timeout: l.duration("DTHCMS_AI_TIMEOUT", 60*time.Second),
		},

		Log: LogConfig{
			Level:  l.str("DTHCMS_LOG_LEVEL", "info"),
			Format: l.str("DTHCMS_LOG_FORMAT", "json"),
		},
	}

	problems := append(l.problems, cfg.validate()...)
	if len(problems) > 0 {
		return nil, &Invalid{Problems: problems}
	}
	return cfg, nil
}

// validate enforces the rules that cannot be expressed by parsing alone.
func (c *Config) validate() []string {
	var problems []string

	switch c.Env {
	case EnvLocal, EnvTest, EnvDev, EnvStaging, EnvProduction:
	default:
		problems = append(problems, fmt.Sprintf(
			"DTHCMS_ENV=%q is not a known environment (local, test, dev, staging, production)", c.Env))
	}

	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "warning", "error":
	default:
		problems = append(problems, fmt.Sprintf("DTHCMS_LOG_LEVEL=%q is not a level", c.Log.Level))
	}

	if c.Postgres.URL == "" {
		problems = append(problems, "DTHCMS_POSTGRES_URL is required")
	}
	if c.Postgres.MaxConns < 1 {
		problems = append(problems, "DTHCMS_POSTGRES_MAX_CONNS must be at least 1")
	}
	if c.Postgres.MinConns > c.Postgres.MaxConns {
		problems = append(problems, "DTHCMS_POSTGRES_MIN_CONNS cannot exceed DTHCMS_POSTGRES_MAX_CONNS")
	}
	if c.Redis.Addr == "" {
		problems = append(problems, "DTHCMS_REDIS_ADDR is required")
	}
	if c.HTTP.MaxBodyBytes < 1024 {
		problems = append(problems, "DTHCMS_HTTP_MAX_BODY_BYTES is implausibly small")
	}

	switch c.AI.Tier {
	case TierFree, TierPaid, TierMock:
	default:
		problems = append(problems, fmt.Sprintf(
			"DTHCMS_AI_TIER=%q is not valid (free, paid, mock)", c.AI.Tier))
	}

	if c.Env.IsProduction() {
		// ADR-0007. Google's terms permit training on, and human review of, content
		// submitted to the Gemini free tier. Patient data may therefore never be sent
		// on free-tier credentials, and a mock must never answer a clinician.
		if c.AI.Tier == TierFree {
			problems = append(problems, "DTHCMS_AI_TIER=free is not permitted in production: "+
				"the Gemini free tier may be trained on and read by human reviewers (ADR-0007)")
		}
		if c.AI.Tier == TierMock {
			problems = append(problems, "DTHCMS_AI_TIER=mock is not permitted in production: "+
				"the mock returns canned text and would silently replace clinical AI")
		}
		if c.AI.Tier == TierPaid && c.AI.APIKey == "" {
			problems = append(problems, "DTHCMS_AI_API_KEY is required when DTHCMS_AI_TIER=paid")
		}
		if strings.Contains(c.Postgres.URL, "sslmode=disable") {
			problems = append(problems, "DTHCMS_POSTGRES_URL must not disable TLS in production")
		}
		if strings.Contains(c.Postgres.URL, "dthcms_local_only") {
			problems = append(problems, "DTHCMS_POSTGRES_URL still contains the local development password")
		}
		if !c.Blob.UseSSL {
			problems = append(problems, "DTHCMS_BLOB_USE_SSL must be true in production")
		}
		if strings.EqualFold(c.Log.Format, "text") {
			problems = append(problems, "DTHCMS_LOG_FORMAT=text is not permitted in production; use json")
		}
	}

	return problems
}

// Invalid reports every configuration problem at once.
type Invalid struct{ Problems []string }

func (e *Invalid) Error() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("configuration is invalid (%d problem(s)):", len(e.Problems)))
	for _, p := range e.Problems {
		b.WriteString("\n  - ")
		b.WriteString(p)
	}
	return b.String()
}

// IsInvalid reports whether err is a configuration validation failure.
func IsInvalid(err error) bool {
	var invalid *Invalid
	return errors.As(err, &invalid)
}

// loader reads environment variables and accumulates parse failures.
type loader struct{ problems []string }

func (l *loader) str(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func (l *loader) intVal(key string, def int) int {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		l.problems = append(l.problems, fmt.Sprintf("%s=%q is not a whole number", key, raw))
		return def
	}
	return v
}

func (l *loader) bytes(key string, def int64) int64 {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		l.problems = append(l.problems, fmt.Sprintf("%s=%q is not a byte count", key, raw))
		return def
	}
	return v
}

func (l *loader) boolVal(key string, def bool) bool {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		l.problems = append(l.problems, fmt.Sprintf("%s=%q is not true or false", key, raw))
		return def
	}
	return v
}

func (l *loader) duration(key string, def time.Duration) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		l.problems = append(l.problems, fmt.Sprintf(
			"%s=%q is not a duration (use forms like 30s, 5m, 1h)", key, raw))
		return def
	}
	return v
}

func (l *loader) list(key, def string) []string {
	raw := l.str(key, def)
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
