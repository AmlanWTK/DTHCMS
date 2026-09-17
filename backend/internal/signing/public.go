package signing

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The public verification page's endpoint (CP85).
//
// # This is the system's only unauthenticated surface besides login, and it is treated as hostile
//
// Everything in this file follows from that sentence. There is no session, no device, no
// facility, no permission and no idempotency key. The caller is a stranger with a phone, and a
// fraction of the callers are somebody looking for a way in.
//
// Six properties, each of which is a line of code rather than an intention:
//
//  1. **The response is an allowlist.** [PublicVerification] is the whole of what may be
//     returned, it is built from [Resolved] and nothing else, and a test asserts the JSON
//     object's key set *is* the allowed set. A positive assertion rather than a blacklist,
//     because a blacklist of forbidden words passes the day somebody adds a field nobody thought
//     to forbid.
//  2. **No PHI, and no query that could produce any.** `Store.ByTokenDigest` joins the signature
//     to `core.app_user` and `core.facility` and touches no patient table. The count of items is
//     a count; there is no route from this handler to a drug name.
//  3. **Tokens are not enumerable.** 160 bits of `crypto/rand`, looked up by digest. See
//     token.go.
//  4. **Nothing distinguishes an unknown token from a tampered prescription.** Both answer
//     [VerdictNotVerified] with no detail block. That is a deliberate loss of helpfulness: a
//     page that said "this prescription exists but has been altered" for one and "no such code"
//     for the other would be a membership oracle over the token space, and — worse — would tell
//     somebody forging a prescription that they had got the token right and only the content
//     wrong.
//  5. **Rate limited, by address, before the database is touched.** See [PublicRateLimit].
//  6. **Every attempt is logged**, with a digest of the address and never the address, and with
//     a prescription id only when the token actually resolved — taken from the resolved row, so
//     a caller cannot cause an id of their choosing to be written beside their own fingerprint.
//
// # Two accepted distinguishers, decided rather than overlooked
//
// A page that is careful about oracles still has two, and they are recorded here because a risk
// somebody decided to take should read differently from one nobody noticed. **Both were reviewed
// and accepted, and both are in scope for CP94's penetration test.**
//
//  1. **Response size.** A VERIFIED body carries the block and is roughly twice the length of a
//     NOT_VERIFIED one. It is not padded. Somebody who already holds a valid token learns nothing
//     from that, and somebody guessing learns only that their guess was wrong — which the verdict
//     in the body already told them.
//
//  2. **Timing, and this one is the sharper of the two.** There are three distinguishable costs
//     on this path:
//
//     A **malformed** token is refused by [WellFormedToken] before any database access. That
//     leaks nothing: it is a syntactic check on the token's own shape, which any caller can
//     apply to their own guess without asking us.
//
//     A **well-formed but unknown** token costs one indexed probe on the digest index.
//
//     A **well-formed and known** token costs that probe *plus* a prescription read, a
//     canonicalisation over every live item, and an Ed25519 verification. That difference is
//     measurable, and it is therefore a membership oracle over the token space: a caller who can
//     time the response can tell whether a token they hold names a signature, without being shown
//     the block.
//
//     **This is accepted rather than fixed.** The oracle answers "is this one of the live
//     tokens", and the token space is 160 bits from `crypto/rand` — so the only caller who can
//     use the answer is one who already has a token to ask about, and a caller who has a token
//     has the paper. Equalising it means a constant-time floor on every request to this endpoint,
//     which costs every genuine scan at a pharmacy counter to deny an attacker a bit they cannot
//     reach the token space with. The limiter bounds how fast the question can be asked at all.
//
//     What would change this judgement: a token space that stopped being 160 bits of randomness,
//     a token derived from anything guessable, or a deployment where an attacker can time
//     responses precisely enough to distinguish a *cached* prescription read from an uncached one
//     — which would turn the oracle into a per-prescription access pattern. CP94 should test for
//     the last of those specifically.

// PublicVerificationPathPrefix is where the endpoint lives.
//
// Under `/v1` for versioning, and **outside the authenticated chain**, exactly as `/v1/auth` is:
// a caller with no credentials cannot be asked for one before being given what they came for.
const PublicVerificationPathPrefix = "/v1/verify"

// VerificationPath is the path a QR code encodes for a token.
//
// The *page*, not the API: a phone camera opens a URL in a browser, and a browser pointed at a
// JSON endpoint shows a stranger a wall of braces. The web page then calls the API. Composed in
// one function so the printed sheet and the page cannot disagree.
func VerificationPath(token string) string { return "/verify/" + token }

