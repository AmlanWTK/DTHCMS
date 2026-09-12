package formulary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The monthly price review (CP75 criterion 4, §16.1).
//
// # Role or named person: both, and here is why
//
// §16.1 asks who owns monthly price review, and D-56 defaults it to the PHARMACIST role. A role
// and a person answer different halves of the question, and the module models both because the
// failures are different:
//
//   - **Role only.** The review survives the pharmacist leaving, and the reminder always has
//     somebody it can reach. It is also the classic way a recurring task goes undone: a task
//     owned by "the pharmacists" is a task nobody in particular has failed to do.
//   - **Person only.** Somebody feels responsible, which is the whole mechanism. It also stops
//     dead the month they are on leave, and stops permanently the month they resign — with
//     nothing in the system noticing, because the row still names them.
//
// So `owner_role` is always set and is the floor; `owner_user_id` is optional and names the
// deputy actually expected to do it. The cycle **copies both** when it opens, because who was
// asked to do March's review is a fact about March and does not change in June.
//
// The recommendation to Dr. Nahid, stated in the checkpoint report rather than decided here: set
// the role to PHARMACIST and name the pharmacist as well. The named person is what makes it
// happen; the role is what makes it keep happening.
//
// # Monthly, on a queue that only does intervals
//
// `ops.job_schedule.every_seconds` tops out at a day (ADR-0031 bought intervals rather than a
// cron parser, on the reasoning that every periodic job this system had was "every N"). A
// monthly review does not fit that, and the answer is not to buy a cron parser: **"is a review
// due" is a domain question**. The job runs daily and asks it. If the current month already has
// a cycle, it does nothing. If the due day has not arrived, it does nothing. Otherwise it opens
// the cycle and raises the reminder.
//
// That makes the reminder idempotent by construction, and the unique index on
// (facility_id, period_month) is the backstop: two workers that both claim the job produce one
// cycle and one alert.
//
// # Where the reminder actually goes
//
// A row in `core.admin_alert`, which is the notification channel this system has — every
// administrator's console polls it every thirty seconds and shows it until somebody
// acknowledges. Plus the open cycle itself, which the formulary screen draws at the top for
// anybody holding `formulary.read`, which the pharmacist does.
//
// That is honest about a gap and it should be read as such: **the pharmacist does not hold
// `alert.read`**, so the console alert reaches administrators rather than the owner. The owner
// sees the review because their own screen says so, not because anything pushed it to them. An
// SMS or an e-mail needs a gateway this clinic does not yet have (`docs/audit.md` §7 carries the
// same open item), and inventing one here would be a notifier nobody had tested against a real
// network.

// Review is one month's cycle.
type Review struct {
	ID          uuid.UUID `json:"id"`
	PeriodMonth string    `json:"period_month"`
	Status      string    `json:"status"`

	OwnerRole       string `json:"owner_role"`
	OwnerRoleNameEN string `json:"owner_role_name_en"`
	OwnerRoleNameBN string `json:"owner_role_name_bn"`
	OwnerNameEN     string `json:"owner_name_en,omitempty"`
	OwnerNameBN     string `json:"owner_name_bn,omitempty"`
	OwnerCode       string `json:"owner_code,omitempty"`

	OpenedAt time.Time `json:"opened_at"`
	DueOn    string    `json:"due_on"`
	// RemindedAt is nil when the cycle exists but nobody has been told. A screen has to be
	// able to show that state, because an unreminded review is exactly the failure criterion 4
	// is about — and a screen that drew "reminded" unconditionally would hide it.
	RemindedAt *time.Time `json:"reminded_at,omitempty"`

	ProductsAtOpen    int `json:"products_at_open"`
	ProvisionalAtOpen int `json:"provisional_at_open"`

	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	CompletedByEN  string     `json:"completed_by_name_en,omitempty"`
	CompletedByBN  string     `json:"completed_by_name_bn,omitempty"`
	CompletionNote string     `json:"completion_note,omitempty"`
}

