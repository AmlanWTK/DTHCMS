package prescription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The printed sheet, as data (CP81 criterion 5, and the contract CP89 renders).
//
// # The problem this solves, stated before the solution
//
// CP81 must show "a live preview of the printed output" and CP89 must print the sheet, and the
// two are written months apart by people looking at different files. The obvious shape — the
// browser lays out a preview from the prescription JSON, and the print service lays out a PDF
// from the same JSON — gives two independent answers to every question the layout asks. What
// order are the lines in? Which instruction shows, the English or the Bangla or both? Does a
// removed line appear? What is printed for a medicine with no price? Each of those is a decision,
// and two programs making it separately agree right up until one of them is changed.
//
// So the contract is not "the same data". It is **the same resolved document**:
//
//	the server resolves the sheet into [PrintModel] — ordering, wording, both languages,
//	prices, what is omitted and why — and the preview and the printer both *render* it
//	without deciding anything.
//
// A renderer may choose type, spacing, page furniture and where the QR sits. It may not choose
// content, order, or wording. If CP89 finds it needs a fact this model does not carry, the fix is
// a field here and a version bump, not a lookup in the print service — because the moment the
// print service reads the prescription directly, the preview stops being a preview.
//
// # The three things that make this checkable rather than aspirational
//
//  1. **`Version`.** A renderer states the model version it was written against. CP89 pinning
//     `"1"` and the server emitting `"2"` is a loud failure rather than a quietly different sheet.
//  2. **`ContentHash`.** SHA-256 over the canonical model with `GeneratedAt` removed. Two
//     renderings of the same prescription carry the same hash whatever time it is, so
//     "the preview matches the print" is an equality between two strings a test can assert —
//     rather than a human comparing a screenshot with a piece of paper.
//  3. **Determinism.** Nothing in the model depends on the wall clock except `GeneratedAt`, and
//     that is the one field the hash excludes. Nothing depends on map iteration order. CP89's
//     own criterion 4 — the same prescription always renders identically — starts here.
//
// # What the model deliberately does not carry
//
//   - **No diagnosis.** CP80's `TestThePrescriptionPayloadCarriesNoDiagnosis` keeps the aggregate
//     free of one because §4.4 blinds the pharmacist, and a print model that reintroduced it
//     would put it back on a route the pharmacist holds. §9.1's ICD-coded diagnoses on the
//     printed sheet are CP89's to add **from the visit**, with its own redaction decision, and
//     this comment is where that hand-off is written down.
//   - **No total.** Deliberate, and inherited from CP80 §9: a sum over lines whose prices are
//     individually provisional looks like a bill. `PriceCaveat` says instead how many lines have
//     a price and how many of those nobody has verified.
//   - **No signature, no QR.** CP84 and CP85 own them. `Signature` is a block with a state and
//     no bytes, so the preview can show the space and say honestly that nothing is in it.
//   - **No graphs.** CP87.

// PrintModelVersion is the version of the document shape below.
//
// Bumped when a renderer written against the old shape would produce a wrong sheet rather than a
// merely plainer one. Adding an optional field is not a bump; changing what an existing field
// means is.
const PrintModelVersion = "1"

// PrintModel is the whole sheet.
type PrintModel struct {
	Version string `json:"version"`
	// GeneratedAt is when this rendering was produced. **Excluded from ContentHash**, and the
	// only field in the model that is.
	GeneratedAt time.Time `json:"generated_at"`
	// ContentHash is SHA-256 over the canonical model with GeneratedAt zeroed. Criterion 5 is
	// an equality between two of these.
	ContentHash string `json:"content_hash"`

	PrescriptionID uuid.UUID `json:"prescription_id"`
	Status         Status    `json:"status"`
	// StatusCaveatEN and StatusCaveatBN are what a reader must be told about a sheet in this
	// state. A draft printed for the patient's benefit is not a prescription, and the sheet has
	// to say so in the language of whoever picks it up.
	StatusCaveatEN string `json:"status_caveat_en"`
	StatusCaveatBN string `json:"status_caveat_bn"`

	Patient   PrintPatient   `json:"patient"`
	Lines     []PrintLine    `json:"lines"`
	Price     PrintPrice     `json:"price"`
	Signature PrintSignature `json:"signature"`

	// Omitted says what is on the prescription and not on the sheet, and why. A removed line is
	// the case that exists today. Carried rather than silently dropped because "why is there
	// nothing about the glimepiride" is a question somebody asks holding the paper.
	Omitted []PrintOmission `json:"omitted"`
}

