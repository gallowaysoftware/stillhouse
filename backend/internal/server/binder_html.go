package server

// binderHTML is the document a person reads, and prints to PDF.
//
// Self-contained on purpose: no stylesheet, no font, no script fetched
// from anywhere. This file has to open in 2032, on a machine that has
// never heard of Stillhouse, with no network. The same reasoning as the
// tenant export — an artifact that needs the system that produced it is
// not evidence, it is a view.
//
// Print styling is real rather than decorative: an auditor is as likely to
// be handed this on paper as on a screen, and a table that splits a row
// across a page break is a table somebody misreads.
const binderHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Audit binder — {{.Tenant.Name}} — {{iso .Period.PeriodStart.Time}} to {{iso .Period.PeriodEnd.Time}}</title>
<style>
  :root { --ink:#141414; --muted:#555; --rule:#c8c8c8; --flag:#8a5300; }
  * { box-sizing: border-box; }
  body {
    font: 11pt/1.5 "Iowan Old Style", "Palatino Linotype", Palatino, Georgia, serif;
    color: var(--ink); background: #fff;
    max-width: 46em; margin: 3em auto; padding: 0 1.5em;
  }
  h1 { font-size: 1.5rem; margin: 0 0 .2em; }
  h2 { font-size: 1.05rem; margin: 2.2em 0 .6em; padding-bottom: .2em;
       border-bottom: 1px solid var(--rule); }
  h3 { font-size: .95rem; margin: 1.6em 0 .4em; }
  .sub { color: var(--muted); margin: 0 0 1.6em; }
  table { border-collapse: collapse; width: 100%; margin: .5em 0 1.2em; font-size: .92em; }
  th, td { text-align: left; padding: .35em .5em; vertical-align: top; }
  thead th { border-bottom: 1px solid var(--rule); font-weight: 600; }
  tbody tr + tr td { border-top: 1px solid #eee; }
  td.n, th.n { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  td.note { color: var(--muted); font-size: .85em; }
  tr.total td { font-weight: 700; border-top: 2px solid var(--ink); }
  .box { border: 1px solid var(--rule); padding: .9em 1.1em; margin: 1.2em 0; }
  .flag { border-color: var(--flag); }
  .flag h3 { color: var(--flag); margin-top: 0; }
  .quote { font-style: italic; margin: .4em 0 0; }
  dl.kv { display: grid; grid-template-columns: auto 1fr; gap: .25em 1.2em; margin: 0; }
  dl.kv dt { color: var(--muted); }
  dl.kv dd { margin: 0; }
  footer { margin-top: 3em; padding-top: .8em; border-top: 1px solid var(--rule);
           color: var(--muted); font-size: .85em; }
  code { font-family: ui-monospace, "SF Mono", Menlo, Consolas, monospace; font-size: .9em; }
  @media print {
    body { margin: 0; max-width: none; font-size: 10pt; }
    h2 { break-before: auto; }
    tr, .box { break-inside: avoid; }
    thead { display: table-header-group; }
  }
</style>
</head>
<body>

<h1>Audit binder</h1>
<p class="sub">
  {{.Tenant.Name}} · CRA spirits licence {{.Tenant.CraSpiritsLicenceNumber}}<br>
  Reporting period {{iso .Period.PeriodStart.Time}} to {{iso .Period.PeriodEnd.Time}}
</p>

<div class="box">
  <dl class="kv">
    <dt>Status</dt><dd>{{if eq (printf "%v" .Period.Status) "submitted"}}Submitted{{else}}Draft — not filed{{end}}</dd>
    {{if .Period.DueOn.Valid}}<dt>Return due</dt><dd>{{iso .Period.DueOn.Time}}</dd>{{end}}
    {{if .Period.SubmittedAt.Valid}}<dt>Marked submitted</dt><dd>{{ts .Period.SubmittedAt.Time}}</dd>{{end}}
    <dt>Binder generated</dt><dd>{{ts .GeneratedAt}} by {{.GeneratedBy.DisplayName}}</dd>
  </dl>
</div>

{{if not .FromSnapshot}}
<div class="box flag">
  <h3>This period has not been submitted</h3>
  <p>There is no frozen snapshot, so no return is reproduced here. The
  schedules listed below are the live records as at the moment this binder
  was generated, and they may change. Generate the binder again after
  marking the period submitted if you need the figures as filed.</p>
</div>
{{else}}

{{if .Period.FilingAcknowledgement}}
<h2>Confirmation</h2>
<p>Before this period was marked submitted, the following was confirmed
{{if .Period.FilingAcknowledgedAt.Valid}}on {{ts .Period.FilingAcknowledgedAt.Time}}{{end}}:</p>
<p class="quote">{{.Period.FilingAcknowledgement}}</p>
{{end}}

<h2>The return as filed</h2>
<p class="sub">These figures are the snapshot frozen when the period was
marked submitted. They have not been recomputed for this binder and will
not change if the underlying records are later corrected.</p>

{{range .Sections}}
<h3>{{.Name}}</h3>
<table>
  <thead><tr><th>Line</th><th class="n">Value</th><th>Unit</th><th>Note</th></tr></thead>
  <tbody>
  {{range .Lines}}
    <tr{{if eq .Label "TOTAL DUTY PAYABLE"}} class="total"{{end}}>
      <td>{{.Label}}</td>
      <td class="n">{{.Value}}</td>
      <td>{{.Unit}}</td>
      <td class="note">{{.Note}}</td>
    </tr>
  {{end}}
  </tbody>
</table>
{{end}}

{{with .Report}}{{if .FilingBlockers}}
<div class="box flag">
  <h3>Outstanding at the time this period was filed</h3>
  <p>Stillhouse recorded the following as unresolved. They are reproduced
  because a binder that hid them would be worth less than one that did
  not.</p>
  <ul>{{range .FilingBlockers}}<li>{{.}}</li>{{end}}</ul>
</div>
{{end}}{{end}}
{{end}}

<h2>Supporting schedules</h2>
<p class="sub">Each is a CSV in this bundle. Together they run from the
figure on the return to the person who determined it.</p>
<table>
  <thead><tr><th>File</th><th>Schedule</th><th class="n">Rows</th></tr></thead>
  <tbody>
  {{range .Schedules}}
    <tr>
      <td><code>{{.File}}</code></td>
      <td>{{.Title}}<br><span class="note">{{.Why}}</span></td>
      <td class="n">{{.Rows}}</td>
    </tr>
  {{end}}
  </tbody>
</table>

<h2>How to follow a figure back</h2>
<p>Take any quantity on the return and walk it down:</p>
<ol>
  <li><strong>The line</strong> — <code>01-return.csv</code>.</li>
  <li><strong>The movements behind it</strong> — <code>02-bulk-movements.csv</code>
      for anything in the bulk section, <code>05-bottling-runs.csv</code> and
      <code>06-removals.csv</code> for the packaged section. Each row's
      <code>reason</code> names the line it belongs to.</li>
  <li><strong>The determination behind the movement</strong> — the same row
      carries what was observed before correction to 20&nbsp;°C
      (<code>observed_volume_l</code>, <code>observed_density_kg_m3</code>,
      <code>temperature_c</code>), the factor applied
      (<code>volume_factor_c</code>), and which of the three paths it took
      (<code>strength_determined_by</code>).</li>
  <li><strong>The instrument that made it</strong> — the same row names it,
      and <code>09-instruments.csv</code> carries its CRA approval reference
      and last calibration. EDM1-1-5 requires each individual instrument to
      be approved, so a determination is only as good as its row here.</li>
  <li><strong>The person</strong> — <code>10-audit-trail.csv</code>.</li>
</ol>
<p>Where a row names no instrument, none was recorded. Where a loss has no
duty treatment, nobody had ruled on it. Nothing has been filled in to make
this bundle look more complete than the records are.</p>

<h2>What this binder does not say</h2>
<p>This is a faithful assembly of the records Stillhouse holds. It is not
an assurance that those records are complete or correct — that
responsibility is the licensee's, and is not shared by the software or by
anyone hosting it.</p>
<p><strong>Stillhouse has never filed anything with CRA.</strong> There is
no integration and no submission. Every return these figures appear on was
filed by a person, by hand.</p>

<footer>
  Stillhouse audit binder · generated {{ts .GeneratedAt}} ·
  {{.GeneratedBy.DisplayName}} &lt;{{.GeneratedBy.Email}}&gt;<br>
  Checksums for every file in this bundle are in <code>manifest.txt</code>.
</footer>

</body>
</html>
`
