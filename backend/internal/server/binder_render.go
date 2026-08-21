package server

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

func jsonCompact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// returnLine is one row of the return, as it goes into both the CSV and
// the document. Section groups them the way the form does.
type returnLine struct {
	Section string
	Label   string
	Value   string
	Unit    string
	// Note carries what the figure means where the number alone is
	// misleading — a total that is the sum of two differently-charged
	// bands, an opening balance that was reverse-walked rather than
	// counted.
	Note string
}

func laa(v float64) string  { return fmt.Sprintf("%.4f", v) }
func cash(v float64) string { return fmt.Sprintf("%.2f", v) }

// returnLines flattens a report into the lines a person reads. Written out
// rather than reflected over the proto so that the order is the form's
// order and each line can carry its own note.
func returnLines(r *stillhousev1.B266Report) []returnLine {
	if r == nil {
		return nil
	}
	opt := func(v float64, sec, label string) []returnLine {
		// Lines the form has and this distillery never uses are omitted
		// rather than printed as zeroes. Eleven zeroes between two real
		// figures is how a reader stops reading.
		if v == 0 {
			return nil
		}
		return []returnLine{{Section: sec, Label: label, Value: laa(v), Unit: "LAA"}}
	}

	const bulk = "Bulk spirits"
	lines := []returnLine{
		{Section: bulk, Label: "Opening inventory", Value: laa(r.GetBulkOpeningLaa()), Unit: "LAA",
			Note: "Reverse-walked from the closing balance: closing − receipts + withdrawals."},
		{Section: bulk, Label: "Production", Value: laa(r.GetBulkProductionLaa()), Unit: "LAA"},
	}
	lines = append(lines, opt(r.GetBulkOpeningInventoryAdoptedLaa(), bulk, "  of which adopted into Stillhouse")...)
	lines = append(lines, returnLine{Section: bulk, Label: "Received in bond", Value: laa(r.GetBulkReceivedInBondLaa()), Unit: "LAA"})
	lines = append(lines, opt(r.GetBulkImportedLaa(), bulk, "Imported")...)
	lines = append(lines, opt(r.GetBulkReceivedFromLicenseeLaa(), bulk, "Received from a spirits licensee")...)
	lines = append(lines, opt(r.GetBulkReceivedFromLicensedUserLaa(), bulk, "Received from a licensed user")...)
	lines = append(lines, opt(r.GetBulkPackagedReturnedToBulkLaa(), bulk, "Packaged spirits returned to bulk")...)
	lines = append(lines,
		returnLine{Section: bulk, Label: "Transferred to packaging", Value: laa(r.GetBulkTransferredToPackagingLaa()), Unit: "LAA"},
		returnLine{Section: bulk, Label: "Transferred out in bond", Value: laa(r.GetBulkTransferredOutInBondLaa()), Unit: "LAA"})
	lines = append(lines, opt(r.GetBulkDeliveredToLicenseeLaa(), bulk, "Delivered to a spirits licensee")...)
	lines = append(lines, opt(r.GetBulkDeliveredToLicensedUserLaa(), bulk, "Delivered to a licensed user")...)
	lines = append(lines, opt(r.GetBulkExportedLaa(), bulk, "Exported")...)
	lines = append(lines, opt(r.GetBulkDenaturedDaLaa(), bulk, "Denatured to DA")...)
	lines = append(lines, opt(r.GetBulkDenaturedSdaLaa(), bulk, "Denatured to SDA")...)
	lines = append(lines, opt(r.GetBulkReturnedToProductionLaa(), bulk, "Returned to production")...)

	lines = append(lines, returnLine{Section: bulk, Label: "Losses", Value: laa(r.GetBulkLossesLaa()), Unit: "LAA",
		Note: "Evaporation and unaccounted, split below by duty treatment (EDM3-4-1)."})
	lines = append(lines, opt(r.GetBulkLossesRelievedLaa(), bulk, "  relieved")...)
	lines = append(lines, opt(r.GetBulkLossesDutiableLaa(), bulk, "  duty payable")...)
	if r.GetBulkLossesUnclassifiedLaa() > 0 {
		lines = append(lines, returnLine{Section: bulk, Label: "  NOT YET CLASSIFIED",
			Value: laa(r.GetBulkLossesUnclassifiedLaa()), Unit: "LAA",
			Note: "Nobody had ruled on the duty treatment of these when this return was filed."})
	}
	lines = append(lines, returnLine{Section: bulk, Label: "Destroyed", Value: laa(r.GetBulkDestroyedLaa()), Unit: "LAA"})
	if r.GetBulkAdjustmentsCount() > 0 {
		lines = append(lines,
			returnLine{Section: bulk, Label: fmt.Sprintf("Adjustments (%d)", r.GetBulkAdjustmentsCount()),
				Value: laa(r.GetBulkAdjustmentsLaa()), Unit: "LAA",
				Note: "Line D. Reason-coded reconciliations of book stock to physical; see schedule 07."},
			returnLine{Section: bulk, Label: "  increases", Value: laa(r.GetBulkAdjustmentsIncreaseLaa()), Unit: "LAA"},
			returnLine{Section: bulk, Label: "  decreases", Value: laa(r.GetBulkAdjustmentsDecreaseLaa()), Unit: "LAA"})
	}
	lines = append(lines, returnLine{Section: bulk, Label: "Closing inventory", Value: laa(r.GetBulkClosingLaa()), Unit: "LAA",
		Note: "As at the end of the period, not as at the date this was generated."})

	const pkg = "Packaged spirits"
	lines = append(lines,
		returnLine{Section: pkg, Label: "Opening inventory", Value: laa(r.GetPackagedOpeningLaa()), Unit: "LAA"},
		returnLine{Section: pkg, Label: "Packaged", Value: laa(r.GetPackagedPackagedLaa()), Unit: "LAA",
			Note: fmt.Sprintf("%d bottles. What became sealed bottles, not what was drawn from the tank.", r.GetPackagedPackagedBottles())},
		returnLine{Section: pkg, Label: "Packaging loss", Value: laa(r.GetPackagedPackagingLossLaa()), Unit: "LAA",
			Note: "Drawn from bulk but never sealed into a bottle. Reconciles the two sections."})
	lines = append(lines, opt(r.GetPackagedDutyPaidLaa(), pkg, "  of which duty-paid")...)
	lines = append(lines, opt(r.GetPackagedNonDutyPaidLaa(), pkg, "  of which non-duty-paid")...)
	lines = append(lines,
		returnLine{Section: pkg, Label: "Removed", Value: laa(r.GetPackagedRemovedDutyPaidLaa()), Unit: "LAA",
			Note: fmt.Sprintf("%d bottles.", r.GetPackagedRemovedDutyPaidBottles())},
		returnLine{Section: pkg, Label: "Closing inventory", Value: laa(r.GetPackagedClosingLaa()), Unit: "LAA",
			Note: fmt.Sprintf("%d bottles, as at the end of the period.", r.GetPackagedClosingBottles())})

	const duty = "Duty"
	lines = append(lines, returnLine{Section: duty, Label: "Duty point", Value: dutyPointLabel(r.GetDutyPoint()),
		Note: "Derived from whether an excise warehouse licence is held (EDM3-1-1 ¶18, ¶29). Governs from " + r.GetDutyPointEffectiveFrom() + "."})
	lines = append(lines, opt(r.GetPackagedDutiedOver7Laa(), duty, "Packaged >7% ABV")...)
	if r.GetPackagedDutiedOver7DutyCad() > 0 {
		lines = append(lines, returnLine{Section: duty, Label: "  duty at packaging",
			Value: cash(r.GetPackagedDutiedOver7DutyCad()), Unit: "CAD"})
	}
	lines = append(lines, opt(r.GetPackagedRemovedOver7Laa(), duty, "Removed >7% ABV")...)
	if r.GetPackagedRemovedOver7DutyCad() > 0 {
		lines = append(lines, returnLine{Section: duty, Label: "  duty at removal",
			Value: cash(r.GetPackagedRemovedOver7DutyCad()), Unit: "CAD"})
	}
	if r.GetPackagedRemovedUnder7Litres() > 0 {
		lines = append(lines,
			returnLine{Section: duty, Label: "Removed ≤7% ABV", Value: laa(r.GetPackagedRemovedUnder7Litres()), Unit: "litres of product",
				Note: "Charged per litre of product, not per litre of absolute alcohol."},
			returnLine{Section: duty, Label: "  duty at removal", Value: cash(r.GetPackagedRemovedUnder7DutyCad()), Unit: "CAD"})
	}
	if r.GetDutyOnLossesCad() > 0 {
		lines = append(lines, returnLine{Section: duty, Label: "Duty on losses",
			Value: cash(r.GetDutyOnLossesCad()), Unit: "CAD",
			Note: "Losses ruled duty-payable under EDM3-4-1; see schedule 08."})
	}
	lines = append(lines,
		returnLine{Section: duty, Label: "Rate, >7% ABV", Value: fmt.Sprintf("%.3f", r.GetDutyRatePerLaa()), Unit: "CAD/LAA"},
		returnLine{Section: duty, Label: "Rate, ≤7% ABV", Value: fmt.Sprintf("%.3f", r.GetDutyRatePerLitreUnder7()), Unit: "CAD/litre"},
		returnLine{Section: duty, Label: "TOTAL DUTY PAYABLE", Value: cash(r.GetDutyPayableCad()), Unit: "CAD",
			Note: "The sum of duty crystallised at packaging, at removal, and on losses."})
	return lines
}

