package visit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The Clinic Traffic Control board (CP40, blueprint §5.2).
//
// One screen, on a wall, showing where every patient in the building is. It is what makes
// the parallel twelve-station model visible: without it, "the patient is somewhere between
// anthropometry and the physician" is a thing staff say to each other twenty times a
// morning.
//
// # The screen is public
//
// Patients sit in front of it for forty minutes. So the board's payload is built from
// `core.board_row`, a view whose column list is an allowlist and whose growth is refused by
// `core.assert_the_board_shows_nothing_clinical()`. This file adds a second, narrower rule
// on top of the view: **a board entry carries no patient id.**
//
// That is not squeamishness. A patient id in the payload is a join key: anyone who can read
// the board's JSON — the display's own account, a browser extension, a screenshot in a
// group chat — can correlate a row on the wall with any other record keyed by that id.
// Without it, the only handle the board offers is a queue entry, and turning a queue entry
// into a patient requires a second call under a permission the wall display does not hold.
//
// # Bottlenecks
//
// Two thresholds, both per facility and both data (CP40's `core.board_setting`): a station
// is *busy* at one and a *bottleneck* at the other, whichever of wait or depth trips first.
// Depth matters independently of wait because a station with nine people who all arrived in
// the last minute has no long wait yet and is already the problem.

// Heat is how hard a station is struggling. Three levels, because a wall display read at
// five metres has about three levels of resolution.
type Heat string

const (
	Calm       Heat = "calm"
	Busy       Heat = "busy"
	Bottleneck Heat = "bottleneck"
)

// BoardSettings is how this facility's board names patients and when it changes colour.
type BoardSettings struct {
	IdentifyBy            string `json:"identify_by"`
	BusyWaitSeconds       int    `json:"busy_wait_seconds"`
	BusyDepth             int    `json:"busy_depth"`
	BottleneckWaitSeconds int    `json:"bottleneck_wait_seconds"`
	BottleneckDepth       int    `json:"bottleneck_depth"`
}

// The identification conventions. `code` is the default and the safe one: a visit code is
// meaningless to anyone who is not holding the card it is printed on.
const (
	IdentifyByCode      = "code"
	IdentifyByInitials  = "code_initials"
	IdentifyByClinical  = "code_clinical"
	defaultIdentifyBy   = IdentifyByCode
	defaultBusyWait     = 900
	defaultBusyDepth    = 4
	defaultBottleWait   = 1800
	defaultBottleDepth  = 7
	suggestionsPerBoard = 3
)

// BoardEntry is one patient on the board.
//
// There is no PatientID field and there must not be one. See the note at the top of the
// file: the absence is the privacy property, and `TestTheBoardCarriesNoPatientIdentifier`
// is what keeps it absent.
type BoardEntry struct {
	EntryID uuid.UUID `json:"entry_id"`
	VisitID uuid.UUID `json:"visit_id"`

	// Label is what the wall shows, already resolved against the facility's convention.
	// Resolving it here rather than in the browser means the identifying fields the
	// convention excludes never travel at all — a redaction the client performs is a
	// redaction that was transmitted.
	Label string `json:"label"`

	Status   QueueStatus `json:"status"`
	Priority int         `json:"priority"`
	// Flagged is priority > 0. The board says *that* somebody is being seen first and never
	// *why*: "critical glucose, seen first" is a diagnosis read aloud to a waiting room.
	Flagged        bool `json:"flagged"`
	CounselingDone bool `json:"counseling_done"`
	WaitedSeconds  int  `json:"waited_seconds"`
}

// BoardStation is one column of the board.
type BoardStation struct {
	StationCode string `json:"station_code"`
	// Position orders the columns along the patient's journey rather than alphabetically,
	// so the board reads left to right the way staff walk the floor.
	Position           int          `json:"position"`
	Heat               Heat         `json:"heat"`
	Waiting            int          `json:"waiting"`
	Called             int          `json:"called"`
	InService          int          `json:"in_service"`
	LongestWaitSeconds int          `json:"longest_wait_seconds"`
	Entries            []BoardEntry `json:"entries"`
}

// Suggestion is a reroute the board is offering, not applying. One tap applies it; nothing
// happens without the tap, and what happens then is attributed to whoever tapped.
type Suggestion struct {
	EntryID       uuid.UUID `json:"entry_id"`
	Label         string    `json:"label"`
	From          string    `json:"from"`
	To            string    `json:"to"`
	WaitedSeconds int       `json:"waited_seconds"`
	// FromWaiting is how deep the station they would leave is.
	//
	// The *facts*, not a sentence. An earlier draft sent "STN_EXAMINATION has 8 waiting;
	// STN_NUTRITION is free" ready-composed, which put an English sentence full of raw
	// station codes in front of a supervisor reading Bangla. A suggestion a supervisor
	// cannot evaluate in one glance is one they will either ignore or obey blindly — so the
	// board composes the sentence itself, in the language it is being read in, from station
	// names it already knows how to write.
	FromWaiting int `json:"from_waiting"`
}

