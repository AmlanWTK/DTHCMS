package formulary

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// The formulary, held in the API process (CP76, §10.1).
//
// # Why the whole thing is in memory, and why that is not a shortcut
//
// The checkpoint's first criterion is a p99 under 50ms **including network** on the clinic's
// own LAN. That budget is mostly spent before the handler runs. What is left does not comfortably
// hold a round trip to PostgreSQL on every keystroke of a physician typing at speed, and the
// plan says so in as many words: a few hundred rows, hold them.
//
// The size argument is worth stating precisely rather than waving at. The formulary is 250
// products across 59 molecules; the index below is about 180 brand groups, each holding a
// handful of strings and a price. That is well under a megabyte, it does not grow with patients,
// visits or prescriptions, and the thing that would make it grow — the clinic stocking more
// medicines — is bounded by what a pharmacy counter in Faridpur can hold. If that ever stops
// being true, the seam to change is [Cache.snapshotFor]; nothing above it knows.
//
// # How a change gets in, and the sixty seconds
//
// Criterion 4: a formulary change is reflected within 60 seconds. Two mechanisms, and the
// slower one is the one that carries the criterion.
//
//  1. **The refresh loop** asks the database for a watermark — a row count and a newest-change
//     instant — every [DefaultRefresh]. If it differs from the one the snapshot was built at,
//     the snapshot is rebuilt. This is what makes the criterion true no matter *who* made the
//     change: a second API process, a migration, the monthly review job in the worker, or a
//     pharmacist with psql.
//
//  2. **[Cache.Invalidate]**, called by this module's own write handlers, drops the snapshot
//     immediately so the next search rebuilds it. That is a latency optimisation for the common
//     case and nothing more. **It is deliberately not what the test of criterion 4 exercises**,
//     because a test that changes a price by calling into the same process it then queries has
//     proved that a function call works, not that a running clinic's second API instance would
//     ever notice.
//
// The watermark is a single cheap row. At ten seconds, one instance asks six aggregate counts of
// PostgreSQL every ten seconds, which is less load than one physician typing four letters would
// have generated under the query-per-keystroke design this replaces.
//
// # What happens when the database is down
//
// The search keeps answering from the snapshot it has, and the response carries its age. A
// physician mid-clinic whose formulary is thirty seconds stale can still prescribe; one whose
// autocomplete returns 503 cannot. There is no clinical decision in this module — no dose, no
// interaction, no allergy — so serving slightly old trade names and prices is a bounded harm,
// and the age is on the response so nothing downstream has to assume it is fresh.
//
// A price is the one thing here anybody could be misled by, and it is bounded twice: the refresh
// window is ten seconds, and every price carries its verification state, which for all 250
// seeded prices is PROVISIONAL.
//
// # No patient, no logging of what was typed
//
// Nothing in this file logs, traces or counts a query string. A physician's search terms in a
// clinic where the formulary is mostly diabetes drugs would say what he is treating, one
// keystroke at a time, and a log line is not a place a diagnosis belongs.

// DefaultRefresh is how often the cache asks whether the formulary has changed.
//
// Ten seconds against a sixty-second criterion. The margin is for a refresh that overlaps a slow
// query, for a clock that is not exact, and for the second and third API process that will
// eventually exist — not for comfort.
const DefaultRefresh = 10 * time.Second

// DefaultSearchLimit is how many brands a search answers with when the client does not say.
const DefaultSearchLimit = 10

// MaxSearchLimit is the ceiling. A combobox that showed fifty brands would be a scroll, and a
// scroll is the thing two-letter search exists to avoid.
const MaxSearchLimit = 25

// Usage is how often and how recently one prescriber has prescribed each product.
//
// **Empty today.** CP80 is the prescription aggregate and it does not exist, so there is no
// source for either number and this module refuses to invent one: see [UsageSource].
type Usage struct {
	// TimesPrescribed counts, per product, over whatever window the source chose.
	TimesPrescribed map[uuid.UUID]int
	// DaysSinceLast is how many days ago this prescriber last prescribed the product. Absent
	// from the map means never.
	DaysSinceLast map[uuid.UUID]int
}

// forProducts collapses a brand group's products into the brand's own numbers: the sum of the
// counts, and the most recent of the last-used days.
//
// Sum and minimum rather than, say, the busiest strength, because the physician is choosing a
// brand. Thyrox 50 prescribed weekly and Thyrox 25 prescribed weekly should put Thyrox above a
// brand prescribed weekly in one strength, which is what he would expect and what a per-product
// ranking would get wrong.
func (u Usage) forProducts(ids []uuid.UUID) (times int, daysSince int) {
	daysSince = neverPrescribed
	for _, id := range ids {
		times += u.TimesPrescribed[id]
		if d, ok := u.DaysSinceLast[id]; ok && d < daysSince {
			daysSince = d
		}
	}
	return times, daysSince
}

