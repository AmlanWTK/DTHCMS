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
	Secrets  SecretsConfig
	Audit    AuditConfig

	Log       LogConfig
	Telemetry TelemetryConfig
}

// SecretsConfig is the key under which small secrets — TOTP seeds, later device keys — are
// encrypted before they reach the database (ADR-0012).
//
// A local default exists so that `make up` works with nothing configured. It is refused
// outside local and test, because a key that is in the repository is not a key.
type SecretsConfig struct {
	// KeyID names the key in every ciphertext, so it can be rotated (secretbox.Ring).
	KeyID string
	// Key is 32 bytes, base64. Generate one with `openssl rand -base64 32`.
	Key string
	// PreviousKeys are older keys, as "id=base64" pairs, still able to open what they
	// sealed while new writes move to Key.
	PreviousKeys []string
	// IdentifierPepper keys the digest that finds a patient by their national ID (CP28,
	// D-47). 32 bytes, base64.
	//
	// Separate from Key and *not rotatable*, which is the whole difficulty: the digests it
	// produces are the duplicate-detection index, so changing it would silently stop every
	// existing patient from matching their own number. It is a secret to be kept, not a key
	// to be rolled — and if it is ever compromised, re-peppering is a migration that reads
	// and re-seals every identifier, which is a planned outage rather than a config change.
	IdentifierPepper string
}

// LocalSecretKey is the development key. Recognisable on purpose.
const LocalSecretKey = "bG9jYWwtb25seS1sb2NhbC1vbmx5LWxvY2FsLW9ubHktMDA="

// AuditConfig is the key that signs audit exports (CP22, ed25519). The seed is 32 bytes,
// base64; the public half is served by the API and printed in the operations guide.
type AuditConfig struct {
	SigningKeyID string
	SigningSeed  string
}

// LocalIdentifierPepper is the development pepper. Refused outside local and test: a
// pepper that is in the repository turns every NID digest back into a plain hash of a
// ten-digit number, which is reversible by anyone with a laptop and a weekend.
const LocalIdentifierPepper = "bG9jYWwtb25seS1pZGVudGlmaWVyLXBlcHBlci0wMDA="

// LocalAuditSeed is the development signing seed. Refused outside local and test, like
// the secret key: a signature anyone can forge from the repository proves nothing.
const LocalAuditSeed = "bG9jYWwtb25seS1hdWRpdC1zaWduaW5nLXNlZWQtMDA="