// Board is the whole snapshot.
type Board struct {
	Day         string         `json:"day"`
	GeneratedAt time.Time      `json:"generated_at"`
	Settings    BoardSettings  `json:"settings"`
	Stations    []BoardStation `json:"stations"`
	Suggestions []Suggestion   `json:"suggestions"`
	Waiting     int            `json:"waiting_total"`
	InBuilding  int            `json:"in_building_total"`
}

// ErrNotWaiting is a reroute of somebody who has already been called or already left.
var ErrNotWaiting = errors.New("visit: that patient is no longer waiting at that station")

// --- reads ---

// Settings is the facility's board configuration, with the cautious defaults if a facility
// has never been configured. A board that refused to render because a settings row was
// missing would be a wall of red text in a waiting room.
func (s *Store) Settings(ctx context.Context, facility uuid.UUID) (BoardSettings, error) {
	row, err := s.q.BoardSetting(ctx, facility)
	if errors.Is(err, pgx.ErrNoRows) {
		return BoardSettings{
			IdentifyBy: defaultIdentifyBy, BusyWaitSeconds: defaultBusyWait,
			BusyDepth: defaultBusyDepth, BottleneckWaitSeconds: defaultBottleWait,
			BottleneckDepth: defaultBottleDepth,
		}, nil
	}
	if err != nil {
		return BoardSettings{}, err
	}
	return BoardSettings{
		IdentifyBy:            row.IdentifyBy,
		BusyWaitSeconds:       int(row.BusyWaitSeconds),
		BusyDepth:             int(row.BusyDepth),
		BottleneckWaitSeconds: int(row.BottleneckWaitSeconds),
		BottleneckDepth:       int(row.BottleneckDepth),
	}, nil
}

// BoardSnapshot is everything the wall shows, in one read.
//
// One query for the rows and one for the settings, then all the shaping in Go. The
// alternative — a clever SQL aggregate producing the whole nested structure — would put the
// privacy decision (which columns become a label) inside a query, where the next person to
// touch it cannot see the rule it is enforcing.
func (s *Store) BoardSnapshot(ctx context.Context, facility uuid.UUID, day, now time.Time) (Board, error) {
	settings, err := s.Settings(ctx, facility)
	if err != nil {
		return Board{}, err
	}
	rows, err := s.q.BoardRows(ctx, dbgen.BoardRowsParams{
		FacilityID: facility, ClinicDay: day,
	})
	if err != nil {
		return Board{}, err
	}

	board := Board{
		Day: day.Format(time.DateOnly), GeneratedAt: now.UTC(),
		Settings: settings, Stations: []BoardStation{}, Suggestions: []Suggestion{},
	}

	byStation := map[string]*BoardStation{}
	order := []string{}
	for _, row := range rows {
		station, seen := byStation[row.StationCode]
		if !seen {
			station = &BoardStation{
				StationCode: row.StationCode, Position: int(row.Position),
				Heat: Calm, Entries: []BoardEntry{},
			}
			byStation[row.StationCode] = station
			order = append(order, row.StationCode)
		}
		entry := boardEntryOf(row, settings.IdentifyBy, now)
		station.Entries = append(station.Entries, entry)

		switch QueueStatus(row.Status) {
		case Waiting:
			station.Waiting++
			if entry.WaitedSeconds > station.LongestWaitSeconds {
				station.LongestWaitSeconds = entry.WaitedSeconds
			}
			board.Waiting++
		case Called:
			station.Called++
		case InService:
			station.InService++
		case Done, Skipped, Rerouted:
			// Not live; `BoardRows` does not return these. The arms exist so that adding a
			// status to the queue's state machine without deciding what the board does with
			// it is a compile error rather than a silently uncounted patient.
		}
		board.InBuilding++
	}

	for _, code := range order {
		station := byStation[code]
		station.Heat = heatOf(station.Waiting, station.LongestWaitSeconds, settings)
		board.Stations = append(board.Stations, *station)
	}
	sort.SliceStable(board.Stations, func(i, j int) bool {
		if board.Stations[i].Position != board.Stations[j].Position {
			return board.Stations[i].Position < board.Stations[j].Position
		}
		return board.Stations[i].StationCode < board.Stations[j].StationCode
	})

	board.Suggestions = suggest(board.Stations, rows, settings)
	return board, nil
}

