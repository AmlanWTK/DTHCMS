package config

import (
	"os"
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
		t.Setenv("DTHCMS_BLOB_USE_SSL", "true")
		t.Setenv("DTHCMS_AI_TIER", "paid")
		t.Setenv("DTHCMS_AI_API_KEY", "key")
		t.Setenv("DTHCMS_OTEL_INSECURE", "false")
		t.Setenv("DTHCMS_OTEL_ENABLED", "true")
	}

	t.Run("valid production config loads", func(t *testing.T) {
		production(t)
		if _, err := Load("api", "test"); err != nil {
			t.Fatalf("a correct production configuration must load: %v", err)
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
