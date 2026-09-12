package prescription_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
)

// The editor's content and the printed sheet (CP81).
//
// Each test here is written against the way the thing it checks would actually fail, rather than
// against the fact that it exists:
//
//   - A suggestion that looked approved is the failure the whole defaults model exists to
//     prevent, so the test asserts the *absence* of an approval on every seeded row and then
//     asserts that approving one and editing it takes the approval away again.
//   - A preview that disagrees with the print is the failure criterion 5 exists to prevent, so
//     the test renders the same prescription twice at different instants and compares hashes —
//     which a test that only checked "the model has lines in it" would not catch.
//   - A draft that printed without saying it is a draft is the failure that reaches a pharmacy
//     counter, so it is asserted in both languages.

// ---------------------------------------------------------------------------
// The suggestions
// ---------------------------------------------------------------------------

func TestEverySeededPrescribingDefaultIsApprovedByNobodyAndCitesItsSource(t *testing.T) {
	r := newRig(t)

	defaults, err := r.store.PrescribingDefaults(r.ctx(), r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaults) == 0 {
		t.Fatal("migration 00064 seeded no prescribing defaults; the editor has nothing to offer")
	}
	for _, d := range defaults {
		if d.Approval.Approved {
			t.Errorf("%s %s arrives approved; nobody at this clinic has read it",
				d.GenericName, d.Strength)
		}
		if d.Approval.Origin != "SEED" {
			t.Errorf("%s %s claims origin %q", d.GenericName, d.Strength, d.Approval.Origin)
		}
		if strings.TrimSpace(d.Approval.SourceCitation) == "" {
			t.Errorf("%s %s cites nothing", d.GenericName, d.Strength)
		}
		if strings.TrimSpace(d.RationaleEN) == "" || strings.TrimSpace(d.RationaleBN) == "" {
			t.Errorf("%s %s has no bilingual rationale", d.GenericName, d.Strength)
		}
		if strings.TrimSpace(d.FrequencyBN) == "" {
			t.Errorf("%s %s has no Bengali frequency", d.GenericName, d.Strength)
		}
	}
}

func TestEverySeededInstructionIsBilingualAndApprovedByNobody(t *testing.T) {
	r := newRig(t)

	templates, err := r.store.InstructionTemplates(r.ctx(), r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) == 0 {
		t.Fatal("migration 00064 seeded no instruction templates")
	}
	for _, tpl := range templates {
		if tpl.Approval.Approved {
			t.Errorf("%s arrives approved", tpl.Code)
		}
		for name, value := range map[string]string{
			"text_en": tpl.TextEN, "text_bn": tpl.TextBN,
			"label_en": tpl.LabelEN, "label_bn": tpl.LabelBN,
		} {
			if strings.TrimSpace(value) == "" {
				t.Errorf("%s has an empty %s", tpl.Code, name)
			}
		}
		// The one failure a key-set comparison cannot see: English copied into the Bengali
		// field. Checked by script rather than by inequality, because two different English
		// sentences are still two English sentences.
		if !hasBengali(tpl.TextBN) {
			t.Errorf("%s: the Bengali instruction has no Bengali in it: %q", tpl.Code, tpl.TextBN)
		}
		if !hasBengali(tpl.LabelBN) {
			t.Errorf("%s: the Bengali label has no Bengali in it: %q", tpl.Code, tpl.LabelBN)
		}
	}
}

