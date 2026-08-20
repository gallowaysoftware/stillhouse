package alcoholometry

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ErrNotLoaded is returned by every lookup before the tables are supplied.
//
// The tables are NOT shipped with Stillhouse. They are Crown material, and
// the Government of Canada's terms permit non-commercial reproduction but
// not commercial redistribution without written permission — which would
// otherwise make a paid hosted Stillhouse a licensing problem. So the
// operator downloads them once from CRA and points the app at the file.
// See the deploy guide.
var ErrNotLoaded = errors.New(
	"alcoholometry: the Canadian Alcoholometric Tables have not been loaded — " +
		"set STILLHOUSE_ALCOHOLOMETRIC_TABLES to the ZIP or ALC_TAB.TXT downloaded from " +
		"https://www.canada.ca/en/revenue-agency/services/tax/technical-information/" +
		"excise-duty/tables-alcoholometry/canadian-alcoholometric-tables-1980.html")

var (
	mu     sync.RWMutex
	loaded *table
)

// Load reads the Canadian Alcoholometric Tables from disk.
//
// Deliberately forgiving about what it's given, because the operator has
// just downloaded a file from a government website and should not have to
// think about which one:
//
//   - the ZIP exactly as downloaded from CRA
//   - the ALC_TAB.TXT inside it, extracted
//   - a directory containing either
//
// Safe to call again; the last successful load wins.
func Load(path string) error {
	raw, name, err := readSource(path)
	if err != nil {
		return err
	}
	t, err := parseALCTAB(raw)
	if err != nil {
		return fmt.Errorf("alcoholometry: %s: %w", name, err)
	}
	t.srcSHA = sha256.Sum256(raw)
	t.srcName = name

	mu.Lock()
	loaded = t
	mu.Unlock()
	return nil
}

// Loaded reports whether the tables are available. The server logs this at
// boot so a missing file is obvious immediately rather than at the first
// gauge.
func Loaded() bool {
	mu.RLock()
	defer mu.RUnlock()
	return loaded != nil
}

// RowCount is how many published rows are in memory, for a startup log
// line that proves the right file was read.
func RowCount() int {
	mu.RLock()
	defer mu.RUnlock()
	if loaded == nil {
		return 0
	}
	n := 0
	for _, c := range loaded.rowCount {
		n += c
	}
	return n
}

func get() (*table, error) {
	mu.RLock()
	defer mu.RUnlock()
	if loaded == nil {
		return nil, ErrNotLoaded
	}
	return loaded, nil
}

// readSource resolves whatever the operator pointed at to the bytes of
// ALC_TAB.TXT.
func readSource(path string) ([]byte, string, error) {
	if path == "" {
		return nil, "", ErrNotLoaded
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("alcoholometry: %w", err)
	}
	if info.IsDir() {
		found, err := findInDir(path)
		if err != nil {
			return nil, "", err
		}
		path = found
		info, err = os.Stat(path)
		if err != nil {
			return nil, "", fmt.Errorf("alcoholometry: %w", err)
		}
	}
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		return readFromZip(path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("alcoholometry: %w", err)
	}
	return b, filepath.Base(path), nil
}

func findInDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("alcoholometry: %w", err)
	}
	var zipPath string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.EqualFold(name, "ALC_TAB.TXT") {
			return filepath.Join(dir, name), nil
		}
		if zipPath == "" && strings.EqualFold(filepath.Ext(name), ".zip") {
			zipPath = filepath.Join(dir, name)
		}
	}
	if zipPath != "" {
		return zipPath, nil
	}
	return "", fmt.Errorf(
		"alcoholometry: no ALC_TAB.TXT or .zip in %s — put the file CRA gives you there", dir)
}

func readFromZip(path string) ([]byte, string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, "", fmt.Errorf("alcoholometry: open zip: %w", err)
	}
	defer func() { _ = zr.Close() }()

	for _, f := range zr.File {
		// Match on the base name so a zip with a directory inside still
		// works, and case-insensitively because the CRA archive is
		// uppercase and re-zips often aren't.
		if !strings.EqualFold(filepath.Base(f.Name), "ALC_TAB.TXT") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, "", fmt.Errorf("alcoholometry: read from zip: %w", err)
		}
		defer func() { _ = rc.Close() }()
		b, err := io.ReadAll(rc)
		if err != nil {
			return nil, "", fmt.Errorf("alcoholometry: read from zip: %w", err)
		}
		return b, filepath.Base(path) + "!" + f.Name, nil
	}
	return nil, "", fmt.Errorf(
		"alcoholometry: %s contains no ALC_TAB.TXT — is this the alcoholometric tables archive?",
		filepath.Base(path))
}

// parseALCTAB turns the published ASCII into the lookup grid.
//
// The file is CRLF-delimited and column-aligned: temperature, density,
// then A, B and C. Rows carrying only a temperature and a density are
// density steps outside the alcohol range at that temperature, and the
// table leaves them blank.
func parseALCTAB(raw []byte) (*table, error) {
	byTemp := map[int]map[int][3]float64{}
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		f := strings.Fields(strings.TrimRight(sc.Text(), "\r"))
		if len(f) != 5 {
			continue
		}
		v := make([]float64, 5)
		ok := true
		for i, s := range f {
			x, err := strconv.ParseFloat(s, 64)
			if err != nil {
				ok = false
				break
			}
			v[i] = x
		}
		if !ok {
			continue
		}
		ti := int(math.Round(v[0] * 10))
		di := int(math.Round(v[1] * 10))
		if byTemp[ti] == nil {
			byTemp[ti] = map[int][3]float64{}
		}
		byTemp[ti][di] = [3]float64{v[2], v[3], v[4]}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(byTemp) == 0 {
		return nil, errors.New("no data rows — is this really ALC_TAB.TXT?")
	}

	temps := make([]int, 0, len(byTemp))
	for t := range byTemp {
		temps = append(temps, t)
	}
	sort.Ints(temps)

	// The published table is a regular grid: 0.5 °C steps, 0.2 kg/m³
	// steps, contiguous within each temperature. Verify rather than
	// assume — a silent gap would shift every lookup after it.
	const tempStep, densStep = 5, 2
	for i, t := range temps {
		if i > 0 && t-temps[i-1] != tempStep {
			return nil, fmt.Errorf("temperature grid is not 0.5 °C regular: %.1f -> %.1f",
				float64(temps[i-1])/10, float64(t)/10)
		}
	}

	t := &table{
		tempMin:   temps[0],
		tempStep:  tempStep,
		densStep:  densStep,
		densStart: make([]int, len(temps)),
		rowCount:  make([]int, len(temps)),
		rowOffset: make([]int, len(temps)),
	}
	var rows []float64 // A, B, C per row, flattened
	for i, temp := range temps {
		ds := make([]int, 0, len(byTemp[temp]))
		for d := range byTemp[temp] {
			ds = append(ds, d)
		}
		sort.Ints(ds)
		for j, d := range ds {
			if j > 0 && d-ds[j-1] != densStep {
				return nil, fmt.Errorf("density grid gap at %.1f °C: %.1f -> %.1f",
					float64(temp)/10, float64(ds[j-1])/10, float64(d)/10)
			}
		}
		t.densStart[i] = ds[0]
		t.rowCount[i] = len(ds)
		t.rowOffset[i] = len(rows) / 3
		for _, d := range ds {
			abc := byTemp[temp][d]
			rows = append(rows, abc[0], abc[1], abc[2])
		}
	}
	t.rows = rows
	return t, nil
}