func dutyPointLabel(p stillhousev1.DutyPoint) string {
	switch p {
	case stillhousev1.DutyPoint_DUTY_POINT_AT_PACKAGING:
		return "At packaging (no excise warehouse licence)"
	case stillhousev1.DutyPoint_DUTY_POINT_AT_REMOVAL:
		return "At removal (excise warehouse licence held)"
	}
	return "not recorded"
}

func returnCSV(r *stillhousev1.B266Report) []byte {
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	_ = cw.Write([]string{"section", "line", "value", "unit", "note"})
	for _, l := range returnLines(r) {
		_ = cw.Write([]string{l.Section, strings.TrimSpace(l.Label), l.Value, l.Unit, l.Note})
	}
	cw.Flush()
	return buf.Bytes()
}

type binderView struct {
	Tenant       sqlcgen.Tenant
	Period       sqlcgen.B266Period
	Report       *stillhousev1.B266Report
	FromSnapshot bool
	Counts       map[string]int
	GeneratedAt  time.Time
	GeneratedBy  sqlcgen.User
}

func (v binderView) Sections() []struct {
	Name  string
	Lines []returnLine
} {
	var out []struct {
		Name  string
		Lines []returnLine
	}
	for _, l := range returnLines(v.Report) {
		if n := len(out); n > 0 && out[n-1].Name == l.Section {
			out[n-1].Lines = append(out[n-1].Lines, l)
			continue
		}
		out = append(out, struct {
			Name  string
			Lines []returnLine
		}{Name: l.Section, Lines: []returnLine{l}})
	}
	return out
}