// ReviewOwner is who owns the review from now on.
type ReviewOwner struct {
	Role       string `json:"owner_role"`
	RoleNameEN string `json:"owner_role_name_en"`
	RoleNameBN string `json:"owner_role_name_bn"`

	UserID string `json:"owner_user_id,omitempty"`
	NameEN string `json:"owner_name_en,omitempty"`
	NameBN string `json:"owner_name_bn,omitempty"`
	Code   string `json:"owner_code,omitempty"`
	DueDay int    `json:"due_day_of_month"`
}

// ReviewState is what the review screen shows.
type ReviewState struct {
	Owner ReviewOwner `json:"owner"`
	// Current is the open cycle, or the most recent closed one. Nil only before the first
	// cycle has ever opened.
	Current *Review `json:"current,omitempty"`

	Products   int `json:"products"`
	Unverified int `json:"unverified"`
	// OldestPriceDays is how long ago the least recently touched current price was recorded.
	// The number that says whether the formulary is being maintained at all.
	OldestPriceDays int `json:"oldest_price_days"`
}

// ReviewState reads it.
func (s *Store) ReviewState(ctx context.Context, facility uuid.UUID) (ReviewState, error) {
	out := ReviewState{}

	owner, err := s.q.ReviewOwner(ctx, facility)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReviewState{}, ErrNotFound
	}
	if err != nil {
		return ReviewState{}, err
	}
	out.Owner = ReviewOwner{
		Role: owner.OwnerRole, RoleNameEN: owner.RoleNameEn, RoleNameBN: owner.RoleNameBn,
		DueDay: int(owner.DueDayOfMonth),
	}
	if owner.OwnerUserID.Valid {
		out.Owner.UserID = owner.OwnerUserID.UUID.String()
	}
	if owner.OwnerNameEn != nil {
		out.Owner.NameEN = *owner.OwnerNameEn
	}
	if owner.OwnerNameBn != nil {
		out.Owner.NameBN = *owner.OwnerNameBn
	}
	if owner.OwnerCode != nil {
		out.Owner.Code = *owner.OwnerCode
	}

	counts, err := s.q.ReviewCounts(ctx, facility)
	if err != nil {
		return ReviewState{}, err
	}
	out.Products, out.Unverified = int(counts.Products), int(counts.Provisional)
	out.OldestPriceDays = int(counts.OldestPriceAgeSeconds / 86400)

	current, err := s.q.CurrentReview(ctx, facility)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return ReviewState{}, err
	}
	review := Review{
		ID: current.ID, PeriodMonth: day(current.PeriodMonth), Status: current.Status,
		OwnerRole: current.OwnerRole, OwnerRoleNameEN: current.RoleNameEn,
		OwnerRoleNameBN: current.RoleNameBn,
		OpenedAt:        current.OpenedAt, DueOn: day(current.DueOn),
		RemindedAt:        current.RemindedAt,
		ProductsAtOpen:    int(current.ProductsAtOpen),
		ProvisionalAtOpen: int(current.ProvisionalAtOpen),
		CompletedAt:       current.CompletedAt,
	}
	if current.OwnerNameEn != nil {
		review.OwnerNameEN = *current.OwnerNameEn
	}
	if current.OwnerNameBn != nil {
		review.OwnerNameBN = *current.OwnerNameBn
	}
	if current.OwnerCode != nil {
		review.OwnerCode = *current.OwnerCode
	}
	if current.CompletedByNameEn != nil {
		review.CompletedByEN = *current.CompletedByNameEn
	}
	if current.CompletedByNameBn != nil {
		review.CompletedByBN = *current.CompletedByNameBn
	}
	if current.CompletionNote != nil {
		review.CompletionNote = *current.CompletionNote
	}
	out.Current = &review
	return out, nil
}

