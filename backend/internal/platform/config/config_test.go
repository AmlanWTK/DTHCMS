package config

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// isolate removes every DTHCMS_* variable for the duration of one test.
//
// Without it these tests assert against whatever environment they happen to run in. CI
// sets DTHCMS_OTEL_ENABLED=false for the whole backend job - correct, since there is no
// collector there - and that silently broke the production test, which asserts that a
// valid production configuration loads while inheriting a setting production refuses.
//
// Setting a variable to empty rather than unsetting it is deliberate and sufficient:
// every getter in the loader treats an empty value as absent, and t.Setenv restores the
// previous value automatically when the test ends.
func isolate(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		if key, _, found := strings.Cut(entry, "="); found && strings.HasPrefix(key, "DTHCMS_") {
			t.Setenv(key, "")
		}
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	isolate(t)

	cfg, err := Load("api", "test")
	if err != nil {
		t.Fatalf("Load with no environment set should succeed for local development: %v", err)
	}

	if cfg.Env != EnvLocal {
		t.Errorf("Env = %q, want local", cfg.Env)
	}
	if cfg.AI.Tier != TierMock {
		t.Errorf("AI.Tier = %q, want mock — development must never call a real model", cfg.AI.Tier)
	}
	if cfg.HTTP.Addr == "" || cfg.Postgres.URL == "" || cfg.Redis.Addr == "" {
		t.Error("defaults must be complete enough to run against the local stack")
	}
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	isolate(t)

	t.Setenv("DTHCMS_ENV", "wonderland")
	t.Setenv("DTHCMS_LOG_LEVEL", "shouty")
	t.Setenv("DTHCMS_POSTGRES_MAX_CONNS", "not-a-number")
	t.Setenv("DTHCMS_HTTP_READ_TIMEOUT", "soon")

	_, err := Load("api", "test")
	if err == nil {
		t.Fatal("invalid configuration must prevent start-up")
	}
	if !IsInvalid(err) {
		t.Fatalf("error should be a configuration failure, got %T", err)
	}

	// One deployment attempt should reveal every problem, not the first one.
	msg := err.Error()
	for _, want := range []string{"DTHCMS_ENV", "DTHCMS_LOG_LEVEL", "DTHCMS_POSTGRES_MAX_CONNS", "DTHCMS_HTTP_READ_TIMEOUT"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message does not mention %s:\n%s", want, msg)
		}
	}
}

func TestErrorMessagesAreActionable(t *testing.T) {
	isolate(t)

	t.Setenv("DTHCMS_HTTP_READ_TIMEOUT", "soon")

	_, err := Load("api", "test")
	if err == nil {
		t.Fatal("expected failure")
	}
	// Someone fixing a deployment at speed needs to be told the accepted form.
	if !strings.Contains(err.Error(), "30s") {
		t.Errorf("duration error should show an example of the accepted format:\n%s", err)
	}
}