// heatOf is the colour rule, and it is deliberately an OR.
//
// Depth matters independently of wait: nine people who all arrived in the last minute have
// no long wait yet and are already the problem, because the wait they are about to have is
// determined. Wait matters independently of depth: one person sitting for forty minutes at
// a station with nobody else in it means something has gone wrong with that one person, and
// that is exactly what a supervisor should walk over and look at.
func heatOf(waiting, longestWait int, s BoardSettings) Heat {
	switch {
	case waiting >= s.BottleneckDepth || longestWait >= s.BottleneckWaitSeconds:
		return Bottleneck
	case waiting >= s.BusyDepth || longestWait >= s.BusyWaitSeconds:
		return Busy
	default:
		return Calm
	}
}

// boardEntryOf resolves one row against the facility's identification convention.
//
// The convention is applied here, on the server, and the fields it excludes are never
// serialised. A client-side redaction is a redaction that already travelled.
func boardEntryOf(row dbgen.CoreBoardRow, identifyBy string, now time.Time) BoardEntry {
	label := row.VisitCode
	switch identifyBy {
	case IdentifyByInitials:
		if row.Initials != "" {
			label = row.VisitCode + " · " + row.Initials
		}
	case IdentifyByClinical:
		label = row.VisitCode + " · " + row.ClinicalID
	}

	entry := BoardEntry{
		EntryID: row.EntryID, VisitID: row.VisitID, Label: label,
		Status: QueueStatus(row.Status), Priority: int(row.Priority),
		Flagged: row.Priority > 0, CounselingDone: row.CounselingDone,
	}

	// Accurate to the second (CP39 criterion 3), measured to whichever came first: the call
	// or now. A patient still waiting has a waiting time that grows.
	until := now
	if row.CalledAt != nil {
		until = *row.CalledAt
	}
	if until.After(row.EnteredAt) {
		entry.WaitedSeconds = int(until.Sub(row.EnteredAt).Seconds())
	}
	return entry
}

// suggest proposes reroutes out of bottlenecked stations.
//
// The rule is narrow on purpose, because a suggestion nobody can evaluate is worse than
// none. A patient is suggested for a move only when all of this holds:
//
//   - they are *waiting*, not called and not in service. Moving somebody an operator has
//     already stood up to fetch is how two people go looking for one patient.
//   - the station they are at is a bottleneck.
//   - the destination is calm, is on their own visit type's planned journey, and comes
//     after where they are now — a suggestion to go backwards is a suggestion to repeat a
//     station.
//   - they are not already queued at the destination.
//
// Longest wait first, because that is who a supervisor would move.
func suggest(stations []BoardStation, rows []dbgen.CoreBoardRow, s BoardSettings) []Suggestion {
	heat := map[string]Heat{}
	position := map[string]int{}
	for _, station := range stations {
		heat[station.StationCode] = station.Heat
		position[station.StationCode] = station.Position
	}

	// Where each visit already is, so a suggestion never sends somebody to a queue they are
	// already standing in.
	queued := map[uuid.UUID]map[string]bool{}
	for _, row := range rows {
		if queued[row.VisitID] == nil {
			queued[row.VisitID] = map[string]bool{}
		}
		queued[row.VisitID][row.StationCode] = true
	}

	// Calm destinations, nearest in the journey first.
	calm := []string{}
	for _, station := range stations {
		if station.Heat == Calm {
			calm = append(calm, station.StationCode)
		}
	}

	candidates := []Suggestion{}
	for _, station := range stations {
		if station.Heat != Bottleneck {
			continue
		}
		for _, entry := range station.Entries {
			if entry.Status != Waiting {
				continue
			}
			target := ""
			for _, code := range calm {
				if position[code] <= station.Position || queued[entry.VisitID][code] {
					continue
				}
				target = code
				break
			}
			if target == "" {
				continue
			}
			candidates = append(candidates, Suggestion{
				EntryID: entry.EntryID, Label: entry.Label,
				From: station.StationCode, To: target,
				WaitedSeconds: entry.WaitedSeconds,
				FromWaiting:   station.Waiting,
			})
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].WaitedSeconds > candidates[j].WaitedSeconds
	})
	if len(candidates) > suggestionsPerBoard {
		candidates = candidates[:suggestionsPerBoard]
	}
	return candidates
}

// --- the one write ---

// Rerouting is a supervisor moving somebody else's patient.
type Rerouting struct {
	EventID uuid.UUID
	To      string
	Reason  string
	Source  eventstore.Source
}