// SetOwner names who owns the review from now on. Open cycles keep the owner they were opened
// with: changing who is responsible next month does not rewrite who was responsible last.
func (s *Store) SetOwner(ctx context.Context, facility uuid.UUID, role string,
	user *uuid.UUID, dueDay int, actor uuid.UUID) error {

	if dueDay < 1 || dueDay > 28 {
		return errors.New("formulary: the review day must be between the 1st and the 28th")
	}
	return s.q.SetReviewOwner(ctx, dbgen.SetReviewOwnerParams{
		FacilityID: facility, OwnerRole: strings.ToUpper(strings.TrimSpace(role)),
		OwnerUserID:   nullUUID(user),
		DueDayOfMonth: int32(dueDay), //nolint:gosec // bounded above
		ActorID:       uuid.NullUUID{UUID: actor, Valid: actor != uuid.Nil},
	})
}

// ReviewCompletion is a cycle being closed, for the audit bridge.
type ReviewCompletion struct {
	FacilityID  uuid.UUID
	ReviewID    uuid.UUID
	PeriodMonth string
	Note        string

	// Unverified is how many products still had an unchecked price at the moment the cycle
	// was closed. **On the audit entry deliberately**: closing a review with two hundred
	// prices still unverified is a legitimate thing to do and a thing somebody should be able
	// to see was done.
	Unverified int

	ActorID   uuid.UUID
	ActorCode string
	ActorRole string
}