// PrintPatient is the demographic block.
//
// Deliberately thin. The sheet identifies a person to a pharmacist and to the patient's family;
// it is not a copy of the register.
type PrintPatient struct {
	ClinicalID string `json:"clinical_id"`
	NameEN     string `json:"name_en"`
	NameBN     string `json:"name_bn,omitempty"`
	// SexEN and SexBN are rendered words rather than a code, because the renderer must not own
	// the vocabulary.
	SexEN string `json:"sex_en"`
	SexBN string `json:"sex_bn"`
	// AgeYears is a whole number of years, and AgeTextEN/BN are how it is said. Both, because
	// "41 years" and "৪১ বছর" differ by more than the digits when the birth date is imprecise.
	AgeYears  *int      `json:"age_years,omitempty"`
	AgeTextEN string    `json:"age_text_en"`
	AgeTextBN string    `json:"age_text_bn"`
	WrittenOn string    `json:"written_on"`
	VisitID   uuid.UUID `json:"visit_id"`
	Resolved  bool      `json:"resolved"`
	// UnresolvedNoteEN/BN say why the block is empty when it is. A preview that showed blank
	// fields would let a physician believe the printed sheet will carry a name.
	UnresolvedNoteEN string `json:"unresolved_note_en,omitempty"`
	UnresolvedNoteBN string `json:"unresolved_note_bn,omitempty"`
}

// PrintLine is one medicine as it appears on the sheet.
//
// Every string here is final. A renderer concatenates nothing and translates nothing.
type PrintLine struct {
	Ordinal int       `json:"ordinal"`
	ItemID  uuid.UUID `json:"item_id"`

	// Medicine is the bold line: the trade name and strength as the physician chose them.
	Medicine string `json:"medicine"`
	// Generic is the molecule, printed under the brand so that a pharmacist substituting knows
	// what he is substituting.
	Generic string `json:"generic,omitempty"`
	Form    string `json:"form,omitempty"`

	// DirectionsEN and DirectionsBN are the whole of "how to take it" as one sentence each —
	// dose, frequency, duration and route, already joined. This is the field CP89 must not
	// rebuild: the join is where an English comma ends up inside a Bengali sentence.
	DirectionsEN string `json:"directions_en"`
	DirectionsBN string `json:"directions_bn"`

	// InstructionEN and InstructionBN are the patient instruction, when there is one.
	InstructionEN string `json:"instruction_en,omitempty"`
	InstructionBN string `json:"instruction_bn,omitempty"`

	// PriceText is what this line costs, already formatted, or empty for a medicine with no
	// price. **Empty, never "0.00"** — zero would tell a patient a medicine is free.
	PriceText string `json:"price_text,omitempty"`
	// PriceUnverified marks a price nobody at this clinic has checked.
	PriceUnverified bool `json:"price_unverified"`
}

// PrintPrice is what the sheet may say about money, and what it may not.
type PrintPrice struct {
	LinesWithPrice   int `json:"lines_with_price"`
	LinesNoPrice     int `json:"lines_no_price"`
	LinesProvisional int `json:"lines_provisional"`
	// CaveatEN and CaveatBN are the sentence printed under the list when any price is missing
	// or unverified. Empty when every line carries a checked price.
	CaveatEN string `json:"caveat_en,omitempty"`
	CaveatBN string `json:"caveat_bn,omitempty"`
}