// TelemetryConfig configures OpenTelemetry tracing and metrics.
type TelemetryConfig struct {
	// Enabled turns tracing and metrics off entirely. Off is a legitimate state for a
	// one-shot CLI run; it is not a legitimate state for a serving process, which is
	// why production requires it on.
	Enabled bool
	// Endpoint is an OTLP/HTTP collector as host:port, with no scheme.
	Endpoint string
	// Insecure sends telemetry over plain HTTP. Refused in production: a span carries
	// route templates, timings and error text, which is a map of the system.
	Insecure bool
	// SampleRatio is the fraction of traces recorded, 0 to 1.
	SampleRatio float64
	// MetricInterval is how often metrics are pushed.
	MetricInterval time.Duration
	// ExportTimeout bounds one export attempt.
	ExportTimeout time.Duration
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
	// URL is the application's connection. It uses a role that may append to the
	// ledger and read from it, and may not modify it (migration 00002).
	URL string
	// MigrationURL is used only by cmd/migrate. Migrations create schemas, tables and
	// grants; the application role can do none of those things, and giving it those
	// privileges so that one binary can migrate would hand them to every request
	// handler as well.
	MigrationURL string
	// ProjectorURL is used only by cmd/projector. Read models are written by
	// dthcms_projector and by nothing else (migration 00002, and
	// core.assert_read_models_derived); a projector that connected as the application
	// role could not write a single one, and granting the application the privilege so
	// that one binary can project would hand it to every request handler as well.
	ProjectorURL string
	// DevRolePassword is the password given to the local login roles created by
	// `migrate dev-roles`. Never used outside local and test environments.
	DevRolePassword string

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
			//
			// The default user is dthcms_app_local, not the database owner. The
			// application runs locally with the privileges it has in production, so an
			// UPDATE against the ledger fails on the machine where it was written
			// rather than in staging a week later.
			//
			// `make migrate` creates that role. Run it once after pulling CP06.
			URL:             l.str("DTHCMS_POSTGRES_URL", "postgres://dthcms_app_local:dthcms_local_only@127.0.0.1:5433/dthcms?sslmode=disable"),
			MigrationURL:    l.str("DTHCMS_POSTGRES_MIGRATION_URL", "postgres://dthcms:dthcms_local_only@127.0.0.1:5433/dthcms?sslmode=disable"),
			ProjectorURL:    l.str("DTHCMS_POSTGRES_PROJECTOR_URL", "postgres://dthcms_projector_local:dthcms_local_only@127.0.0.1:5433/dthcms?sslmode=disable"),
			DevRolePassword: l.str("DTHCMS_POSTGRES_DEV_ROLE_PASSWORD", "dthcms_local_only"),
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

		Secrets: SecretsConfig{
			KeyID:            l.str("DTHCMS_SECRET_KEY_ID", "local-1"),
			Key:              l.str("DTHCMS_SECRET_KEY", LocalSecretKey),
			PreviousKeys:     l.list("DTHCMS_SECRET_PREVIOUS_KEYS", ""),
			IdentifierPepper: l.str("DTHCMS_IDENTIFIER_PEPPER", LocalIdentifierPepper),
		},
		Audit: AuditConfig{
			SigningKeyID: l.str("DTHCMS_AUDIT_SIGNING_KEY_ID", "audit-local-1"),
			SigningSeed:  l.str("DTHCMS_AUDIT_SIGNING_SEED", LocalAuditSeed),
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

		Telemetry: TelemetryConfig{
			Enabled:  l.boolVal("DTHCMS_OTEL_ENABLED", true),
			Endpoint: l.str("DTHCMS_OTEL_ENDPOINT", "127.0.0.1:4318"),
			Insecure: l.boolVal("DTHCMS_OTEL_INSECURE", true),
			// Every trace, locally. Sampling exists to control cost and volume, and a
			// developer has neither problem — but does have the problem of the one
			// request they care about being the one that was not sampled.
			SampleRatio:    l.float("DTHCMS_OTEL_SAMPLE_RATIO", 1.0),
			MetricInterval: l.duration("DTHCMS_OTEL_METRIC_INTERVAL", 15*time.Second),
			ExportTimeout:  l.duration("DTHCMS_OTEL_EXPORT_TIMEOUT", 10*time.Second),
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

	if c.Telemetry.Enabled {
		if c.Telemetry.Endpoint == "" {
			problems = append(problems, "DTHCMS_OTEL_ENDPOINT is required when telemetry is enabled")
		}
		if strings.Contains(c.Telemetry.Endpoint, "://") {
			problems = append(problems, fmt.Sprintf(
				"DTHCMS_OTEL_ENDPOINT=%q must be host:port with no scheme (for example 127.0.0.1:4318)",
				c.Telemetry.Endpoint))
		}
		if c.Telemetry.SampleRatio < 0 || c.Telemetry.SampleRatio > 1 {
			problems = append(problems, fmt.Sprintf(
				"DTHCMS_OTEL_SAMPLE_RATIO=%v must be between 0 and 1", c.Telemetry.SampleRatio))
		}
	}

	switch c.AI.Tier {
	case TierFree, TierPaid, TierMock:
	default:
		problems = append(problems, fmt.Sprintf(
			"DTHCMS_AI_TIER=%q is not valid (free, paid, mock)", c.AI.Tier))
	}

	if c.Secrets.KeyID == "" || c.Secrets.Key == "" {
		problems = append(problems, "DTHCMS_SECRET_KEY_ID and DTHCMS_SECRET_KEY are required")
	}
	if c.Env != EnvLocal && c.Env != EnvTest && c.Secrets.Key == LocalSecretKey {
		problems = append(problems, "DTHCMS_SECRET_KEY is the local development key: "+
			"TOTP seeds would be encrypted under a key that is in the repository (ADR-0012)")
	}
	if c.Secrets.IdentifierPepper == "" {
		problems = append(problems, "DTHCMS_IDENTIFIER_PEPPER is required")
	}
	if c.Env != EnvLocal && c.Env != EnvTest && c.Secrets.IdentifierPepper == LocalIdentifierPepper {
		problems = append(problems, "DTHCMS_IDENTIFIER_PEPPER is the local development pepper: "+
			"national ID digests would be a plain hash of a ten-digit number (D-47)")
	}
	if c.Audit.SigningKeyID == "" || c.Audit.SigningSeed == "" {
		problems = append(problems, "DTHCMS_AUDIT_SIGNING_KEY_ID and DTHCMS_AUDIT_SIGNING_SEED are required")
	}
	if c.Env != EnvLocal && c.Env != EnvTest && c.Audit.SigningSeed == LocalAuditSeed {
		problems = append(problems, "DTHCMS_AUDIT_SIGNING_SEED is the local development seed: "+
			"audit exports would be signed with a key that is in the repository")
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
		if c.Postgres.MigrationURL == c.Postgres.URL {
			// If they are the same, the application is running with the privileges
			// needed to create schemas, grant roles and drop tables — which is to say,
			// the privileges needed to make the ledger writable.
			problems = append(problems, "DTHCMS_POSTGRES_MIGRATION_URL must differ from "+
				"DTHCMS_POSTGRES_URL: migrations need privileges the application must not have")
		}
		if strings.Contains(c.Postgres.MigrationURL, "dthcms_local_only") {
			problems = append(problems, "DTHCMS_POSTGRES_MIGRATION_URL still contains the local development password")
		}
		if strings.Contains(c.Postgres.MigrationURL, "sslmode=disable") {
			problems = append(problems, "DTHCMS_POSTGRES_MIGRATION_URL must not disable TLS in production")
		}
		if c.Postgres.ProjectorURL == c.Postgres.URL {
			// If they are the same, the application can write to the read models, and
			// core.assert_read_models_derived() will refuse to let the service start —
			// correctly. Saying so here names the cause rather than the symptom.
			problems = append(problems, "DTHCMS_POSTGRES_PROJECTOR_URL must differ from "+
				"DTHCMS_POSTGRES_URL: only the projector may write read models")
		}
		if strings.Contains(c.Postgres.ProjectorURL, "dthcms_local_only") {
			problems = append(problems, "DTHCMS_POSTGRES_PROJECTOR_URL still contains the local development password")
		}
		if strings.Contains(c.Postgres.ProjectorURL, "sslmode=disable") {
			problems = append(problems, "DTHCMS_POSTGRES_PROJECTOR_URL must not disable TLS in production")
		}
		if !c.Blob.UseSSL {
			problems = append(problems, "DTHCMS_BLOB_USE_SSL must be true in production")
		}
		if strings.EqualFold(c.Log.Format, "text") {
			problems = append(problems, "DTHCMS_LOG_FORMAT=text is not permitted in production; use json")
		}
		if !c.Telemetry.Enabled {
			// A production incident without traces is diagnosed by guessing. The point
			// of building observability before the first clinical feature is that it is
			// never absent when it is needed.
			problems = append(problems, "DTHCMS_OTEL_ENABLED=false is not permitted in production: "+
				"a service with no traces cannot be diagnosed during an incident")
		}
		if c.Telemetry.Enabled && c.Telemetry.Insecure {
			problems = append(problems, "DTHCMS_OTEL_INSECURE=true is not permitted in production: "+
				"spans carry route templates, timings and error text")
		}
		if c.Telemetry.Enabled && c.Telemetry.SampleRatio == 0 {
			problems = append(problems, "DTHCMS_OTEL_SAMPLE_RATIO=0 records nothing; "+
				"set DTHCMS_OTEL_ENABLED=false if that is deliberate")
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

func (l *loader) float(key string, def float64) float64 {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		l.problems = append(l.problems, fmt.Sprintf("%s=%q is not a number", key, raw))
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