func TestApprovingADefaultAndThenEditingItTakesTheApprovalAway(t *testing.T) {
	r := newRig(t)

	defaults, err := r.store.PrescribingDefaults(r.ctx(), r.facility)
	if err != nil {
		t.Fatal(err)
	}
	target := defaults[0]

	approval, err := r.store.ApproveDefault(r.ctx(), r.facility, target.ID, r.user, r.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !approval.Approved {
		t.Fatal("approving a default did not approve it")
	}

	// The edit is a plain UPDATE, which is what a future admin screen will issue. The
	// guarantee has to hold against that rather than against a handler being careful.
	if _, err := r.SQL.Exec(
		`UPDATE core.prescribing_default SET dose = '2 tablets' WHERE id = $1`, target.ID); err != nil {
		t.Fatal(err)
	}

	after, err := r.store.PrescribingDefaults(r.ctx(), r.facility)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range after {
		if d.ID != target.ID {
			continue
		}
		if d.Approval.Approved {
			t.Fatal("the dose changed after approval and the row still says a physician " +
				"approved it — he approved a sentence, not a row id")
		}
		if d.Dose != "2 tablets" {
			t.Fatalf("the edit did not land: dose is %q", d.Dose)
		}
		return
	}
	t.Fatal("the default disappeared")
}

func TestApprovingTwiceKeepsTheFirstPhysiciansName(t *testing.T) {
	r := newRig(t)

	defaults, err := r.store.PrescribingDefaults(r.ctx(), r.facility)
	if err != nil {
		t.Fatal(err)
	}
	target := defaults[0]
	first, err := r.store.ApproveDefault(r.ctx(), r.facility, target.ID, r.user, r.clock.Now())
	if err != nil {
		t.Fatal(err)
	}

	other := uuid.New()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'DOC02', 'Second', 'দ্বিতীয়', 'active')`, other, r.facility); err != nil {
		t.Fatal(err)
	}
	second, err := r.store.ApproveDefault(r.ctx(), r.facility,
		target.ID, other, r.clock.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if first.ApprovedAt == nil || second.ApprovedAt == nil ||
		!first.ApprovedAt.Equal(*second.ApprovedAt) {
		t.Fatalf("the second approval re-stamped the row: %v then %v",
			first.ApprovedAt, second.ApprovedAt)
	}

	var name string
	if err := r.SQL.QueryRow(`
		SELECT u.name_en FROM core.prescribing_default d
		  JOIN core.app_user u ON u.id = d.approved_by WHERE d.id = $1`,
		target.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Nahid" {
		t.Fatalf("the name on the approval is %q; the person who read it is the person whose "+
			"name belongs on it", name)
	}
}

func TestTheDefaultsRouteSaysHowManyAreApproved(t *testing.T) {
	r := newRig(t)

	var body struct {
		Defaults []prescription.PrescribingDefault `json:"defaults"`
		Total    int                               `json:"total"`
		Approved int                               `json:"approved"`
	}
	r.getJSON(t, "/v1/prescribing-defaults", &body)
	if body.Total != len(body.Defaults) || body.Total == 0 {
		t.Fatalf("total %d against %d rows", body.Total, len(body.Defaults))
	}
	if body.Approved != 0 {
		t.Fatalf("%d defaults report as approved before anybody approved one", body.Approved)
	}
}

func TestResolvingASuggestionPrefersTheExactStrength(t *testing.T) {
	all := []prescription.PrescribingDefault{
		{GenericName: "Metformin hydrochloride", Strength: "500 mg", Dose: "1 tablet",
			Frequency: "twice daily"},
		{GenericName: "Metformin hydrochloride", Strength: "1000 mg", Dose: "1 tablet",
			Frequency: "twice daily", DoseUnit: "mg"},
		{GenericName: "Semaglutide", Strength: "", Dose: "1 pen dose", Frequency: "once weekly"},
	}

	got, ok := prescription.ResolveDefault(all, "Metformin hydrochloride", "1000 mg")
	if !ok || got.Strength != "1000 mg" {
		t.Fatalf("a 1000 mg tablet resolved to %q — half the dose, written confidently",
			got.Strength)
	}
	// The molecule-wide row is a fallback, not a competitor.
	got, ok = prescription.ResolveDefault(all, "Semaglutide", "1 mg/0.5 mL")
	if !ok || got.Dose != "1 pen dose" {
		t.Fatalf("the molecule-wide row did not answer a strength it does not name: %#v", got)
	}
	// A strength with no row of its own and no molecule-wide row gets **nothing**, rather
	// than the 500 mg row for a different tablet. This is the negative the whole function
	// exists for and it is the one a "first match on generic name" implementation fails.
	if got, ok := prescription.ResolveDefault(all, "Metformin hydrochloride", "850 mg"); ok {
		t.Fatalf("850 mg was answered with the %s suggestion", got.Strength)
	}
	if _, ok := prescription.ResolveDefault(all, "Linagliptin", "5 mg"); ok {
		t.Fatal("a molecule with no suggestion got one; an invented default is the machine " +
			"prescribing")
	}
}

// ---------------------------------------------------------------------------
// The printed sheet
// ---------------------------------------------------------------------------

func TestThePreviewAndThePrintAreTheSameDocument(t *testing.T) {
	r := newRig(t)
	sheet := r.draft(t)

	var first, second prescription.PrintModel
	r.getJSON(t, "/v1/prescriptions/"+sheet.ID.String()+"/print-model", &first)
	// A different instant. Everything that is not the clock must be identical, which is the
	// whole of CP89's criterion 4 and of this checkpoint's criterion 5.
	r.clock.Advance(90 * time.Minute)
	r.getJSON(t, "/v1/prescriptions/"+sheet.ID.String()+"/print-model", &second)

	if first.ContentHash == "" {
		t.Fatal("the model carries no content hash, so nothing can be compared against the print")
	}
	if first.ContentHash != second.ContentHash {
		t.Fatalf("two renderings of one prescription hashed differently:\n%s\n%s",
			first.ContentHash, second.ContentHash)
	}
	if first.GeneratedAt.Equal(second.GeneratedAt) {
		t.Fatal("generated_at did not move, so the test proved nothing about clock independence")
	}
	if first.Version != prescription.PrintModelVersion {
		t.Fatalf("model version %q", first.Version)
	}
}

func TestAPrintedDraftSaysInBothLanguagesThatItIsNotAPrescription(t *testing.T) {
	r := newRig(t)
	sheet := r.draft(t)

	var model prescription.PrintModel
	r.getJSON(t, "/v1/prescriptions/"+sheet.ID.String()+"/print-model", &model)

	if model.Status != prescription.StatusDraft {
		t.Fatalf("status %q", model.Status)
	}
	if !strings.Contains(strings.ToUpper(model.StatusCaveatEN), "DRAFT") ||
		!strings.Contains(model.StatusCaveatEN, "not a prescription") {
		t.Fatalf("the English caveat does not say this is not a prescription: %q",
			model.StatusCaveatEN)
	}
	if !hasBengali(model.StatusCaveatBN) {
		t.Fatalf("the Bengali caveat is not in Bengali: %q", model.StatusCaveatBN)
	}
	if model.Signature.Signed {
		t.Fatal("an unsigned draft reports a signature")
	}
	if !hasBengali(model.Signature.NoteBN) {
		t.Fatalf("the signature note is not bilingual: %q", model.Signature.NoteBN)
	}
}

func TestARemovedLineIsOffTheSheetAndSaysWhy(t *testing.T) {
	r := newRig(t)
	sheet := r.draft(t)

	if err := r.service.RemoveItem(r.ctx(), uuid.New(), sheet.ID, sheet.Items[0].ID,
		"Changed to linagliptin.", eventstore.SourceWeb); err != nil {
		t.Fatal(err)
	}

	var model prescription.PrintModel
	r.getJSON(t, "/v1/prescriptions/"+sheet.ID.String()+"/print-model", &model)
	if len(model.Lines) != 0 {
		t.Fatalf("a removed medicine is still on the sheet: %#v", model.Lines)
	}
	if len(model.Omitted) != 1 {
		t.Fatalf("the removal is not recorded as an omission: %#v", model.Omitted)
	}
	if !hasBengali(model.Omitted[0].ReasonBN) {
		t.Fatalf("the omission reason is not bilingual: %q", model.Omitted[0].ReasonBN)
	}
}

func TestTheDirectionsAreOneSentencePerLanguageAndNotAJoinTheRendererMakes(t *testing.T) {
	r := newRig(t)
	sheet := r.draft(t)

	var model prescription.PrintModel
	r.getJSON(t, "/v1/prescriptions/"+sheet.ID.String()+"/print-model", &model)
	if len(model.Lines) != 1 {
		t.Fatalf("expected one line, got %d", len(model.Lines))
	}
	line := model.Lines[0]
	for _, want := range []string{"1 tablet", "twice daily", "30 days"} {
		if !strings.Contains(line.DirectionsEN, want) {
			t.Errorf("the English directions do not carry %q: %q", want, line.DirectionsEN)
		}
	}
	if !hasBengali(line.DirectionsBN) {
		t.Fatalf("the Bengali directions are not in Bengali: %q", line.DirectionsBN)
	}
	if strings.Contains(line.DirectionsBN, "twice daily") {
		t.Fatalf("an English frequency leaked into the Bengali sentence: %q", line.DirectionsBN)
	}
	if !strings.Contains(line.Medicine, sheet.Items[0].Strength) {
		t.Fatalf("the medicine line does not carry the strength: %q", line.Medicine)
	}
}

func TestAPriceNobodyCheckedIsSaidToBeUncheckedRatherThanShownPlain(t *testing.T) {
	r := newRig(t)
	sheet := r.draft(t)

	var model prescription.PrintModel
	r.getJSON(t, "/v1/prescriptions/"+sheet.ID.String()+"/print-model", &model)
	if model.Price.LinesProvisional == 0 && model.Price.LinesNoPrice == 0 {
		t.Skip("this seeded product carries a verified price; nothing to assert")
	}
	if model.Price.CaveatEN == "" || model.Price.CaveatBN == "" {
		t.Fatalf("a sheet with an unchecked or missing price says nothing about it: %#v",
			model.Price)
	}
	if !hasBengali(model.Price.CaveatBN) {
		t.Fatalf("the price caveat is not bilingual: %q", model.Price.CaveatBN)
	}
}

// ---------------------------------------------------------------------------
// Small shared pieces
// ---------------------------------------------------------------------------

// hasBengali reports whether a string contains any Bengali codepoint.
//
// The check the bilingual guarantee actually needs. Comparing the two fields for inequality
// passes when somebody wrote two different English sentences, which is the failure that reaches
// a Bangla-reading patient.
func hasBengali(s string) bool {
	for _, r := range s {
		if r >= 0x0980 && r <= 0x09FF {
			return true
		}
	}
	return false
}

func (r *rig) getJSON(t *testing.T, path string, into any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, r.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("X-Requested-With", "DTHCMS")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d", path, res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(into); err != nil {
		t.Fatal(err)
	}
}

// TestAnInjectionIsNotToldToBeSwallowed is the defect the first rendered sheet showed.
//
// The Bengali directions ended in "খাবেন" — *will eat* — for every line, including an insulin
// pen. One hard-coded verb is the kind of thing that survives a translation review, because the
// sentence is grammatical and the reviewer is reading a tablet.
func TestAnInjectionIsNotToldToBeSwallowed(t *testing.T) {
	r := newRig(t)
	sheet, err := r.service.Create(r.ctx(), prescription.Creation{
		PatientID: r.patient, VisitID: r.visit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.service.AddItem(r.ctx(), prescription.Addition{
		PrescriptionID: sheet.ID, Label: "Glarine 100 IU/mL",
		Dose: "12 units", Frequency: "once daily", Route: "subcutaneous",
	}); err != nil {
		t.Fatal(err)
	}

	var model prescription.PrintModel
	r.getJSON(t, "/v1/prescriptions/"+sheet.ID.String()+"/print-model", &model)
	line := model.Lines[0]
	if strings.Contains(line.DirectionsBN, "খাবেন") {
		t.Fatalf("a subcutaneous injection is told to be eaten: %q", line.DirectionsBN)
	}
	if !strings.Contains(line.DirectionsBN, "নেবেন") {
		t.Fatalf("the Bengali directions have no verb: %q", line.DirectionsBN)
	}
	if !strings.Contains(line.DirectionsBN, "চামড়ার নিচে") {
		t.Fatalf("the Bengali directions do not say where it goes: %q", line.DirectionsBN)
	}
	if !strings.Contains(line.DirectionsEN, "subcutaneous") {
		t.Fatalf("the English directions do not carry the route: %q", line.DirectionsEN)
	}
}