// PrintSignature is the space CP84 will fill.
type PrintSignature struct {
	Signed bool `json:"signed"`
	// NoteEN and NoteBN say what is in the space. On an unsigned sheet they say that nothing is.
	NoteEN string `json:"note_en"`
	NoteBN string `json:"note_bn"`
}

// PrintOmission is something on the prescription that is not on the sheet.
type PrintOmission struct {
	ItemID   uuid.UUID `json:"item_id"`
	Medicine string    `json:"medicine"`
	ReasonEN string    `json:"reason_en"`
	ReasonBN string    `json:"reason_bn"`
}

// PatientHeader supplies the demographics the sheet carries.
//
// A port rather than an import: `prescription` may not import `patient` (architecture.json), and
// the restriction is worth keeping. What crosses into this module is five display strings, so a
// prescription's print model cannot grow a national identifier by somebody adding a field to the
// patient record.
//
// Unwired, or a patient this reader cannot see, produces an **unresolved** header rather than an
// error: the preview is still worth showing, and it says in both languages that the name block
// could not be filled.
type PatientHeader interface {
	PrescriptionHeader(ctx context.Context, facility, patient uuid.UUID) (HeaderFacts, error)
}

// HeaderFacts is what the bridge hands over. Display strings only, no identifiers.
type HeaderFacts struct {
	ClinicalID string
	NameEN     string
	NameBN     string
	// Sex is the patient module's own word — "male", "female", "other". Translated here rather
	// than in the bridge, so the vocabulary lives with the sheet that prints it.
	Sex string
	// AgeYears is nil when the birth date is not usable.
	AgeYears *int
}

// PrintModelOf assembles the sheet.
//
// Pure apart from the two lookups it is handed, and deterministic given them — which is what
// makes the hash worth anything.
func PrintModelOf(sheet Prescription, facts HeaderFacts, resolved bool,
	now time.Time) PrintModel {

	model := PrintModel{
		Version:        PrintModelVersion,
		GeneratedAt:    now.UTC(),
		PrescriptionID: sheet.ID,
		Status:         sheet.Status,
		Patient:        printPatient(sheet, facts, resolved),
		Lines:          []PrintLine{},
		Omitted:        []PrintOmission{},
	}
	model.StatusCaveatEN, model.StatusCaveatBN = statusCaveat(sheet.Status)
	model.Signature = signatureBlock(sheet)

	live := make([]Item, 0, len(sheet.Items))
	for _, item := range sheet.Items {
		if item.Live() {
			live = append(live, item)
			continue
		}
		model.Omitted = append(model.Omitted, PrintOmission{
			ItemID: item.ID, Medicine: medicineOf(item),
			ReasonEN: "Taken off this prescription before it was printed.",
			ReasonBN: "ছাপার আগেই এই ব্যবস্থাপত্র থেকে বাদ দেওয়া হয়েছে।",
		})
	}
	// Line number first, then the instant it was written, then the id. Three keys because the
	// first two can tie — every line CP80 writes today carries the same line_no of 0 unless a
	// client sets one — and a sheet whose order changed between the preview and the paper is
	// the exact failure criterion 5 is about.
	sort.SliceStable(live, func(i, j int) bool {
		if live[i].LineNo != live[j].LineNo {
			return live[i].LineNo < live[j].LineNo
		}
		if !live[i].RecordedAt.Equal(live[j].RecordedAt) {
			return live[i].RecordedAt.Before(live[j].RecordedAt)
		}
		return live[i].ID.String() < live[j].ID.String()
	})
	sort.SliceStable(model.Omitted, func(i, j int) bool {
		return model.Omitted[i].ItemID.String() < model.Omitted[j].ItemID.String()
	})

	for i, item := range live {
		line := PrintLine{
			Ordinal: i + 1, ItemID: item.ID,
			Medicine: medicineOf(item), Generic: item.GenericName, Form: item.FormCode,
		}
		line.DirectionsEN, line.DirectionsBN = directions(item)
		line.InstructionEN, line.InstructionBN = instruction(item)
		if item.Price != nil && item.Price.AmountPoisha > 0 {
			line.PriceText = item.Price.AmountBDT
			line.PriceUnverified = item.Price.Verification != "VERIFIED"
			model.Price.LinesWithPrice++
			if line.PriceUnverified {
				model.Price.LinesProvisional++
			}
		} else {
			model.Price.LinesNoPrice++
		}
		model.Lines = append(model.Lines, line)
	}
	model.Price.CaveatEN, model.Price.CaveatBN = priceCaveat(model.Price)
	model.ContentHash = hashOf(model)
	return model
}

