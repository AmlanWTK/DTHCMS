package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/synthesis"
)

// The review page (CP71 criterion 5).
//
// One long document rather than an application: it is read once, by one person, probably on paper
// or on a tablet with no network. Every summary carries the assembled context beside it, because
// the question being asked is not only "is this narrative good" but "was the right thing put in
// front of the model" — and the second question is the one whose answer this checkpoint can act on.

type pageData struct {
	GeneratedAt string
	Tier        string
	Summaries   []summary
}

type summary struct {
	Index      int
	Name       string
	ClinicalID string
	VisitType  string
	ClinicDay  string
	State      string
	Degraded   bool
	Message    string

	Narrative  string
	KeyPoints  []string
	Diagnoses  []suggestedDiagnosis
	Missing    []missingItem
	RedFlags   []redFlag
	Confidence string
	Citations  []string

	Prompt    string
	Model     string
	Elapsed   string
	MetSLA    string
	FactCount int

	// Ungrounded is every citation in the answer that does not resolve to a fact in the context.
	//
	// CP72's job, done here at its crudest, because the page is where a reviewer would otherwise
	// have to do it by eye across two columns. It should always be empty; a non-empty list on this
	// page is the single most important thing on it.
	Ungrounded []string

	Complaint   string
	Stations    []stationRow
	Measured    []measuredRow
	Trends      []trendRow
	Growth      []growthRow
	Allergy     string
	History     []string
	Alerts      []string
	Priors      []string
	Gaps        []gapRow
	ContextJSON string
}

type stationRow struct{ Code, Status string }
type measuredRow struct{ Ref, Label, Value, Flag, On string }
type trendRow struct{ Label, Series, Change string }
type growthRow struct{ Label, Value, Centile, Z, Velocity string }
type gapRow struct{ Severity, Detail string }

func summaryOf(c candidate, view synthesis.View, elapsed time.Duration) summary {
	s := summary{
		Name: c.nameEN, ClinicalID: c.clinicalRef,
		VisitType: strings.ReplaceAll(c.visitType, "_", " "),
		ClinicDay: c.clinicDay.Format("2 January 2006"),
		State:     string(view.State), Degraded: view.Degraded, Message: view.MessageEN,
		Elapsed: fmt.Sprintf("%.1fs", elapsed.Seconds()),
	}
	if view.Run == nil {
		return s
	}
	run := view.Run
	s.Prompt, s.Model = run.PromptVersion, run.ModelVersion
	s.Complaint = run.Context.Visit.Complaint
	s.FactCount = len(run.Context.Facts)
	switch {
	case run.MetSLA == nil:
		s.MetSLA = "—"
	case *run.MetSLA:
		s.MetSLA = "met"
	default:
		s.MetSLA = "MISSED"
	}

	for _, station := range run.Context.Visit.Stations {
		s.Stations = append(s.Stations, stationRow{
			Code: readableStation(station.Code), Status: strings.ReplaceAll(station.Status, "_", " "),
		})
	}
	for _, m := range run.Context.Current {
		s.Measured = append(s.Measured, measuredRow{
			Ref: m.Ref, Label: m.Label, Value: strings.TrimSpace(m.Value + " " + m.Unit),
			Flag: strings.ReplaceAll(m.Flag, "_", " "), On: m.On,
		})
	}
	for _, trend := range run.Context.Trends {
		var points []string
		for _, p := range trend.Points {
			points = append(points, p.On+": "+p.Value)
		}
		row := trendRow{Label: trend.Label, Series: strings.Join(points, "  →  ")}
		if trend.Change != nil {
			row.Change = fmt.Sprintf("%s over %d days", trend.Change.Delta, trend.Change.OverDays)
		}
		s.Trends = append(s.Trends, row)
	}
	if g := run.Context.Growth; g != nil {
		for _, indicator := range g.Indicators {
			s.Growth = append(s.Growth, growthRow{
				Label: indicator.Label, Value: strings.TrimSpace(indicator.Value + " " + indicator.Unit),
				Centile: indicator.Percentile, Z: indicator.Z, Velocity: indicator.Velocity,
			})
		}
		if g.ObesityFlag != "" {
			s.Growth = append(s.Growth, growthRow{Label: "Flag", Value: strings.ReplaceAll(g.ObesityFlag, "_", " ")})
		}
		if g.Note != "" {
			s.Growth = append(s.Growth, growthRow{Label: "Note", Value: strings.ReplaceAll(g.Note, "_", " ")})
		}
	}
	s.Allergy = run.Context.Allergies.Status
	for _, item := range run.Context.Allergies.Items {
		s.Allergy += " — " + item.Substance + " (" + item.Reaction + ")"
	}
	for _, item := range run.Context.History {
		line := item.Kind + ": " + item.Label
		if !item.Confirmed {
			line += " (not confirmed at this visit)"
		}
		s.History = append(s.History, line)
	}
	for _, alert := range run.Context.Alerts {
		s.Alerts = append(s.Alerts, fmt.Sprintf("%s %s %s — %s threshold %s (%s)",
			alert.Label, alert.Value, alert.Unit, alert.Breached, alert.Threshold, alert.Status))
	}
	for _, prior := range run.Context.PriorVisits {
		s.Priors = append(s.Priors, strings.TrimSpace(prior.On+" — "+prior.Diagnoses+" / "+prior.Plan))
	}
	for _, gap := range run.Context.Gaps {
		s.Gaps = append(s.Gaps, gapRow{Severity: gap.Severity, Detail: gap.Detail})
	}
	if encoded, err := json.MarshalIndent(run.Context, "", "  "); err == nil {
		s.ContextJSON = string(encoded)
	}

	if len(run.Output) > 0 {
		var got answer
		if err := json.Unmarshal(run.Output, &got); err == nil {
			s.Narrative = got.NarrativeEN
			s.KeyPoints = got.KeyPoints
			s.Diagnoses = got.SuggestedDiagnoses
			s.Missing = got.MissingInvestigations
			s.RedFlags = got.RedFlags
			s.Confidence = fmt.Sprintf("%.2f", got.Confidence)
			s.Citations = got.Citations

			// The grounding check, at its crudest and a checkpoint early. Every citation must
			// resolve to a fact the assembler put in; a citation that does not is either a
			// hallucinated reference or an assembler that dropped a fact between the payload and
			// the stored context, and both are things a reviewer must see rather than infer.
			facts := run.Context.FactRefs()
			for _, ref := range got.Citations {
				if _, known := facts[ref]; !known {
					s.Ungrounded = append(s.Ungrounded, ref)
				}
			}
		}
	}
	return s
}

