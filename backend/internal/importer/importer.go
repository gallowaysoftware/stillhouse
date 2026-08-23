// Package importer reads a CSV and turns it into rows a distillery can
// start from.
//
// Stage 124's adopt-existing-stock path is the careful half of getting a
// running distillery into Stillhouse: one cask at a time, from a scale
// reading and a hydrometer, with the determination trail intact. This is
// the boring half — the four hundred rows somebody already has in a
// spreadsheet — and PLAN is right that it is the difference between a
// distillery trying Stillhouse and finishing.
//
// Three properties matter more than the parsing:
//
//   - Dry run first, always available. An import that can only be found
//     out about by doing it is one nobody runs on real data.
//   - All or nothing. Every row lands in one transaction, so a failure
//     on row 380 leaves nothing behind — there is no half-imported state
//     to reason about and no "rollback" to perform.
//   - Every rejection names the row and says what is wrong with it in a
//     sentence. A count of failures is not a thing anybody can act on.
package importer

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Kind is what a file contains. One file, one kind: a spreadsheet that
// mixes them is a spreadsheet somebody will get wrong.
type Kind string

const (
	KindMaterials    Kind = "materials"
	KindMaterialLots Kind = "material_lots"
	KindProducts     Kind = "products"
	KindCustomers    Kind = "customers"
	KindBarrels      Kind = "barrels"
	KindPackaged     Kind = "packaged_inventory"
)

// Kinds is every importable kind, in the order a distillery would
// sensibly do them — later kinds reference earlier ones by name.
var Kinds = []Kind{
	KindMaterials, KindMaterialLots, KindProducts,
	KindCustomers, KindBarrels, KindPackaged,
}

// Column describes one field of a kind, for the template and for the
// error messages.
type Column struct {
	Name     string
	Required bool
	Help     string
}

// Problem is one thing wrong with one row.
type Problem struct {
	// Row is the line number in the file as a person sees it: 1 is the
	// header, so data starts at 2. Off-by-one here means somebody scrolls
	// to the wrong line, which is its own small betrayal.
	Row    int
	Column string
	Detail string
}

func (p Problem) String() string {
	if p.Column == "" {
		return fmt.Sprintf("row %d: %s", p.Row, p.Detail)
	}
	return fmt.Sprintf("row %d, %s: %s", p.Row, p.Column, p.Detail)
}

// Row is one parsed line, keyed by column name, with its file position
// kept so problems found later can still name it.
type Row struct {
	Line   int
	Values map[string]string
}

// Get returns a trimmed value.
func (r Row) Get(col string) string { return strings.TrimSpace(r.Values[col]) }

// Float parses a numeric cell, treating empty as absent.
func (r Row) Float(col string) (v float64, present bool, err error) {
	s := r.Get(col)
	if s == "" {
		return 0, false, nil
	}
	// Spreadsheets export thousands separators and currency symbols; a
	// number that a human clearly wrote as a number should not be
	// rejected over punctuation.
	s = strings.NewReplacer(",", "", "$", "", " ", "").Replace(s)
	v, err = strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%q is not a number", r.Get(col))
	}
	return v, true, nil
}

// Int parses an integer cell, treating empty as absent.
func (r Row) Int(col string) (v int64, present bool, err error) {
	f, present, err := r.Float(col)
	if err != nil || !present {
		return 0, present, err
	}
	if f != float64(int64(f)) {
		return 0, true, fmt.Errorf("%q must be a whole number", r.Get(col))
	}
	return int64(f), true, nil
}

// Parse reads a CSV into rows, checking the header against the columns
// the kind requires.
//
// Column order does not matter and unknown columns are reported rather
// than ignored: a column nobody reads is usually a column somebody
// misspelled, and silently dropping it means the data silently does not
// arrive.
func Parse(r io.Reader, columns []Column) ([]Row, []Problem) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // checked per row, so the message can be specific
	cr.TrimLeadingSpace = true

	records, err := cr.ReadAll()
	if err != nil {
		return nil, []Problem{{Row: 0, Detail: "could not read the file as CSV: " + err.Error()}}
	}
	if len(records) == 0 {
		return nil, []Problem{{Row: 0, Detail: "the file is empty"}}
	}

	known := map[string]Column{}
	for _, c := range columns {
		known[c.Name] = c
	}

	header := make([]string, len(records[0]))
	var problems []Problem
	seen := map[string]bool{}
	for i, h := range records[0] {
		// Strip a UTF-8 BOM, which Excel writes and which otherwise makes
		// the first column name never match anything.
		h = strings.TrimPrefix(h, "\ufeff")
		name := strings.ToLower(strings.TrimSpace(h))
		header[i] = name
		if name == "" {
			continue
		}
		if _, ok := known[name]; !ok {
			problems = append(problems, Problem{Row: 1, Column: name,
				Detail: "not a column this import understands — check the spelling against the template"})
			continue
		}
		if seen[name] {
			problems = append(problems, Problem{Row: 1, Column: name, Detail: "appears twice"})
		}
		seen[name] = true
	}
	for _, c := range columns {
		if c.Required && !seen[c.Name] {
			problems = append(problems, Problem{Row: 1, Column: c.Name, Detail: "required column is missing"})
		}
	}
	if len(problems) > 0 {
		// A bad header makes every row problem noise, so stop here.
		return nil, problems
	}

	var rows []Row
	for i, rec := range records[1:] {
		line := i + 2
		if isBlank(rec) {
			continue // trailing blank lines are what spreadsheets produce
		}
		if len(rec) > len(header) {
			problems = append(problems, Problem{Row: line, Detail: fmt.Sprintf(
				"has %d values but the header has %d columns", len(rec), len(header))})
			continue
		}
		values := make(map[string]string, len(header))
		for j, name := range header {
			if name == "" || j >= len(rec) {
				continue
			}
			values[name] = rec[j]
		}
		row := Row{Line: line, Values: values}
		for _, c := range columns {
			if c.Required && row.Get(c.Name) == "" {
				problems = append(problems, Problem{Row: line, Column: c.Name, Detail: "is required"})
			}
		}
		rows = append(rows, row)
	}
	return rows, problems
}

// Template renders the header line plus one commented example, which is
// what somebody actually needs in order to produce a file that imports.
func Template(columns []Column) string {
	names := make([]string, 0, len(columns))
	for _, c := range columns {
		names = append(names, c.Name)
	}
	var b strings.Builder
	b.WriteString(strings.Join(names, ",") + "\n")
	return b.String()
}

// Describe returns the column help, sorted required-first, for the UI.
func Describe(columns []Column) []Column {
	out := append([]Column(nil), columns...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Required != out[j].Required {
			return out[i].Required
		}
		return false
	})
	return out
}

func isBlank(rec []string) bool {
	for _, v := range rec {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}