// The production rules are the ones that protect patients, so they are tested case by case.
func TestProductionRules(t *testing.T) {
	production := func(t *testing.T) {
		t.Helper()
		isolate(t)

		// Every setting a production rule reads is set here. A rule that reads a
		// variable this helper does not set makes the test depend on the machine it
		// runs on, which is how this suite passed locally and failed in CI.
		t.Setenv("DTHCMS_ENV", "production")
		t.Setenv("DTHCMS_POSTGRES_URL", "postgres://dthcms_app:strongpassword@db.internal:5432/dthcms?sslmode=require")
		t.Setenv("DTHCMS_POSTGRES_MIGRATION_URL", "postgres://dthcms_owner:otherpassword@db.internal:5432/dthcms?sslmode=require")
		t.Setenv("DTHCMS_POSTGRES_PROJECTOR_URL", "postgres://dthcms_projector:thirdpassword@db.internal:5432/dthcms?sslmode=require")
		t.Setenv("DTHCMS_BLOB_USE_SSL", "true")
		t.Setenv("DTHCMS_AI_TIER", "paid")
		t.Setenv("DTHCMS_AI_API_KEY", "key")
		t.Setenv("DTHCMS_OTEL_INSECURE", "false")
		t.Setenv("DTHCMS_OTEL_ENABLED", "true")
		t.Setenv("DTHCMS_SECRET_KEY_ID", "prod-1")
		t.Setenv("DTHCMS_SECRET_KEY", "c3Ryb25nLXN0cm9uZy1zdHJvbmctc3Ryb25nLXN0cm9uZy0wMQ==")
		t.Setenv("DTHCMS_AUDIT_SIGNING_SEED", "c3Ryb25nLWF1ZGl0LXNpZ25pbmctc2VlZC1mb3ItcHJvZC0x")
		t.Setenv("DTHCMS_IDENTIFIER_PEPPER", "c3Ryb25nLWlkZW50aWZpZXItcGVwcGVyLWZvci1wcm9kLTE=")
	}

	t.Run("valid production config loads", func(t *testing.T) {
		production(t)
		if _, err := Load("api", "test"); err != nil {
			t.Fatalf("a correct production configuration must load: %v", err)
		}
	})

	t.Run("refuses the local development secret key", func(t *testing.T) {
		// ADR-0012. The default key is in the repository; anything encrypted under it is
		// encrypted in name only.
		production(t)
		t.Setenv("DTHCMS_SECRET_KEY", LocalSecretKey)
		if _, err := Load("api", "test"); err == nil || !strings.Contains(err.Error(), "DTHCMS_SECRET_KEY") {
			t.Fatalf("the local secret key was accepted in production: %v", err)
		}
	})

	t.Run("refuses the local audit signing seed", func(t *testing.T) {
		// CP22. A signature made with a seed that is in the repository proves nothing.
		production(t)
		t.Setenv("DTHCMS_AUDIT_SIGNING_SEED", LocalAuditSeed)
		if _, err := Load("api", "test"); err == nil || !strings.Contains(err.Error(), "DTHCMS_AUDIT_SIGNING_SEED") {
			t.Fatalf("the local audit signing seed was accepted in production: %v", err)
		}
	})

	t.Run("refuses the local identifier pepper", func(t *testing.T) {
		// D-47. With the repository's pepper, an NID digest is a plain hash of a
		// ten-digit number — reversible by anyone with a laptop and a weekend.
		production(t)
		t.Setenv("DTHCMS_IDENTIFIER_PEPPER", LocalIdentifierPepper)
		if _, err := Load("api", "test"); err == nil || !strings.Contains(err.Error(), "DTHCMS_IDENTIFIER_PEPPER") {
			t.Fatalf("the local identifier pepper was accepted in production: %v", err)
		}
	})

	t.Run("free AI tier is refused", func(t *testing.T) {
		production(t)
		t.Setenv("DTHCMS_AI_TIER", "free")

		_, err := Load("api", "test")
		if err == nil {
			t.Fatal("the Gemini free tier must never be usable in production (ADR-0007)")
		}
		if !strings.Contains(err.Error(), "ADR-0007") {
			t.Errorf("the refusal should cite the decision it enforces:\n%s", err)
		}
	})

	t.Run("mock AI tier is refused", func(t *testing.T) {
		production(t)
		t.Setenv("DTHCMS_AI_TIER", "mock")

		if _, err := Load("api", "test"); err == nil {
			t.Fatal("a mock returning canned text must never answer a clinician")
		}
	})

	t.Run("paid tier without a key is refused", func(t *testing.T) {
		production(t)
		t.Setenv("DTHCMS_AI_API_KEY", "")

		if _, err := Load("api", "test"); err == nil {
			t.Fatal("paid tier without an API key would fail on first use instead of at start-up")
		}
	})

	t.Run("plaintext database connection is refused", func(t *testing.T) {
		production(t)
		t.Setenv("DTHCMS_POSTGRES_URL", "postgres://user:pass@db:5432/dthcms?sslmode=disable")

		if _, err := Load("api", "test"); err == nil {
			t.Fatal("patient data must not travel to the database unencrypted")
		}
	})

	t.Run("leftover local password is refused", func(t *testing.T) {
		production(t)
		t.Setenv("DTHCMS_POSTGRES_URL", "postgres://dthcms:dthcms_local_only@db:5432/dthcms?sslmode=require")

		if _, err := Load("api", "test"); err == nil {
			t.Fatal("the committed development password must never reach production")
		}
	})

	t.Run("unencrypted object storage is refused", func(t *testing.T) {
		production(t)
		t.Setenv("DTHCMS_BLOB_USE_SSL", "false")

		if _, err := Load("api", "test"); err == nil {
			t.Fatal("scanned patient records must not travel unencrypted")
		}
	})

	t.Run("plaintext telemetry is refused", func(t *testing.T) {
		production(t)
		t.Setenv("DTHCMS_OTEL_INSECURE", "true")

		if _, err := Load("api", "test"); err == nil {
			t.Fatal("spans carry route templates, timings and error text; they must not " +
				"travel in the clear")
		}
	})

	t.Run("telemetry cannot be switched off", func(t *testing.T) {
		production(t)
		t.Setenv("DTHCMS_OTEL_ENABLED", "false")

		if _, err := Load("api", "test"); err == nil {
			t.Fatal("a production incident with no traces is diagnosed by guessing")
		}
	})

	t.Run("migrating as the application role is refused", func(t *testing.T) {
		production(t)
		same := "postgres://dthcms_app:strongpassword@db.internal:5432/dthcms?sslmode=require"
		t.Setenv("DTHCMS_POSTGRES_URL", same)
		t.Setenv("DTHCMS_POSTGRES_MIGRATION_URL", same)

		_, err := Load("api", "test")
		if err == nil {
			t.Fatal("one connection for both roles gives every request handler the " +
				"privileges needed to make the ledger writable")
		}
		if !strings.Contains(err.Error(), "DTHCMS_POSTGRES_MIGRATION_URL") {
			t.Errorf("the refusal should name the setting at fault:\n%s", err)
		}
	})

	t.Run("projecting as the application role is refused", func(t *testing.T) {
		production(t)
		same := "postgres://dthcms_app:strongpassword@db.internal:5432/dthcms?sslmode=require"
		t.Setenv("DTHCMS_POSTGRES_URL", same)
		t.Setenv("DTHCMS_POSTGRES_PROJECTOR_URL", same)

		_, err := Load("projector", "test")
		if err == nil {
			t.Fatal("one connection for both roles lets the application write read models, " +
				"which core.assert_read_models_derived() exists to prevent")
		}
		if !strings.Contains(err.Error(), "DTHCMS_POSTGRES_PROJECTOR_URL") {
			t.Errorf("the refusal should name the setting at fault:\n%s", err)
		}
	})
}

