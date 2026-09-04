package clinical_test

import (
	"net/http"
	"testing"
)

// The client's resolution and the server's cannot disagree (CP46).
//
// `GET /v1/observations/plausibility` returns the rules **already ordered most specific
// first**, and `@dthcms/clinical-calc` takes the first match. `core.plausibility_for` ranks
// them itself. This asserts the two agree for every code, every sex and a sweep of ages —
// which is what makes "the client reimplements nothing" true rather than hopeful.
//
// The failure it prevents is the nastiest kind: a screen that warns an operator about one
// band while the write is refused by another, so the value they were told was fine is the
// value the server rejects.
func TestTheListedOrderResolvesToTheSameRuleTheServerPicks(t *testing.T) {
	h := newAPI(t)

	resp, body := h.call(t, http.MethodGet, "/v1/observations/plausibility", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the rules: %d %v", resp.StatusCode, body)
	}
	listed, _ := body["rules"].([]any)
	if len(listed) == 0 {
		t.Fatal("no rules, which would make this test pass by doing nothing")
	}

	// The codes that have rules at all.
	codes := map[string]bool{}
	for _, item := range listed {
		rule, _ := item.(map[string]any)
		code, _ := rule["code"].(string)
		codes[code] = true
	}

	matches := func(rule map[string]any, sex string, age float64) bool {
		if got, ok := rule["sex"].(string); ok && got != "" && got != sex {
			return false
		}
		if got, ok := rule["min_age_years"].(float64); ok && age < got {
			return false
		}
		if got, ok := rule["max_age_years"].(float64); ok && age >= got {
			return false
		}
		return true
	}

	checked := 0
	for code := range codes {
		for _, sex := range []string{"male", "female"} {
			for _, age := range []float64{0, 0.5, 1.9, 2, 5, 11.9, 12, 17.9, 18, 45, 90} {
				// What the client would pick: the first listed rule that matches.
				var want map[string]any
				for _, item := range listed {
					rule, _ := item.(map[string]any)
					if rule["code"] == code && matches(rule, sex, age) {
						want = rule
						break
					}
				}

				// What the server picks.
				var absoluteMin, absoluteMax *float64
				var minAge, maxAge *float64
				var ruleSex *string
				err := h.SQL.QueryRow(
					`SELECT sex, min_age_years, max_age_years, absolute_min, absolute_max
					   FROM core.plausibility_for($1::text, $2::text, $3::numeric)`,
					code, sex, age).Scan(&ruleSex, &minAge, &maxAge, &absoluteMin, &absoluteMax)
				if err != nil {
					if want != nil {
						t.Errorf("%s/%s/%v: the client would use a rule and the server has none: %v",
							code, sex, age, err)
					}
					continue
				}
				if want == nil {
					t.Errorf("%s/%s/%v: the server has a rule and the client would use none", code, sex, age)
					continue
				}

				same := func(name string, listedValue any, serverValue *float64) {
					t.Helper()
					got, hasListed := listedValue.(float64)
					switch {
					case !hasListed && serverValue == nil:
					case hasListed && serverValue != nil && got == *serverValue:
					default:
						t.Errorf("%s/%s/%v: %s differs — client %v, server %v",
							code, sex, age, name, listedValue, serverValue)
					}
				}
				same("absolute_min", want["absolute_min"], absoluteMin)
				same("absolute_max", want["absolute_max"], absoluteMax)
				same("min_age_years", want["min_age_years"], minAge)
				same("max_age_years", want["max_age_years"], maxAge)
				checked++
			}
		}
	}
	if checked < 100 {
		t.Fatalf("only %d combinations were compared; the sweep is not doing its job", checked)
	}
}