func (v binderView) Schedules() []struct {
	File, Title, Why string
	Rows             int
} {
	var out []struct {
		File, Title, Why string
		Rows             int
	}
	for _, t := range binderTables {
		out = append(out, struct {
			File, Title, Why string
			Rows             int
		}{t.file, t.title, t.why, v.Counts[t.file]})
	}
	return out
}

func (v binderView) Dates() (string, string) {
	return v.Period.PeriodStart.Time.Format("2006-01-02"), v.Period.PeriodEnd.Time.Format("2006-01-02")
}

func renderBinderHTML(v binderView) ([]byte, error) {
	var buf bytes.Buffer
	if err := binderTemplate.Execute(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func binderReadme(p sqlcgen.B266Period, fromSnapshot bool, at time.Time, by sqlcgen.User, counts map[string]int) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "STILLHOUSE AUDIT BINDER\n")
	fmt.Fprintf(&b, "Reporting period %s to %s\n\n",
		p.PeriodStart.Time.Format("2006-01-02"), p.PeriodEnd.Time.Format("2006-01-02"))

	b.WriteString("WHAT THIS IS\n")
	b.WriteString(strings.Repeat("-", 60) + "\n")
	b.WriteString(wrap(
		"One bundle for one reporting period: the figures on the return, the "+
			"movements behind each line, the determinations and the approved "+
			"instruments behind each movement, and the record of who did what. "+
			"Open binder.html for the document; the CSVs are the same evidence in "+
			"a form a spreadsheet can work with.") + "\n\n")

	b.WriteString("WHERE THE FIGURES CAME FROM\n")
	b.WriteString(strings.Repeat("-", 60) + "\n")
	if fromSnapshot {
		b.WriteString(wrap(
			"The return in 01-return.csv is the FROZEN SNAPSHOT taken when this "+
				"period was marked submitted. It is what was filed. It has not been "+
				"recomputed for this binder, and it will not change if the underlying "+
				"records are later corrected — which is the point of it.") + "\n\n")
	} else {
		b.WriteString(wrap(
			"WARNING: this period has NOT been marked submitted, so there is no "+
				"frozen snapshot and no return is included. The supporting schedules "+
				"below are the live records as at the moment this binder was "+
				"generated, and they may change. Generate the binder again after "+
				"submitting the period if you need the figures as filed.") + "\n\n")
	}

	b.WriteString("WHAT IS IN IT\n")
	b.WriteString(strings.Repeat("-", 60) + "\n")
	fmt.Fprintf(&b, "%-28s %s\n", "README.txt", "this file")
	fmt.Fprintf(&b, "%-28s %s\n", "binder.html", "the document — open in a browser, print to PDF")
	fmt.Fprintf(&b, "%-28s %s\n", "01-return.csv", "the return, line by line")
	for _, t := range binderTables {
		fmt.Fprintf(&b, "%-28s %s (%d rows)\n", t.file, t.title, counts[t.file])
		b.WriteString(indent(wrapAt(t.why, 48), strings.Repeat(" ", 29)) + "\n")
	}
	fmt.Fprintf(&b, "%-28s %s\n\n", "manifest.txt", "SHA-256 of every file above")

	b.WriteString("WHAT THIS DOES NOT SAY\n")
	b.WriteString(strings.Repeat("-", 60) + "\n")
	b.WriteString(wrap(
		"This binder is a faithful assembly of the records Stillhouse holds. It "+
			"is not an assurance that those records are complete or correct — that "+
			"is the licensee's, and only the licensee's. Stillhouse never filed "+
			"anything with CRA; every return it appears in was filed by a person, "+
			"by hand.") + "\n\n")
	b.WriteString(wrap(
		"Where a schedule shows a determination made with no instrument named, or "+
			"a loss with no duty treatment, that is what the record says. Nothing "+
			"has been filled in to make the binder look tidier.") + "\n\n")

	fmt.Fprintf(&b, "Generated %s by %s <%s>\n",
		at.Format(time.RFC3339), by.DisplayName, by.Email)
	return []byte(b.String())
}

func wrap(s string) string { return wrapAt(s, 76) }

func wrapAt(s string, width int) string {
	var out strings.Builder
	line := 0
	for i, w := range strings.Fields(s) {
		if line > 0 && line+1+len(w) > width {
			out.WriteString("\n")
			line = 0
		} else if i > 0 && line > 0 {
			out.WriteString(" ")
			line++
		}
		out.WriteString(w)
		line += len(w)
	}
	return out.String()
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

var binderTemplate = template.Must(template.New("binder").Funcs(template.FuncMap{
	"iso": func(t time.Time) string { return t.Format("2006-01-02") },
	"ts":  func(t time.Time) string { return t.Format("2006-01-02 15:04 MST") },
}).Parse(binderHTML))