func medicineOf(item Item) string {
	label := strings.TrimSpace(item.Label)
	if strength := strings.TrimSpace(item.Strength); strength != "" {
		return label + " " + strength
	}
	return label
}

// directions joins dose, frequency, duration and route into one sentence per language.
//
// **The join is the point.** English puts the dose first and the duration last; Bengali puts the
// duration first and the verb last, and a renderer that inserted an English comma into a Bengali
// sentence would produce something a patient reads twice. Doing it here means it is done once.
//
// The dose and the frequency are the physician's own words, captured on the item. They stay in
// the script they were written in — a dose transliterated into Bengali numerals is a dose
// somebody transcribes wrongly, which is the design system's rule for every clinical value.
func directions(item Item) (string, string) {
	dose := strings.TrimSpace(item.Dose)
	freq := strings.TrimSpace(item.Frequency)

	en := make([]string, 0, 4)
	if dose != "" {
		en = append(en, dose)
	}
	if freq != "" {
		en = append(en, freq)
	}
	if item.DurationDays != nil {
		en = append(en, "for "+strconv.Itoa(*item.DurationDays)+" days")
	}
	if route := strings.TrimSpace(item.Route); route != "" && !strings.EqualFold(route, "oral") {
		en = append(en, "by the "+route+" route")
	}

	bn := make([]string, 0, 4)
	if dose != "" {
		bn = append(bn, dose)
	}
	if freq != "" {
		bn = append(bn, frequencyBN(freq))
	}
	if item.DurationDays != nil {
		bn = append(bn, strconv.Itoa(*item.DurationDays)+" দিন")
	}
	if route := strings.TrimSpace(item.Route); route != "" && !strings.EqualFold(route, "oral") {
		bn = append(bn, routeBN(route))
	}
	// The Bengali verb, and it is not one verb.
	//
	// "খাবেন" is *will eat*, which is right for a tablet and wrong for an insulin pen. A single
	// hard-coded verb printed "1 tablet · দিনে দুইবার — খাবেন" under Glarine 100 IU/mL on the
	// first sheet this preview rendered, which is an instruction to swallow an injection. So the
	// verb follows the route: oral eats, everything else takes.
	bengali := strings.Join(bn, " · ")
	if bengali != "" {
		bengali += " — " + verbBN(item.Route)
	}
	return strings.Join(en, ", "), bengali
}

// frequencyBN says a frequency in Bengali, and says the English back when it does not know it.
//
// The fallback is deliberate and it is the honest one. `frequency` is free text on the item
// because the set of real frequencies is open (CP80), so a physician may type "every third day"
// and no table here will have it. Printing the English phrase inside the Bengali line is worse
// than a translation and better than a guess: a patient shown a phrase in the wrong script asks
// what it says, and a patient shown a confident mistranslation does not.
//
// The small vocabulary below is the one migration 00064 seeds defaults in, which is what makes it
// cover the ordinary case. **Anything a physician types by hand falls through**, and CP91 is
// where a real bilingual instruction library lands.
func frequencyBN(freq string) string {
	switch strings.ToLower(strings.TrimSpace(freq)) {
	case "once daily":
		return "দিনে একবার"
	case "twice daily":
		return "দিনে দুইবার"
	case "three times daily":
		return "দিনে তিনবার"
	case "four times daily":
		return "দিনে চারবার"
	case "once weekly":
		return "সপ্তাহে একবার"
	case "at night", "once daily at night":
		return "রাতে একবার"
	case "as needed":
		return "প্রয়োজনমতো"
	}
	return freq
}

