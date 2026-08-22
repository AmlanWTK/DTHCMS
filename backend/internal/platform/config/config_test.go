package config

import (
	"strings"
	"testing"
)

func TestLoadAppliesDefaults(t *testing.T) {
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
		t.Setenv("DTHCMS_ENV", "production")
		t.Setenv("DTHCMS_POSTGRES_URL", "postgres://user:strongpassword@db.internal:5432/dthcms?sslmode=require")
		t.Setenv("DTHCMS_BLOB_USE_SSL", "true")
		t.Setenv("DTHCMS_AI_TIER", "paid")
		t.Setenv("DTHCMS_AI_API_KEY", "key")
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
}

func TestLocalEnvironmentIsPermissive(t *testing.T) {
	// The production rules must not make local development painful; that is how
	// developers end up disabling checks.
	t.Setenv("DTHCMS_ENV", "local")
	t.Setenv("DTHCMS_AI_TIER", "mock")

	if _, err := Load("api", "test"); err != nil {
		t.Fatalf("local development configuration must load without ceremony: %v", err)
	}
}