// PublicVerification is the entire public response.
//
// # Every field here was decided, and the ones that are absent were decided harder
//
// The implementation plan names five things: issuing physician, issue date, prescription id,
// verification status, item count. The clinic's name is added because a verification page that
// does not say which clinic issued the prescription answers half the question a stranger has.
// Nothing else is here.
//
// **Absent on purpose**: the patient (any part of them — no name, no id, no age, no sex), the
// diagnosis, any medicine, any dose, the visit, the QA clearance, the canonical digest, the
// signature bytes, the public key, the key id, the signer kind and the device assurance. The
// last five are not PHI and are still absent: they are the clinic's security posture, and a
// public page that published which prescriptions were signed with a development key would be
// publishing a target list.
type PublicVerification struct {
	// Verdict is VERIFIED or NOT_VERIFIED. One word, and the only thing an unverified response
	// carries.
	Verdict Verdict `json:"verdict"`

	// Prescription is present only on a verified response.
	Prescription *PublicPrescription `json:"prescription,omitempty"`

	// MessageEN and MessageBN are what the page says, composed here rather than on the client,
	// so that the sentence a stranger reads about a prescription that does not verify is the
	// same sentence everywhere and is not a client's paraphrase.
	MessageEN string `json:"message_en"`
	MessageBN string `json:"message_bn"`
}

// PublicPrescription is the minimum-necessary block.
type PublicPrescription struct {
	// ID is on the printed sheet already. Publishing it tells a holder of the paper nothing
	// they do not have, and is what lets them say which prescription they are asking about.
	ID string `json:"id"`
	// IssuedOnEN and IssuedOnBN are a **date**, in the clinic's own time zone, and not an
	// instant. The minute a prescription was signed would let two pieces of paper be ordered
	// against each other and correlated with a clinic's queue; the day is what a verification
	// needs.
	//
	// # Why two fields, and why they are words rather than `2026-09-14`
	//
	// This page is read by a stranger holding paper — a pharmacist at a counter, or the patient
	// — and ISO is a **storage** format. It is unambiguous, which is why it is stored, and it is
	// also the format in which nobody says a date aloud. CP83 shipped the same defect in three
	// places (`in the last 1 year`, `no HBA1C`, `obs.ldl:2026-09-01`) and the answer was
	// `internal/clinicalterm`, which owns the question *what does this look like to a person
	// reading it, in the language they are reading*. This calls that rather than formatting
	// here: a second date formatter is a second one to drift, and the one that drifts is always
	// the copy on the page nobody opens.
	//
	// Two fields for the same reason the physician's and the clinic's names are two: **a stranger
	// has no stored language preference**, so the page draws both and the reader takes the one
	// they read. A single date chosen by a locale we do not know would be chosen wrongly for
	// exactly the reader most likely to want the other one.
	IssuedOnEN string `json:"issued_on_en"`
	IssuedOnBN string `json:"issued_on_bn"`

	PhysicianNameEN string `json:"physician_name_en"`
	PhysicianNameBN string `json:"physician_name_bn"`

	FacilityNameEN string `json:"facility_name_en"`
	FacilityNameBN string `json:"facility_name_bn"`

	// ItemCount is how many medicines are on the sheet. A count and never a list: "does the
	// paper in my hand have the number of lines this clinic issued" is answerable from it, and
	// nothing about what they are is.
	ItemCount int `json:"item_count"`
}

// Dates renders a day the way a person reads it, in each language.
//
// # Why this is an interface rather than a call to `clinicalterm`
//
// `internal/clinicalterm` is the package that owns *what does this look like to a person reading
// it* — the one CP83's three machine-shaped strings were fixed with, and the obvious thing to
// call here. **`architecture.json` does not list it among signing's imports**, and that file's own
// header says changing a rule requires an ADR, which is the point of the file. So the fact
// crosses an interface this package declares and the composition root satisfies, exactly as
// station 10's clearance does through `signingClearanceBridge` — the pattern is four lines away
// and it exists for the same reason.
//
// It is deliberately about **dates and nothing else**. A wider "renderer" seam here would be an
// invitation to move clinical vocabulary onto a public page, which is the one page in this system
// that must carry none.
type Dates interface {
	// EN and BN render the same day. Two methods rather than one taking a language, because a
	// caller that has to choose is a caller that can choose wrongly — and this page draws both,
	// for a reader who told us no language.
	EN(time.Time) string
	BN(time.Time) string
}