// rerouteNamespace derives the second event's id from the first.
//
// A reroute is two ledger entries — the patient left one queue and joined another — and a
// tablet that lost the reply and pressed again must produce the same two ids, or the retry
// writes a second arrival and the patient is queued twice. Deriving the arrival's id from
// the departure's makes the whole operation idempotent on the ledger's primary key, which
// is where idempotency belongs: not in a handler that can be bypassed.
var rerouteNamespace = uuid.MustParse("6f4d5c1e-9a2b-4f83-91c7-3b6e0d5a7c48")

// Reroute moves a waiting patient from one station's queue to another.
//
// Both writes happen in one transaction, because half a reroute is a patient who has left
// anthropometry and is standing in no queue at all — invisible to the board, which is
// precisely the failure the board exists to prevent.
func (s *Service) Reroute(ctx context.Context, entryID uuid.UUID, in Rerouting) (QueueEntry, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return QueueEntry{}, err
	}
	to := strings.TrimSpace(in.To)
	reason := strings.TrimSpace(in.Reason)
	if to == "" || len(reason) < 5 {
		return QueueEntry{}, ErrRerouteIncomplete
	}
	stations, err := s.store.Stations(ctx, actor.FacilityID())
	if err != nil {
		return QueueEntry{}, err
	}
	if !contains(stations, to) {
		return QueueEntry{}, fmt.Errorf("%w: %s", ErrUnknownStation, to)
	}

	existing, err := s.store.q.QueueEntryByID(ctx, dbgen.QueueEntryByIDParams{
		ID: entryID, FacilityID: actor.FacilityID(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return QueueEntry{}, ErrNotFound
	}
	if err != nil {
		return QueueEntry{}, err
	}
	if existing.StationCode == to {
		return QueueEntry{}, fmt.Errorf("%w: they are already at %s", ErrRerouteIncomplete, to)
	}

	now := s.clock.Now().UTC()
	waited := int(now.Sub(existing.EnteredAt).Seconds())
	arrivalID := uuid.NewSHA1(rerouteNamespace, []byte(in.EventID.String()+":arrived"))
	newEntryID := uuid.NewSHA1(rerouteNamespace, []byte(in.EventID.String()+":entry"))

	var out QueueEntry
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		row, err := q.RerouteQueueEntry(ctx, dbgen.RerouteQueueEntryParams{
			PEntry: entryID, PFacility: actor.FacilityID(), PTo: to, PReason: reason,
			PUser: actor.UserID(), PAt: now, PNewEntry: newEntryID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// The function returns no rows when the entry is not live. Which is the
			// interesting race: two supervisors looking at the same board, one of them a
			// second slower. The second is told the patient has moved on, not that the
			// entry does not exist.
			return ErrNotWaiting
		}
		if err != nil {
			if isUnique(err, "queue_one_live_per_station") {
				return ErrAlreadyQueued
			}
			return err
		}
		out = queueEntryOf(row, now)

		left, err := json.Marshal(eventstore.QueueLeft{
			FacilityID: actor.FacilityID().String(), PatientID: existing.PatientID.String(),
			VisitID: existing.VisitID.String(), EntryID: entryID.String(),
			StationCode: existing.StationCode, Outcome: "rerouted",
			Reason: reason, ReroutedTo: to, WaitedSeconds: waited,
		})
		if err != nil {
			return err
		}
		if err := s.append(ctx, tx, in.EventID, existing.VisitID, existing.PatientID,
			"QUEUE_LEFT", left, in.Source, now); err != nil {
			return err
		}

		entered, err := json.Marshal(eventstore.QueueEntered{
			FacilityID: actor.FacilityID().String(), PatientID: existing.PatientID.String(),
			VisitID: existing.VisitID.String(), EntryID: newEntryID.String(),
			StationCode: to, Position: int(row.Position),
			Priority: int(row.Priority), PriorityReason: row.PriorityReason,
		})
		if err != nil {
			return err
		}
		return s.append(ctx, tx, arrivalID, existing.VisitID, existing.PatientID,
			"QUEUE_ENTERED", entered, in.Source, now)
	})
	if err != nil {
		return QueueEntry{}, err
	}
	// Two announcements, because two columns of the board changed: the station they left
	// got shorter and the station they joined got longer. A board told only about the
	// arrival would leave a stale row sitting in the bottleneck it was rerouted out of,
	// which is the one column a supervisor is watching.
	s.announce(ctx, actor.FacilityID(), QueueEntry{
		ID: entryID, VisitID: existing.VisitID, StationCode: existing.StationCode,
		Status: Rerouted,
	}, KindQueueRerouted)
	s.announce(ctx, actor.FacilityID(), out, KindQueueEntered)
	return out, nil
}