// verbBN is how the sentence ends.
//
// Oral is the default because an empty route is overwhelmingly a tablet at this clinic — but the
// default is stated here rather than assumed, so that adding a route means visiting this switch.
func verbBN(route string) string {
	switch strings.ToLower(strings.TrimSpace(route)) {
	case "", "oral":
		return "খাবেন"
	case "subcutaneous", "intramuscular", "intravenous":
		return "নেবেন"
	case "topical":
		return "লাগাবেন"
	case "inhaled":
		return "নেবেন"
	}
	return "নেবেন"
}

func routeBN(route string) string {
	switch strings.ToLower(strings.TrimSpace(route)) {
	case "subcutaneous":
		return "চামড়ার নিচে"
	case "intramuscular":
		return "মাংসপেশিতে"
	case "topical":
		return "ত্বকে"
	case "inhaled":
		return "শ্বাসের সঙ্গে"
	}
	return route
}

// instruction is the patient sentence, taken from the item and from nowhere else.
//
// A template is copied onto the item at the moment it is chosen (CP81's editor), so what the
// prescription carries is the sentence rather than a reference to one. That is deliberate: a
// prescription that pointed at `core.instruction_template` would say something different the day
// somebody edited the template, and this sheet is a medico-legal record of what a patient was
// told on a particular afternoon.
func instruction(item Item) (string, string) {
	return strings.TrimSpace(item.InstructionsEN), strings.TrimSpace(item.InstructionsBN)
}

func printPatient(sheet Prescription, facts HeaderFacts, resolved bool) PrintPatient {
	out := PrintPatient{
		VisitID:   sheet.VisitID,
		WrittenOn: sheet.CreatedAt.UTC().Format("2006-01-02"),
		Resolved:  resolved,
	}
	if !resolved {
		out.UnresolvedNoteEN = "The patient's name and age could not be read for this preview. " +
			"The printed sheet will not carry them either until this is fixed."
		out.UnresolvedNoteBN = "এই প্রাকদর্শনের জন্য রোগীর নাম ও বয়স পড়া যায়নি। এটি ঠিক না হওয়া " +
			"পর্যন্ত ছাপা কাগজেও সেগুলো থাকবে না।"
		return out
	}
	out.ClinicalID = facts.ClinicalID
	out.NameEN = facts.NameEN
	out.NameBN = facts.NameBN
	out.SexEN, out.SexBN = sexWords(facts.Sex)
	out.AgeYears = facts.AgeYears
	if facts.AgeYears != nil {
		out.AgeTextEN = strconv.Itoa(*facts.AgeYears) + " years"
		out.AgeTextBN = strconv.Itoa(*facts.AgeYears) + " বছর"
	} else {
		out.AgeTextEN = "age not recorded"
		out.AgeTextBN = "বয়স লেখা নেই"
	}
	return out
}

func sexWords(sex string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(sex)) {
	case "male":
		return "Male", "পুরুষ"
	case "female":
		return "Female", "নারী"
	case "other":
		return "Other", "অন্যান্য"
	}
	return "Not recorded", "লেখা নেই"
}