// PublicHandlers serve the unauthenticated endpoint.
type PublicHandlers struct {
	service *Service
	store   *Store
	sheets  Sheets
	dates   Dates
	clock   interface{ Now() time.Time }
	logger  *slog.Logger
}

// PublicHandlersConfig builds them.
type PublicHandlersConfig struct {
	Service *Service
	Store   *Store
	Sheets  Sheets
	// Dates renders the issue date. **Required**: see [NewPublicHandlers].
	Dates  Dates
	Clock  interface{ Now() time.Time }
	Logger *slog.Logger
}

// NewPublicHandlers builds them, and refuses to build them without a date renderer.
//
// An error at wiring rather than a fallback at request time, for the reason `NewRouter` gives
// about the rate limiter: this is the system's only unauthenticated surface and a deployment
// mistake on it must look like one **before** it serves a request rather than after. The two
// fallbacks a nil renderer invites are both worse than not starting — an ISO date would put back
// the machine-shaped string this seam exists to remove, quietly, on the page a stranger reads;
// and refusing every verification would tell a pharmacist holding a genuine sheet that it may not
// be real, which is the failure this checkpoint spent its whole review eliminating.
func NewPublicHandlers(cfg PublicHandlersConfig) (*PublicHandlers, error) {
	if cfg.Dates == nil {
		return nil, errors.New("signing: the public verification page needs a date renderer; " +
			"a date on it is read by a stranger holding paper and must not be ISO")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &PublicHandlers{service: cfg.Service, store: cfg.Store, sheets: cfg.Sheets,
		dates: cfg.Dates, clock: cfg.Clock, logger: logger}, nil
}

func (h *PublicHandlers) now() time.Time {
	if h.clock == nil {
		return time.Now().UTC()
	}
	return h.clock.Now().UTC()
}

// Mount wires the public route.
//
// `httpx.Public()` is the declaration, which is what keeps `AuditRoutes` from refusing to start:
// a route registered any other way is found at boot and the process does not serve. Declaring it
// public is a decision in a diff rather than an omission.
func (h *PublicHandlers) Mount(r chi.Router) {
	r.Method("GET", "/{token}", httpx.Declare(httpx.Public(), h.verify))
}

// verify answers one scan.
func (h *PublicHandlers) verify(w http.ResponseWriter, r *http.Request) {
	presented := chi.URLParam(r, "token")
	digest := TokenDigest(presented)
	client := clientDigest(clientAddress(r))

	// `Cache-Control: no-store` and `X-Frame-Options: DENY` are **not set here**. They are
	// `httpx.PublicSurfaceHeaders`, applied to the whole prefix above the rate limiter, because
	// a handler can only set headers on the responses it produces: set here, they covered the two
	// verdicts and missed the 429, which left one framable response shape on the public surface.
	// One owner for the headers, and it is the route.

	if !WellFormedToken(presented) {
		// Answered without a database round trip, and with exactly the response an unknown
		// token gets. The only thing this shortcut changes is our own load.
		h.record(r.Context(), nil, digest, string(VerdictNotVerified), client)
		h.write(w, notVerified())
		return
	}

	resolved, err := h.store.ByTokenDigest(r.Context(), digest)
	if errors.Is(err, ErrNotSigned) {
		h.record(r.Context(), nil, digest, string(VerdictNotVerified), client)
		h.write(w, notVerified())
		return
	}
	if err != nil {
		// A database fault answers the same as an unknown token. A 500 on this endpoint would
		// be a signal — "that token did something different" — and the stranger's question is
		// better answered by "we cannot confirm this" than by a stack trace's HTTP status.
		h.logger.ErrorContext(r.Context(), "the public verification endpoint could not read a signature",
			"error", err.Error())
		h.write(w, notVerified())
		return
	}

	sheet, err := h.sheets.ByID(r.Context(), resolved.Signature.PrescriptionID, resolved.Signature.FacilityID)
	if err != nil {
		h.record(r.Context(), &resolved, digest, string(VerdictNotVerified), client)
		h.write(w, notVerified())
		return
	}
	verification, err := h.service.verifyAgainst(sheet, resolved.Signature)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "the public verification endpoint could not verify a signature",
			"error", err.Error())
		h.record(r.Context(), &resolved, digest, string(VerdictNotVerified), client)
		h.write(w, notVerified())
		return
	}
	if verification.Verdict != VerdictVerified {
		// **A tampered prescription shows as unverified, and shows nothing else.** Criterion 5.
		// The block is withheld rather than shown with a warning, because a page that printed a
		// physician's name beside "this has been altered" would be publishing an accusation
		// about a named colleague to anybody holding a forged piece of paper.
		h.record(r.Context(), &resolved, digest, string(VerdictNotVerified), client)
		h.write(w, notVerified())
		return
	}

	h.record(r.Context(), &resolved, digest, string(VerdictVerified), client)
	h.write(w, PublicVerification{
		Verdict: VerdictVerified,
		Prescription: &PublicPrescription{
			ID: resolved.Signature.PrescriptionID.String(),
			// Through the [Dates] seam, which the composition root satisfies with
			// `clinicalterm`. Not `time.Format`: Go's layout has no Bengali, and this page is
			// read in two languages by somebody who told us neither.
			IssuedOnEN:      h.dates.EN(resolved.IssuedOn),
			IssuedOnBN:      h.dates.BN(resolved.IssuedOn),
			PhysicianNameEN: resolved.PhysicianNameEN,
			PhysicianNameBN: resolved.PhysicianNameBN,
			FacilityNameEN:  resolved.FacilityNameEN,
			FacilityNameBN:  resolved.FacilityNameBN,
			ItemCount:       resolved.ItemCount,
		},
		MessageEN: "This prescription was issued by this clinic and has not been altered since " +
			"it was signed.",
		MessageBN: "এই ব্যবস্থাপত্রটি এই ক্লিনিক থেকে দেওয়া হয়েছে এবং স্বাক্ষরের পর এতে কোনো " +
			"পরিবর্তন করা হয়নি।",
	})
}

