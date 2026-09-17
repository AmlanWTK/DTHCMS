package signing_test

import (
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/apispec"
	"github.com/AmlanWTK/DTHCMS/backend/internal/signing"
)

// The four schemas CP84 and CP85 added to the contract, checked against what the server actually
// serialises (CP84, CP85).
//
// # Why this test is not a YAML lint
//
// `pnpm run spec:lint` proves the document is a valid OpenAPI description. It cannot prove the
// description is *true*. A schema that names a field the server does not send, or omits one it
// does, passes every linter there is and produces a generated client whose types are fiction —
// and the failure lands on a screen, at the point where somebody reads `verification.verdict` and
// gets `undefined`.
//
// So the documented property set is read from the contract and the real one is read from
// `encoding/json` marshalling the types the handlers actually write, and the two must be the same
// set in both directions. A field added to a Go struct fails this test until somebody documents
// it; a property documented for a field nobody sends fails it until somebody deletes it.
//
// # Why every optional field is populated
//
// `omitempty` means an under-populated fixture would serialise a *subset* and the test would pass
// while the contract described fields the server can never produce. Each fixture below therefore
// carries a value in every field, including the pointers — the point is the key set, not the
// values.
//
// # The public schema is checked here and its *contents* are checked elsewhere
//
// [TestThePublicResponseCarriesOnlyWhatItIsAllowedTo] drives the live endpoint and asserts the
// response's key set against an allowlist, with the patient's own details asserted absent from
// the bytes. This test is the other half: that the published description of that response says
// the same thing, so a client generated from the contract cannot be written expecting a field a
// stranger will never be given.

const contractPath = "../../../api/openapi.yaml"

func TestTheSigningSchemasDescribeWhatTheServerSerialises(t *testing.T) {
	t.Parallel()

	doc, err := apispec.Load(contractPath)
	if err != nil {
		t.Fatalf("reading the contract: %v", err)
	}

	for _, one := range []struct {
		schema string
		value  any
	}{
		{"PrescriptionSignature", fullSignature()},
		{"SignatureVerification", signing.Verification{
			PrescriptionID:   uuid.New(),
			Verdict:          signing.VerdictNotVerified,
			Signature:        fullSignature(),
			RecomputedSHA256: "9f2c1f0e",
			ReasonEN:         "This prescription is not what it was when it was signed.",
			ReasonBN:         "স্বাক্ষরের সময় এই ব্যবস্থাপত্র যেমন ছিল, এখন তেমন নেই।",
		}},
		{"SignatureImageCaveat", signing.TheImageIsNotTheSignature()},
		{"SigningReadiness", signing.Readiness{
			MaySign: false, Signed: false, Cleared: false, Status: "QA_REVIEW",
			ReasonEN: "Station 10 has not cleared this prescription.",
			ReasonBN: "১০ নম্বর কেন্দ্র এই ব্যবস্থাপত্রে ছাড়পত্র দেয়নি।",
		}},
		{"PublicVerification", signing.PublicVerification{
			Verdict:      signing.VerdictVerified,
			Prescription: &signing.PublicPrescription{},
			MessageEN:    "This prescription was issued by this clinic.",
			MessageBN:    "এই ব্যবস্থাপত্রটি এই ক্লিনিক থেকে দেওয়া হয়েছে।",
		}},
	} {
		compareKeys(t, doc, "components.schemas."+one.schema+".properties", one.value)
	}

	// The nested block, which is the one a stranger sees. It is inline in the contract rather
	// than a component of its own, deliberately: nothing but the public response may ever
	// reference it, and a named component is a component somebody reuses.
	compareKeys(t, doc,
		"components.schemas.PublicVerification.properties.prescription.properties",
		signing.PublicPrescription{})
}

// fullSignature is a [signing.Signature] with a value in every field, including the optional ones.
func fullSignature() signing.Signature {
	return signing.Signature{
		PrescriptionID:   uuid.New(),
		FacilityID:       uuid.New(),
		CanonicalVersion: signing.CanonicalVersion,
		CanonicalSHA256:  "9f2c1f0e",
		Algorithm:        signing.Algorithm,
		SignerKind:       signing.SignerLocal,
		KeyID:            "local-dev-1",
		PublicKey:        "00",
		Value:            "01",
		SignedAt:         time.Date(2026, 9, 14, 9, 45, 0, 0, time.UTC),
		SignedBy:         uuid.New(),
		SignedByCode:     "E001",
		SignedByNameEN:   "Dr K M Nahid Ul Haque",
		SignedByNameBN:   "ডা. কে এম নাহিদ উল হক",
		DeviceAssurance:  signing.AssuranceNamed,
		QAReviewID:       uuid.New(),
		QAClearedAt:      time.Date(2026, 9, 14, 9, 35, 0, 0, time.UTC),
		NonExportableKey: false,
	}
}

// compareKeys asserts that the contract's property names for a schema are exactly the JSON keys
// the type serialises to.
//
// Both directions, and the two failures are reported separately because they mean opposite
// things: a key the contract does not carry is an undocumented field, and a property the type
// does not produce is a documented field that does not exist.
func compareKeys(t *testing.T, doc apispec.Document, path string, value any) {
	t.Helper()

	documented := doc[path]
	if len(documented) == 0 {
		t.Fatalf("%s has no properties in the contract — either the schema is missing or the "+
			"scanner no longer understands the document, and both must fail rather than pass", path)
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshalling for %s: %v", path, err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decoding for %s: %v", path, err)
	}

	inContract := map[string]bool{}
	for _, key := range documented {
		inContract[key] = true
	}
	served := map[string]bool{}
	for key := range decoded {
		served[key] = true
	}

	for _, key := range sortedKeys(served) {
		if !inContract[key] {
			t.Errorf("%s: the server sends %q and the contract does not describe it. A generated "+
				"client cannot see this field.", path, key)
		}
	}
	for _, key := range documented {
		if !served[key] {
			t.Errorf("%s: the contract describes %q and the server never sends it. A client "+
				"written against the contract would read undefined.", path, key)
		}
	}
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
