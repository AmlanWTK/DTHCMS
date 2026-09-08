// Command devseed creates the people a local stack needs before anybody can sign in.
//
// # Why this exists, and why it is late
//
// Sixty-five checkpoints of this system were built, tested and documented before anyone
// noticed that a fresh local stack has **no way to log in**. The migrations create the
// facility, the roles and the permission catalogue, but not a single user — correctly, since
// a migration that shipped a known password would eventually run somewhere real. The
// integration tests never noticed because each one inserts the people it needs; the web
// end-to-end tests never noticed because they mock the API. So the gap sat exactly where no
// test looks: between a database that is right and a person who wants to use it.
//
// That is worth stating plainly rather than fixing quietly. A system nobody has signed into
// is a system whose defect count is unknown, however green its suite is, and this command
// exists so that the first person to find those defects is Dr. Nahid on his own laptop
// rather than an operator on a clinic morning.
//
// # What it will not do
//
// It refuses outright when the environment is production, and it refuses when the database
// already holds users it did not create. The first guard is the obvious one. The second
// matters more: the failure this is guarding is not "somebody runs it in production" — they
// will not — but "somebody points a local DTHCMS_POSTGRES_URL at a shared database during a
// demo", at which point a known password on a real account is the whole of the breach.
//
// The passwords are printed, are the same every time, and are meant to be. A local seed that
// generated secrets would be a local seed whose output somebody pastes into a chat window.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/pwhash"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
)

// Password is the same for every seeded account, and long enough to pass the policy.
//
// One password rather than five: the point of this command is to remove an obstacle between
// a developer and the running system, and five passwords to keep track of is a smaller
// obstacle of the same kind.
const Password = "local development only"

// person is one seeded account.
//
// One per station that has a screen, because the interesting bugs in a role-scoped system are
// the ones you only see by signing in as somebody who cannot do everything. Signing in as the
// administrator and finding that everything works tells you very little.
var people = []struct {
	code, nameEN, nameBN, role, note string
}{
	{"ADM01", "Local Administrator", "স্থানীয় প্রশাসক", "ADMIN",
		"users, devices, the audit trail, the job queue"},
	{"DOC01", "Dr Nahid-Ul-Haque", "ডা. নাহিদ-উল-হক", "PHYSICIAN",
		"the consultation station, critical-value alerts, releasing quarantined events"},
	{"REG01", "Registration Officer", "নিবন্ধন কর্মকর্তা", "REGISTRATION",
		"registering patients and the traffic board"},
	{"CA01", "Clinical Assistant", "ক্লিনিক্যাল সহকারী", "CLINICAL_ASSISTANT",
		"vitals, examination, the station screens a tablet uses"},
	{"NUT01", "Clinical Nutritionist", "পুষ্টিবিদ", "NUTRITIONIST",
		"the 24-hour recall and diet planning"},
	{"EXE01", "Exercise Specialist", "ব্যায়াম বিশেষজ্ঞ", "EXERCISE",
		"the contraindication assessment and the filtered plan"},
	{"QA01", "Quality Assurance Officer", "মান নিয়ন্ত্রণ কর্মকর্তা", "QA",
		"the correction queue and the supervisor's view"},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "devseed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	rt, err := platform.Boot(ctx, platform.Options{Service: "devseed", NeedsDB: true, NoTelemetry: true})
	if err != nil {
		return err
	}
	defer rt.Close()

	if rt.Config.Env.IsProduction() {
		return errors.New("refused: this creates accounts with a published password, and the " +
			"environment is production")
	}

	pool := rt.DB.Pool

	// The guard that is actually load-bearing. A local stack has no users; a database that
	// already has some is somebody else's, whatever the environment variable says.
	var existing int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM core.app_user
		 WHERE employee_code <> ALL($1::text[])`, codes()).Scan(&existing); err != nil {
		return fmt.Errorf("counting the users already here: %w", err)
	}
	if existing > 0 && os.Getenv("DTHCMS_DEVSEED_ANYWAY") == "" {
		return fmt.Errorf("refused: this database already holds %d user(s) this command did not "+
			"create, so it is probably not a local one. Set DTHCMS_DEVSEED_ANYWAY=1 if you are "+
			"certain", existing)
	}

	var facility uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT core.default_facility()`).Scan(&facility); err != nil {
		return fmt.Errorf("finding the facility: %w", err)
	}

	hasher := pwhash.New(pwhash.DefaultParams())
	hash, err := hasher.Hash(Password)
	if err != nil {
		return fmt.Errorf("hashing the password: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, who := range people {
		var id uuid.UUID
		// Idempotent: running it twice resets the password and re-grants the role rather than
		// failing, because the second thing anybody does with a seed command is run it again.
		err := tx.QueryRow(ctx, `
			INSERT INTO core.app_user
			  (facility_id, employee_code, name_en, name_bn, status, password_hash, password_set_at)
			VALUES ($1, $2, $3, $4, 'active', $5, now())
			ON CONFLICT (facility_id, employee_code) DO UPDATE
			   SET password_hash = EXCLUDED.password_hash,
			       password_set_at = now(),
			       status = 'active',
			       name_en = EXCLUDED.name_en,
			       name_bn = EXCLUDED.name_bn
			RETURNING id`,
			facility, who.code, who.nameEN, who.nameBN, hash).Scan(&id)
		if err != nil {
			return fmt.Errorf("creating %s: %w", who.code, err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO core.user_role (user_id, role_id, facility_id, granted_at)
			SELECT $1, r.id, $2, now() FROM core.role r WHERE r.code = $3
			ON CONFLICT DO NOTHING`, id, facility, who.role); err != nil {
			return fmt.Errorf("granting %s to %s: %w", who.role, who.code, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing: %w", err)
	}

	report()
	return nil
}

func codes() []string {
	out := make([]string, 0, len(people))
	for _, who := range people {
		out = append(out, who.code)
	}
	return out
}

func report() {
	fmt.Println()
	fmt.Println("Seeded. Sign in at http://localhost:3100 with any of these:")
	fmt.Println()
	width := 0
	for _, who := range people {
		if n := len(who.code + "  " + who.role); n > width {
			width = n
		}
	}
	for _, who := range people {
		left := who.code + "  " + who.role
		fmt.Printf("  %s%s  %s\n", left, strings.Repeat(" ", width-len(left)), who.note)
	}
	fmt.Println()
	fmt.Printf("  password (all of them):  %s\n", Password)
	fmt.Println()
	fmt.Println("None has a second factor, so the password is the whole of it. That is true of")
	fmt.Println("this command's accounts only — enrolling one is the administrator console's job.")
	fmt.Println()
}