func render(page pageData) ([]byte, error) {
	tmpl, err := template.New("page").Parse(pageTemplate)
	if err != nil {
		return nil, err
	}
	for i := range page.Summaries {
		page.Summaries[i].Index = i + 1
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, page); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const pageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>DTHCMS · CP71 · Twenty pre-consultation summaries for review</title>
<style>
  :root { --ink:#1b1d21; --muted:#5c6470; --line:#dfe3e8; --paper:#ffffff;
          --wash:#f6f7f9; --flag:#b3261e; --warn:#8a5a00; --ok:#1e6b3a; }
  * { box-sizing:border-box; }
  body { margin:0; background:var(--wash); color:var(--ink);
         font:15px/1.6 "Iowan Old Style", "Palatino Linotype", Georgia, serif; }
  .wrap { max-width:1180px; margin:0 auto; padding:32px 20px 80px; }
  h1 { font-size:30px; line-height:1.2; margin:0 0 6px; letter-spacing:-0.01em; }
  h2 { font-size:20px; margin:0; }
  .sub { color:var(--muted); margin:0 0 26px; }
  .banner { border:2px solid var(--flag); background:#fff5f4; border-radius:8px;
            padding:16px 18px; margin:0 0 26px; }
  .banner strong { color:var(--flag); }
  .banner p { margin:8px 0 0; }
  .note { background:var(--paper); border:1px solid var(--line); border-radius:8px;
          padding:16px 18px; margin:0 0 26px; }
  .card { background:var(--paper); border:1px solid var(--line); border-radius:10px;
          margin:0 0 26px; overflow:hidden; }
  .card > header { padding:14px 18px; border-bottom:1px solid var(--line);
                   display:flex; flex-wrap:wrap; gap:10px 18px; align-items:baseline; }
  .card > header .who { font-weight:700; }
  .card > header .meta { color:var(--muted); font-size:13px; }
  .cols { display:grid; grid-template-columns:1fr 1fr; }
  @media (max-width:900px) { .cols { grid-template-columns:1fr; } }
  .col { padding:16px 18px; }
  .col + .col { border-left:1px solid var(--line); }
  @media (max-width:900px) { .col + .col { border-left:0; border-top:1px solid var(--line); } }
  .colhead { font:600 12px/1.4 ui-sans-serif, system-ui, sans-serif; letter-spacing:.09em;
             text-transform:uppercase; color:var(--muted); margin:0 0 10px; }
  .narrative { margin:0 0 14px; }
  h3 { font:600 12px/1.4 ui-sans-serif, system-ui, sans-serif; letter-spacing:.07em;
       text-transform:uppercase; color:var(--muted); margin:16px 0 6px; }
  ul { margin:0 0 8px; padding-left:20px; }
  li { margin:0 0 4px; }
  table { border-collapse:collapse; width:100%; font-size:13.5px; }
  td, th { text-align:left; padding:3px 8px 3px 0; vertical-align:top; }
  th { color:var(--muted); font-weight:600; }
  code, .mono { font:12.5px/1.5 ui-monospace, "SF Mono", Menlo, Consolas, monospace; }
  .ref { color:var(--muted); }
  .flag { color:var(--flag); font-weight:700; }
  .warn { color:var(--warn); }
  .ok { color:var(--ok); }
  .pill { display:inline-block; font:600 11px/1 ui-sans-serif, system-ui, sans-serif;
          letter-spacing:.06em; text-transform:uppercase; padding:5px 8px; border-radius:999px;
          border:1px solid var(--line); background:var(--wash); color:var(--muted); }
  .pill.ai { border-color:#b58900; background:#fff8e1; color:#7a5a00; }
  .pill.bad { border-color:var(--flag); background:#fff5f4; color:var(--flag); }
  details { margin-top:12px; }
  summary { cursor:pointer; color:var(--muted); font-size:13px; }
  pre { background:var(--wash); border:1px solid var(--line); border-radius:6px;
        padding:12px; overflow:auto; max-height:460px; }
</style>
</head>
<body>
<div class="wrap">

<h1>Twenty pre-consultation summaries</h1>
<p class="sub">DTHCMS · CP71 · generated {{.GeneratedAt}} · AI tier <code>{{.Tier}}</code></p>

<div class="banner">
  <strong>READ THIS FIRST — no language model wrote any of the narratives below.</strong>
  <p>Every summary on this page was produced by the real pipeline — the real deterministic
  assembler, the real prompt, the real schema validator, the real storage — with a
  <em>deterministic composer</em> standing in for the model, so that the page is free, repeatable
  and needs no key. The composer writes from the assembled context by rule. It cannot infer,
  cannot weigh two findings against each other, and never notices anything the assembler did not
  name.</p>
  <p>So this page is evidence about <strong>what the model is given</strong> and about
  <strong>the shape a summary arrives in</strong>. It is <strong>not</strong> evidence about the
  prose a real model would write, which is what the checkpoint's fifth acceptance criterion is
  really asking about. Closing that criterion needs this command run again against a paid
  credential.</p>
</div>

<div class="note">
  <p style="margin:0 0 8px"><strong>What to look at, and in what order.</strong></p>
  <ol style="margin:0; padding-left:20px">
    <li><strong>The right-hand column first.</strong> That is the assembled context: everything the
    model was given, and nothing else reaches it. If something a consultant needs is missing there,
    no model will recover it — and that is the finding this page exists to produce.</li>
    <li><strong>Then the gaps.</strong> The clinic's own list of what it knows it does not know. A
    gap that should not be there, or one that is missing, is a rule to change.</li>
    <li><strong>Then the fact references</strong> in square brackets. Every clinical claim carries
    the reference it rests on, and every reference resolves to a row in the context. Any that do
    not are listed in red under the narrative — there should never be any.</li>
    <li><strong>Last, the narrative itself</strong>, remembering the banner above.</li>
  </ol>
  <p style="margin:8px 0 0"><strong>Drafted medications are empty on every summary, deliberately.</strong>
  There is no formulary in this system until CP75, and a rule-based writer that named a drug would
  be inventing one. The checkpoint's own test — "assert the synthesis contains no invented drug
  names, validated against the formulary" — cannot be written until that formulary exists, and this
  is what standing in for it looks like in the meantime.</p>
</div>

{{range .Summaries}}
<section class="card">
  <header>
    <span class="who">{{.Index}}. {{.Name}}</span>
    <span class="meta"><code>{{.ClinicalID}}</code> · {{.VisitType}} visit · {{.ClinicDay}}</span>
    <span style="margin-left:auto"></span>
    <span class="pill ai">AI draft · not physician-authored</span>
    <span class="pill{{if .Degraded}} bad{{end}}">{{.State}}</span>
    <span class="pill">SLA {{.MetSLA}} · {{.Elapsed}}</span>
  </header>
  <div class="cols">

    <div class="col">
      <p class="colhead">What the system produced</p>
      {{if .Narrative}}
        <p class="narrative">{{.Narrative}}</p>
        {{if .Ungrounded}}
          <p class="flag">Citations that do not resolve to a fact in the context:
            {{range .Ungrounded}}<code>{{.}}</code> {{end}}</p>
        {{end}}
        <h3>Key points</h3>
        <ul>{{range .KeyPoints}}<li>{{.}}</li>{{end}}</ul>
        {{if .RedFlags}}
          <h3>Red flags</h3>
          <ul>{{range .RedFlags}}<li class="flag">{{.Statement}}
            <span class="ref mono">{{range .Basis}}[{{.}}]{{end}}</span></li>{{end}}</ul>
        {{end}}
        {{if .Diagnoses}}
          <h3>Diagnoses</h3>
          <ul>{{range .Diagnoses}}<li>{{.Label}} <span class="ref">({{.Status}})</span>
            <span class="ref mono">{{range .Basis}}[{{.}}]{{end}}</span></li>{{end}}</ul>
        {{end}}
        {{if .Missing}}
          <h3>Missing investigations</h3>
          <ul>{{range .Missing}}<li><strong>{{.Investigation}}</strong> — {{.Why}}</li>{{end}}</ul>
        {{end}}
        <h3>Drafted medications</h3>
        <p class="ref">None. See the note at the top of this page: there is no formulary until CP75.</p>
        <h3>Provenance</h3>
        <p class="ref mono">prompt {{.Prompt}} · model {{.Model}} · confidence {{.Confidence}} ·
          {{len .Citations}} citations over {{.FactCount}} facts</p>
      {{else}}
        <p class="flag">{{.Message}}</p>
        <p class="ref">This is the degraded state D-15 requires: the structured record is beside
        this, and the physician can still work from it.</p>
      {{end}}
    </div>

    <div class="col">
      <p class="colhead">What the model was given</p>

      {{if .Complaint}}<h3>Chief complaint</h3><p>{{.Complaint}}</p>{{end}}

      <h3>Journey</h3>
      <table>{{range .Stations}}<tr><td>{{.Code}}</td>
        <td class="{{if eq .Status "done"}}ok{{else}}warn{{end}}">{{.Status}}</td></tr>{{end}}</table>

      {{if .Measured}}
        <h3>Current measurements</h3>
        <table><tr><th>Value</th><th>On</th><th>Flag</th><th>Reference</th></tr>
        {{range .Measured}}<tr>
          <td>{{.Label}} <strong>{{.Value}}</strong></td>
          <td>{{.On}}</td>
          <td class="{{if .Flag}}flag{{end}}">{{.Flag}}</td>
          <td class="ref mono">{{.Ref}}</td></tr>{{end}}</table>
      {{end}}

      {{if .Trends}}
        <h3>Trends</h3>
        <table>{{range .Trends}}<tr><td>{{.Label}}</td><td class="mono">{{.Series}}</td>
          <td><strong>{{.Change}}</strong></td></tr>{{end}}</table>
      {{end}}

      {{if .Growth}}
        <h3>Growth (paediatric)</h3>
        <table>{{range .Growth}}<tr><td>{{.Label}}</td><td>{{.Value}}</td>
          <td>{{if .Centile}}centile {{.Centile}}{{end}}</td>
          <td>{{if .Z}}z {{.Z}}{{end}}</td><td>{{.Velocity}}</td></tr>{{end}}</table>
      {{end}}

      <h3>Allergies</h3>
      <p>{{.Allergy}}</p>

      {{if .History}}
        <h3>History</h3>
        <ul>{{range .History}}<li>{{.}}</li>{{end}}</ul>
      {{else}}
        <h3>History</h3>
        <p class="warn">None recorded.</p>
      {{end}}

      {{if .Alerts}}
        <h3>Critical values</h3>
        <ul>{{range .Alerts}}<li class="flag">{{.}}</li>{{end}}</ul>
      {{end}}

      {{if .Priors}}
        <h3>Prior visits</h3>
        <ul>{{range .Priors}}<li>{{.}}</li>{{end}}</ul>
      {{end}}

      {{if .Gaps}}
        <h3>Gaps the clinic already knows about</h3>
        <ul>{{range .Gaps}}<li class="{{if eq .Severity "important"}}flag{{else}}warn{{end}}">{{.Detail}}</li>{{end}}</ul>
      {{end}}

      <details>
        <summary>The whole assembled context, as JSON ({{.FactCount}} facts)</summary>
        <pre class="mono">{{.ContextJSON}}</pre>
      </details>
    </div>

  </div>
</section>
{{end}}

<p class="sub">Every payload behind this page went through the AI gateway's PHI minimiser: no name,
no telephone number, no national identity number and no date of birth left the building to produce
any of it. The names in the headers above are read from the database by the page itself, after the
fact — which is the strip-and-restore boundary made visible.</p>

</div>
</body>
</html>
`