func TestLocalEnvironmentIsPermissive(t *testing.T) {
	isolate(t)

	// The production rules must not make local development painful; that is how
	// developers end up disabling checks.
	t.Setenv("DTHCMS_ENV", "local")
	t.Setenv("DTHCMS_AI_TIER", "mock")

	if _, err := Load("api", "test"); err != nil {
		t.Fatalf("local development configuration must load without ceremony: %v", err)
	}
}

// The development secrets have to actually work, not merely be present.
//
// LocalSecretKey was 35 bytes for a year. Every consequence of that landed outside the tests:
// `go run ./cmd/api` on a fresh checkout died at start-up with "cannot build the secret key
// ring", and no local stack could serve a request until somebody set DTHCMS_SECRET_KEY by
// hand. Nothing caught it because the tests that need a key ring build their own, and the
// config tests asserted only that the string was non-empty — which it was.
//
// So this decodes them and checks the lengths the cryptography actually requires. It is the
// same lesson as the migrations that skipped silently and the suite that timed out: a check
// that confirms a value exists is not a check that the value is usable.
func TestTheLocalDevelopmentSecretsAreActuallyUsable(t *testing.T) {
	t.Parallel()

	for _, secret := range []struct {
		name  string
		value string
		want  int
	}{
		// AES-256, for sealing identifiers and TOTP seeds at rest.
		{"LocalSecretKey", LocalSecretKey, 32},
		// The pepper for the national-ID digest. Not rotatable, so its size is fixed too.
		{"LocalIdentifierPepper", LocalIdentifierPepper, 32},
		// An Ed25519 seed, which is 32 bytes by definition.
		{"LocalAuditSeed", LocalAuditSeed, 32},
	} {
		raw, err := base64.StdEncoding.DecodeString(secret.value)
		if err != nil {
			t.Errorf("%s is not valid base64: %v", secret.name, err)
			continue
		}
		if len(raw) != secret.want {
			t.Errorf("%s decodes to %d bytes, want %d. A local stack cannot start with this: "+
				"the API refuses at boot rather than serving with a key of the wrong size",
				secret.name, len(raw), secret.want)
		}
	}
}

// The default CORS origin has to be the port the web application actually binds.
//
// It was http://localhost:3000 while `pnpm --filter web dev` has always bound 3100, so a
// browser refused every request the web application made — and the API's own log showed a
// preflight answered 204 and then nothing, which reads as healthy. Nobody found it for as
// long as the two lived in separate languages with nothing comparing them.
//
// So this reads the port out of web/package.json. A Go test reaching into a JavaScript
// package's manifest is unusual and is the point: the bug lives precisely in the gap between
// the two, and a constant repeated on both sides of that gap is a constant that drifts. It is
// the same argument as the permission catalogue being compared against the database and the
// route table against the OpenAPI document — the check has to span the seam it is guarding.
func TestTheDefaultOriginMatchesTheWebApplicationsPort(t *testing.T) {
	// Not parallel: isolate() below uses t.Setenv, which Go forbids in a parallel test
	// because the environment is process-wide. Worth a line rather than a silent omission —
	// written parallel, this passed alone and panicked in the full suite, because alone there
	// were no DTHCMS_* variables for isolate() to unset and so t.Setenv was never reached.

	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "web", "package.json"))
	if err != nil {
		t.Skipf("web/package.json is not readable from here (%v); nothing to compare against", err)
	}

	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("web/package.json does not parse: %v", err)
	}

	dev := manifest.Scripts["dev"]
	port := regexp.MustCompile(`--port\s+(\d+)`).FindStringSubmatch(dev)
	if port == nil {
		t.Skipf("the web dev script names no port (%q); nothing to compare against", dev)
	}

	isolate(t)
	cfg, err := Load("api", "test")
	if err != nil {
		t.Fatalf("loading the defaults: %v", err)
	}

	want := "http://localhost:" + port[1]
	for _, origin := range cfg.HTTP.AllowedOrigins {
		if strings.TrimSpace(origin) == want {
			return
		}
	}
	t.Fatalf("the default allowed origins are %v, which does not include %s — the port "+
		"`pnpm --filter web dev` binds. A browser will refuse every request the web "+
		"application makes, and the API's log will show only a preflight answered 204",
		cfg.HTTP.AllowedOrigins, want)
}