// notVerified is the single response every failure produces.
//
// One constructor, so that no future branch can accidentally add a field to one failure path and
// not the others — which is exactly how an oracle gets built, one helpful detail at a time.
func notVerified() PublicVerification {
	return PublicVerification{
		Verdict: VerdictNotVerified,
		MessageEN: "This code could not be verified. It may have been mistyped or damaged, or " +
			"the prescription may not be genuine. Ask the clinic before acting on it.",
		MessageBN: "এই কোডটি যাচাই করা যায়নি। এটি ভুল উঠে থাকতে পারে বা নষ্ট হয়ে থাকতে পারে, " +
			"অথবা ব্যবস্থাপত্রটি আসল না-ও হতে পারে। এটি অনুযায়ী কিছু করার আগে ক্লিনিকে জিজ্ঞাসা করুন।",
	}
}

func (h *PublicHandlers) write(w http.ResponseWriter, body PublicVerification) {
	// 200 for both verdicts. The verdict is in the body: an HTTP status that differed would put
	// the answer somewhere a cache, a proxy and an access log all see, and would make the two
	// cases distinguishable to anything sitting between the phone and this process.
	httpx.WriteJSON(w, http.StatusOK, body)
}

// record writes the attempt, and never fails the request.
//
// The prescription id comes from `resolved`, which came from the database, and never from the
// request. That is the whole defence against this table becoming a linkage record of who scanned
// which patient's prescription.
func (h *PublicHandlers) record(ctx context.Context, resolved *Resolved, digest []byte,
	outcome string, client []byte) {

	attempt := Attempt{
		PresentedDigest: digest, Outcome: outcome, ClientDigest: client, At: h.now(),
	}
	if resolved != nil {
		facility := resolved.Signature.FacilityID
		id := resolved.Signature.PrescriptionID
		attempt.FacilityID = &facility
		attempt.PrescriptionID = &id
	}
	if err := h.store.RecordAttempt(ctx, attempt); err != nil {
		// Logged and swallowed. A verification that succeeded must not be reported as failed
		// because a log write did not land — and a caller must not be able to learn anything
		// from the difference.
		h.logger.ErrorContext(ctx, "a prescription verification attempt was not recorded",
			"error", err.Error(), "outcome", outcome)
	}
}

// clientAddress is the address to charge and to fingerprint.
//
// `RemoteAddr` and **not** `X-Forwarded-For`. A header is whatever the caller wrote in it, and a
// rate limiter keyed on one is a rate limiter with an unlimited number of budgets. When this
// clinic puts a reverse proxy in front of the API, the proxy's trusted-header configuration is
// what changes and it changes in one place; until then, believing a header would be a limiter
// that looks present and does nothing.
func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