// UsageSource is the seam CP80's prescriptions plug into, and the only part of criterion 3 that
// exists today.
//
// # What is stubbed, stated plainly
//
// Criterion 3 is "the physician's own recent prescriptions rank higher". There are no
// prescriptions in this system yet — CP80 builds them — so there is nothing to rank by, and the
// two ways of pretending otherwise were both refused:
//
//   - Ranking by *anybody's* prescribing would answer a different question and would make the
//     ranking worse for the one physician using it, not better.
//   - Ranking by a proxy (how many products a molecule has, how cheap it is, how often it
//     appears in a history) would produce an order that looks personalised, is not, and would
//     be very hard to notice as wrong.
//
// So [NoUsage] is what CP76 ships, the two rank terms are constants, and the response says
// `ranking_complete: false`. CP80's work is to write an implementation of this interface over
// `prescription_item` and pass it to [CacheConfig]; no ranking code changes.
//
// The interface takes the prescriber because the criterion says *this physician*. A shared
// clinic-wide count is a different and lesser signal, and putting the prescriber in the
// signature now is what stops it quietly becoming the thing that ships.
type UsageSource interface {
	Usage(ctx context.Context, facility, prescriber uuid.UUID) (Usage, error)
	// Complete reports whether this source can actually answer. False means the ranking is
	// running with two of its four terms constant, and the response says so to the client.
	//
	// A method rather than a comment, because "the ranking is not finished" has to travel all
	// the way to the screen the physician is looking at, and a fact that only exists in a
	// doc comment does not travel anywhere.
	Complete() bool
}

// NoUsage is the source CP76 ships: there are no prescriptions, so there is no usage.
type NoUsage struct{}

// Usage returns nothing, and that is the honest answer until CP80.
func (NoUsage) Usage(context.Context, uuid.UUID, uuid.UUID) (Usage, error) { return Usage{}, nil }

// Complete is false. CP80 has not happened.
func (NoUsage) Complete() bool { return false }

// watermark is the database's answer to "has anything changed?".
type watermark struct {
	rows      int64
	changedAt time.Time
}

// snapshot is one facility's formulary, indexed and ready to search.
type snapshot struct {
	index []*indexEntry
	mark  watermark
	// day is the clinic calendar day the prices in this snapshot were in force on.
	//
	// Its own field because it is the one change the watermark cannot see: a price recorded
	// last week to take effect tomorrow becomes the price in force at midnight without any row
	// changing, so a cache that only watched the watermark would serve yesterday's price all of
	// the next day. The monthly review records future-dated prices routinely, so this is a
	// case that happens rather than one that could.
	day      time.Time
	loadedAt time.Time
}

// Cache holds the formulary and keeps it current.
type Cache struct {
	store   *Store
	usage   UsageSource
	clock   interface{ Now() time.Time }
	refresh time.Duration
	logger  *slog.Logger

	mu   sync.RWMutex
	byID map[uuid.UUID]*snapshot
}

// CacheConfig builds a Cache.
type CacheConfig struct {
	Store *Store
	// Usage is CP80's signal. Nil means [NoUsage].
	Usage   UsageSource
	Clock   interface{ Now() time.Time }
	Refresh time.Duration
	Logger  *slog.Logger
}

// NewCache builds one. It loads nothing until the first search: a facility with no physician
// signed in should not cost the process a query every ten seconds.
func NewCache(cfg CacheConfig) *Cache {
	c := &Cache{
		store:   cfg.Store,
		usage:   cfg.Usage,
		clock:   cfg.Clock,
		refresh: cfg.Refresh,
		logger:  cfg.Logger,
		byID:    map[uuid.UUID]*snapshot{},
	}
	if c.usage == nil {
		c.usage = NoUsage{}
	}
	if c.refresh <= 0 {
		c.refresh = DefaultRefresh
	}
	if c.logger == nil {
		c.logger = slog.Default()
	}
	return c
}

func (c *Cache) now() time.Time {
	if c.clock == nil {
		return time.Now().UTC()
	}
	return c.clock.Now().UTC()
}

// today is the clinic's calendar day, UTC-truncated — the same day [Handlers.today] uses, and
// for the reason stated there: a price is a daily fact and the six hours between Dhaka and UTC
// are not a thing anybody prices against.
func (c *Cache) today() time.Time { return c.now().Truncate(24 * time.Hour) }

// Run keeps every loaded facility's snapshot current until ctx is cancelled.
//
// Started by the composition root. A process that never starts it still answers correctly — the
// first search of each facility loads a snapshot — it just never notices a change made
// elsewhere, which is why the composition root starting it is not optional and why
// [Cache.Age] exists for a health check to look at.
func (c *Cache) Run(ctx context.Context) {
	ticker := time.NewTicker(c.refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.refreshAll(ctx)
		}
	}
}

func (c *Cache) refreshAll(ctx context.Context) {
	c.mu.RLock()
	ids := make([]uuid.UUID, 0, len(c.byID))
	for id := range c.byID {
		ids = append(ids, id)
	}
	c.mu.RUnlock()

	for _, id := range ids {
		if _, err := c.snapshotFor(ctx, id, true); err != nil {
			// Logged once per facility per tick and without the error being allowed to stop
			// the loop: a refresh that fails leaves the previous snapshot in place, which is
			// the whole point of the design.
			c.logger.WarnContext(ctx, "the formulary cache could not refresh",
				slog.String("facility_id", id.String()), slog.String("error", err.Error()))
		}
	}
}