// statusCaveat is what the sheet must say about its own state.
//
// A draft is the one that matters. A printed draft looks exactly like a prescription to a
// pharmacist, and the only thing standing between the two is this sentence — so it is in the
// model rather than left to a renderer to remember.
func statusCaveat(s Status) (string, string) {
	switch s {
	case StatusDraft:
		return "DRAFT — not signed. This is not a prescription and must not be dispensed against.",
			"খসড়া — স্বাক্ষরিত নয়। এটি ব্যবস্থাপত্র নয় এবং এর ভিত্তিতে ওষুধ দেওয়া যাবে না।"
	case StatusQAReview:
		return "Awaiting quality review — not signed. Not to be dispensed against.",
			"মান-যাচাইয়ের অপেক্ষায় — স্বাক্ষরিত নয়। এর ভিত্তিতে ওষুধ দেওয়া যাবে না।"
	case StatusCancelled:
		return "CANCELLED. This prescription has been withdrawn and must not be dispensed against.",
			"বাতিল। এই ব্যবস্থাপত্রটি প্রত্যাহার করা হয়েছে, এর ভিত্তিতে ওষুধ দেওয়া যাবে না।"
	case StatusCorrected:
		return "SUPERSEDED. A correction replaces this prescription; ask for the current sheet.",
			"প্রতিস্থাপিত। একটি সংশোধনী এই ব্যবস্থাপত্রের জায়গা নিয়েছে; বর্তমান কাগজটি চেয়ে নিন।"
	}
	return "", ""
}

func signatureBlock(sheet Prescription) PrintSignature {
	if sheet.SignedAt != nil {
		return PrintSignature{
			Signed: true,
			NoteEN: "Signed electronically.",
			NoteBN: "ইলেকট্রনিকভাবে স্বাক্ষরিত।",
		}
	}
	return PrintSignature{
		Signed: false,
		NoteEN: "Not signed. Signing arrives with CP84; nothing stands in this space yet.",
		NoteBN: "স্বাক্ষরিত নয়। স্বাক্ষরের ব্যবস্থা CP84-এ আসবে; এই জায়গায় এখনও কিছু নেই।",
	}
}

func priceCaveat(p PrintPrice) (string, string) {
	switch {
	case p.LinesNoPrice > 0 && p.LinesProvisional > 0:
		return itoa(p.LinesNoPrice) + " medicine(s) have no price on file and " +
				itoa(p.LinesProvisional) + " carry a price nobody at this clinic has checked. " +
				"Ask the pharmacy what these cost.",
			itoa(p.LinesNoPrice) + "টি ওষুধের দাম নথিতে নেই এবং " + itoa(p.LinesProvisional) +
				"টির দাম এই ক্লিনিকের কেউ যাচাই করেননি। দাম ফার্মেসিতে জেনে নিন।"
	case p.LinesNoPrice > 0:
		return itoa(p.LinesNoPrice) + " medicine(s) have no price on file. Ask the pharmacy " +
				"what these cost.",
			itoa(p.LinesNoPrice) + "টি ওষুধের দাম নথিতে নেই। দাম ফার্মেসিতে জেনে নিন।"
	case p.LinesProvisional > 0:
		return itoa(p.LinesProvisional) + " price(s) here have not been checked by this clinic " +
				"and may be out of date.",
			"এখানে " + itoa(p.LinesProvisional) + "টি দাম এই ক্লিনিক যাচাই করেনি, সেগুলো পুরনো হতে পারে।"
	}
	return "", ""
}

func itoa(n int) string { return strconv.Itoa(n) }

// hashOf is the equality criterion 5 is proved with.
//
// `encoding/json` marshals a struct in field-declaration order and a map in sorted key order, so
// the bytes are stable for a given model. `GeneratedAt` is zeroed rather than omitted, because
// omitting it would make the hash of a model with the field absent differ from one where it was
// present and empty — and a hash that depends on how it was built is not a hash of the content.
func hashOf(model PrintModel) string {
	model.GeneratedAt = time.Time{}
	model.ContentHash = ""
	encoded, err := json.Marshal(model)
	if err != nil {
		// Unreachable: every field is a plain type. Returning an empty hash rather than
		// panicking, because an unhashable preview is still a preview worth showing and the
		// comparison that uses it will fail loudly against an empty string.
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
