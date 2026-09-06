package counseling

import (
	"context"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Which checklist a patient in the room actually needs (CP56, §5.1's assignment rules).
//
// # Why this exists at all
//
// A counsellor's phone knows the patient in front of it and nothing else. Without this, the
// only way to start a session would be a list of every checklist in the clinic and a counsellor
// choosing one — which is a clinical assignment made by the person least placed to make it, and
// the reason §5.1 has assignment rules in the first place.
//
// # Why the conditions arrive through an interface
//
// A patient's coded conditions live in `history` (CP53), and `counseling` may not import it.
// That boundary is not bureaucracy here: it is what stops this package growing its own opinion
// about what a diagnosis is. So the composition root passes the codings in, and this file's job
// is only to say which checklists they call for and which of them are already open.
//
// # Why comorbidities and not "diagnoses"
//
// The physician's diagnosis is a later checkpoint. Today the coded conditions a patient carries
// are their history's ICD-10 items, recorded at station 4 — which is upstream of counselling in
// every visit type, so the information is there by the time it is needed. When diagnoses arrive,
// they become another source of codings passed to the same function, and nothing here changes.

// Coding is one coded condition. The three parts travel together, always (CP52).
type Coding struct {
	System  string
	Version string
	Code    string
}

// Conditions is how a patient's coded conditions reach this package.
//
// Implemented in `cmd/api` over the history store. Nil means no suggestions rather than an
// error: a deployment without it should show a counsellor an empty list and a sentence, not a
// 500 in the middle of a clinic.
type Conditions interface {
	CodedConditions(ctx context.Context, patient uuid.UUID) ([]Coding, error)
}

// Suggestion is one checklist this patient calls for, and whether it is already open.
type Suggestion struct {
	TemplateID   uuid.UUID `json:"template_id"`
	TemplateCode string    `json:"template_code"`
	TitleEN      string    `json:"title_en"`
	TitleBN      string    `json:"title_bn"`

	// Version is what a session started now would walk.
	Version int `json:"version"`

	// Priority is the assignment rule's. Higher wins, and it is reported rather than applied
	// silently: a counsellor looking at two checklists deserves to know which the clinic
	// considers the main one.
	Priority int `json:"priority"`

	// MatchedCode is the condition that called for it, so "why am I being asked to do this"
	// has an answer on the screen rather than in a policy document.
	MatchedSystem  string `json:"matched_system,omitempty"`
	MatchedVersion string `json:"matched_version,omitempty"`
	MatchedCode    string `json:"matched_code,omitempty"`

	// SessionID is the session already open for this visit and checklist, when there is one.
	// Present so the phone resumes rather than starting a second walk down the same list.
	SessionID string `json:"session_id,omitempty"`
	// Complete is always serialised. With `omitempty`, "this session is not finished" and "you
	// were not told" would be the same absence, and a phone would have to guess which.
	Complete bool `json:"complete"`
}

// Suggest is the checklists a visit calls for, best first.
//
// Sessions already open for the visit are included **even when no rule matches them any more**:
// a rule retired at lunchtime must not make a half-ticked session disappear from the phone of
// the counsellor walking it.
func (s *Store) Suggest(ctx context.Context, visit, patient uuid.UUID,
	conditions Conditions) ([]Suggestion, error) {

	byTemplate := map[uuid.UUID]Suggestion{}

	if conditions != nil {
		codings, err := conditions.CodedConditions(ctx, patient)
		if err != nil {
			return nil, err
		}
		for _, coding := range codings {
			if coding.System == "" || coding.Version == "" || coding.Code == "" {
				// A partial coding is not a coding (CP52). An item the catalogue had nothing
				// for is legitimate and matches no rule, which is the honest answer.
				continue
			}
			matches, err := s.For(ctx, strings.ToUpper(coding.System), coding.Version, coding.Code)
			if err != nil {
				return nil, err
			}
			for _, match := range matches {
				existing, seen := byTemplate[match.TemplateID]
				if seen && existing.Priority >= match.Priority {
					continue
				}
				byTemplate[match.TemplateID] = Suggestion{
					TemplateID: match.TemplateID, TemplateCode: match.Code,
					TitleEN: match.TitleEN, TitleBN: match.TitleBN,
					Version: match.Version, Priority: match.Priority,
					MatchedSystem: coding.System, MatchedVersion: coding.Version,
					MatchedCode: coding.Code,
				}
			}
		}
	}

	open, err := s.SessionsForVisit(ctx, visit)
	if err != nil {
		return nil, err
	}
	for _, session := range open {
		suggestion, seen := byTemplate[session.TemplateID]
		if !seen {
			suggestion = Suggestion{
				TemplateID: session.TemplateID, TemplateCode: session.TemplateCode,
				TitleEN: session.TitleEN, TitleBN: session.TitleBN,
				Version: session.TemplateVersion,
			}
		}
		suggestion.SessionID = session.ID.String()
		suggestion.Complete = session.Complete()
		byTemplate[session.TemplateID] = suggestion
	}

	out := make([]Suggestion, 0, len(byTemplate))
	for _, suggestion := range byTemplate {
		out = append(out, suggestion)
	}
	// Open sessions first — the counsellor is in the middle of one — then by the clinic's own
	// priority, then by code so the order is stable rather than whatever the map felt like.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.SessionID != "") != (b.SessionID != "") {
			return a.SessionID != ""
		}
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		return a.TemplateCode < b.TemplateCode
	})
	return out, nil
}