// CompleteReview closes the open cycle.
func (s *Store) CompleteReview(ctx context.Context, facility, review uuid.UUID,
	note string, actor uuid.UUID) (ReviewCompletion, error) {

	counts, err := s.q.ReviewCounts(ctx, facility)
	if err != nil {
		return ReviewCompletion{}, err
	}
	row, err := s.q.CompleteReview(ctx, dbgen.CompleteReviewParams{
		ID: review, FacilityID: facility, ActorID: uuid.NullUUID{UUID: actor, Valid: true},
		CompletionNote: nilIfBlank(note),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReviewCompletion{}, ErrNoOpenReview
	}
	if err != nil {
		return ReviewCompletion{}, err
	}
	return ReviewCompletion{
		FacilityID: facility, ReviewID: row.ID, PeriodMonth: day(row.PeriodMonth),
		Note: strings.TrimSpace(note), Unverified: int(counts.Provisional), ActorID: actor,
	}, nil
}

// ReviewOpened is a cycle opening, for the audit bridge. It names the role and the person the
// month was assigned to, because "who was asked" is the question a review nobody did gets asked.
type ReviewOpened struct {
	FacilityID  uuid.UUID
	ReviewID    uuid.UUID
	PeriodMonth string
	OwnerRole   string
	OwnerUserID *uuid.UUID
	Products    int
	Unverified  int
}

// Auditor is the composition root's bridge to the security trail.
//
// An interface rather than a dependency, for the reason every module here has one: `formulary`
// may not import `audit` (architecture.json), because a module able to write audit entries
// directly grows a second, differently-shaped way of describing what happened — and the trail's
// value is that there is exactly one.
type Auditor interface {
	PriceChanged(ctx context.Context, change PriceChange) error
	ReviewCompleted(ctx context.Context, done ReviewCompletion) error
	ReviewOpener
}

// ReviewOpener is the one method the scheduled sweep needs.
//
// Split out of Auditor because the worker binary runs the sweep and nothing else here: asking it
// for a type that can also record a price change would make it carry — and keep in step — three
// translations of which it uses one. The API binary implements the whole of Auditor; the worker
// implements this.
type ReviewOpener interface {
	ReviewOpened(ctx context.Context, opened ReviewOpened) error
}

// ReviewJobKind is the queue kind CP69 runs this under.
const ReviewJobKind = "maintenance.formulary_price_review"

// ReviewSweep opens whatever review cycles are due and reminds their owners.
//
// Runs daily. Everything about it is idempotent: an existing cycle for the month is left alone,
// a cycle already reminded is not reminded again, and the unique index is the backstop if two
// workers somehow both get here.
//
// `now` is passed rather than read, so the test can be a table of dates rather than a wait.
func (s *Store) ReviewSweep(ctx context.Context, now time.Time, audit ReviewOpener) (int, error) {
	facilities, err := s.q.FacilitiesForReview(ctx)
	if err != nil {
		return 0, err
	}

	opened := 0
	for _, facility := range facilities {
		// The clinic's calendar, not the server's. A review "due on the 1st" means the 1st in
		// Faridpur, and a server in UTC is six hours behind that — so a sweep at 02:00 Dhaka
		// on the 1st would otherwise still think it was the last day of the previous month.
		location, err := time.LoadLocation(facility.Timezone)
		if err != nil {
			location = time.UTC
		}
		local := now.In(location)
		if local.Day() < int(facility.DueDayOfMonth) {
			continue
		}
		period := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.UTC)
		due := period.AddDate(0, 0, int(facility.DueDayOfMonth)-1)

		counts, err := s.q.ReviewCounts(ctx, facility.ID)
		if err != nil {
			return opened, err
		}

		row, err := s.q.OpenReview(ctx, dbgen.OpenReviewParams{
			FacilityID: facility.ID, PeriodMonth: period,
			OwnerRole: facility.OwnerRole, OwnerUserID: facility.OwnerUserID, DueOn: due,
			ProductsAtOpen:    int32(counts.Products),    //nolint:gosec // a count of rows
			ProvisionalAtOpen: int32(counts.Provisional), //nolint:gosec // a count of rows
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// This month's cycle already exists. Nothing to do and nothing to say — this is
			// the ordinary outcome on twenty-nine days out of thirty.
			continue
		}
		if err != nil {
			return opened, err
		}
		opened++

		alertID, err := s.remind(ctx, facility.ID, period, counts.Products, counts.Provisional)
		if err != nil {
			return opened, err
		}
		if err := s.q.MarkReviewReminded(ctx, dbgen.MarkReviewRemindedParams{
			ID: row, AlertID: uuid.NullUUID{UUID: alertID, Valid: true},
		}); err != nil {
			return opened, err
		}

		if audit != nil {
			var user *uuid.UUID
			if facility.OwnerUserID.Valid {
				id := facility.OwnerUserID.UUID
				user = &id
			}
			if err := audit.ReviewOpened(ctx, ReviewOpened{
				FacilityID: facility.ID, ReviewID: row, PeriodMonth: day(period),
				OwnerRole: facility.OwnerRole, OwnerUserID: user,
				Products: int(counts.Products), Unverified: int(counts.Provisional),
			}); err != nil {
				return opened, err
			}
		}
	}
	return opened, nil
}

// remind raises the console alert. Bilingual, and it names the number that makes the review
// worth doing: how many prices nobody has checked.
func (s *Store) remind(ctx context.Context, facility uuid.UUID, period time.Time,
	products, unverified int64) (uuid.UUID, error) {

	month := period.Format("January 2006")
	reference, err := json.Marshal(map[string]any{
		"period_month": day(period),
		"products":     products,
		"unverified":   unverified,
	})
	if err != nil {
		return uuid.Nil, err
	}
	return s.q.RaiseReviewAlert(ctx, dbgen.RaiseReviewAlertParams{
		FacilityID: facility,
		MessageEn: fmt.Sprintf(
			"The medicine price review for %s is due: %d of %d prices have not been checked by anybody.",
			month, unverified, products),
		MessageBn: fmt.Sprintf(
			"%s মাসের ওষুধের দাম পর্যালোচনার সময় হয়েছে: %dটি দামের মধ্যে %dটি কেউ যাচাই করেননি।",
			month, products, unverified),
		Reference: reference,
	})
}