// Invalidate drops a facility's snapshot so that the next search rebuilds it.
//
// Called by this module's write handlers. It is a latency optimisation, not the mechanism behind
// criterion 4 — see the package note.
func (c *Cache) Invalidate(facility uuid.UUID) {
	c.mu.Lock()
	delete(c.byID, facility)
	c.mu.Unlock()
}

// Age is how long ago a facility's snapshot was built, and whether there is one.
func (c *Cache) Age(facility uuid.UUID) (time.Duration, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.byID[facility]
	if !ok {
		return 0, false
	}
	return c.now().Sub(s.loadedAt), true
}

// Search answers one query.
func (c *Cache) Search(ctx context.Context, facility, prescriber uuid.UUID,
	query string, limit int) (SearchResult, error) {

	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}

	snap, err := c.snapshotFor(ctx, facility, false)
	if err != nil {
		return SearchResult{}, err
	}

	// The usage lookup is outside the snapshot because it is per-physician and per-request:
	// two doctors searching the same two letters must not share a cached ranking. Today it is
	// a method call that returns an empty struct; when CP80 fills it in, this is the line that
	// will need a cache of its own, and it will be a cache keyed by prescriber rather than a
	// change to anything above.
	usage, err := c.usage.Usage(ctx, facility, prescriber)
	if err != nil {
		return SearchResult{}, err
	}

	out := searchIndex(snap.index, query, limit, usage)
	out.ServedFrom = "cache"
	out.AgeSeconds = int(c.now().Sub(snap.loadedAt) / time.Second)
	out.RankingComplete = c.usage.Complete()
	return out, nil
}

// snapshotFor returns a facility's snapshot, building or refreshing it as needed.
//
// `check` asks it to consult the watermark even when a snapshot exists — the refresh loop's
// path. A search does not: a snapshot that is at most [DefaultRefresh] old is the thing the
// whole design is for, and checking the watermark per keystroke would put the round trip back.
//
// A snapshot built for a different day is rebuilt whoever asks, watermark or not. See
// [snapshot.day].
func (c *Cache) snapshotFor(ctx context.Context, facility uuid.UUID, check bool) (*snapshot, error) {
	c.mu.RLock()
	existing, ok := c.byID[facility]
	c.mu.RUnlock()

	today := c.today()

	if ok && !check && existing.day.Equal(today) {
		return existing, nil
	}

	if ok && existing.day.Equal(today) {
		mark, err := c.store.Watermark(ctx, facility)
		if err != nil {
			return nil, err
		}
		if mark == existing.mark {
			return existing, nil
		}
	}

	rows, mark, err := c.store.CacheRows(ctx, facility, today)
	if err != nil {
		return nil, err
	}
	built := &snapshot{index: buildIndex(rows), mark: mark, day: today, loadedAt: c.now()}

	c.mu.Lock()
	c.byID[facility] = built
	c.mu.Unlock()
	return built, nil
}

// buildIndex groups the rows into brands and precomputes every match key.
//
// The group is (trade name, form). See the note at the top of search.go for why the result of a
// prescribing search is a brand rather than a row, and why the form is part of the identity:
// Comet and Comet XR are dosed differently and are not two strengths of one thing.
func buildIndex(rows []CacheRow) []*indexEntry {
	type key struct{ trade, form string }
	order := make([]key, 0, len(rows))
	groups := map[key]*SearchEntry{}

	for _, r := range rows {
		// A withdrawn product is not prescribable, so it is not in the index. The row is
		// fetched anyway — the cache load is one statement over a few hundred rows and
		// filtering in SQL would put half of "what may be prescribed" in the query and half
		// here. A brand every one of whose strengths has been withdrawn therefore never
		// appears; a brand with one strength left appears with that one strength.
		if !r.IsActive {
			continue
		}
		k := key{trade: r.TradeName, form: r.FormCode}
		e, ok := groups[k]
		if !ok {
			e = &SearchEntry{
				TradeName: r.TradeName, GenericName: r.GenericName,
				Manufacturer: r.Manufacturer,
				ClassCode:    r.ClassCode, ClassEN: r.ClassEN, ClassBN: r.ClassBN,
				FormCode: r.FormCode, FormEN: r.FormEN, FormBN: r.FormBN,
			}
			groups[k] = e
			order = append(order, k)
		}
		e.Strengths = append(e.Strengths, SearchStrength{
			ProductID: r.ProductID, Strength: r.Strength,
			DispenseUnit: r.DispenseUnit, UnitEN: r.UnitEN, UnitBN: r.UnitBN,
			Price: r.Price,
		})
	}

	out := make([]*indexEntry, 0, len(order))
	for _, k := range order {
		e := groups[k]
		sortStrengths(e.Strengths)
		out = append(out, buildIndexEntry(*e))
	}
	return out
}
