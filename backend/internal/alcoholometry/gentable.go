//go:build ignore

// gentable converts the CRA "Canadian Alcoholometric Tables 1980" ASCII
// distribution (ALC_TAB.TXT) into the compact binary blob that this
// package embeds.
//
// Source:
//
//	https://www.canada.ca/en/revenue-agency/services/tax/technical-information/
//	  excise-duty/tables-alcoholometry/canadian-alcoholometric-tables-1980.html
//
// The download is a ZIP ("Canadian Alcoholometric Tables 1980 (921 KB/ASCII)")
// containing a single CRLF-delimited file, ALC_TAB.TXT.
//
// Usage:
//
//	go run gentable.go -src /path/to/ALC_TAB.TXT -out alctab.bin
//
// The source file is ~5.2 MB and is deliberately NOT committed; the
// generated blob is ~700 KB and is. The blob header carries the SHA-256
// of the exact source bytes it was built from, so provenance is
// checkable without keeping the original around.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

type row struct {
	a, b, c float64
}

func main() {
	src := flag.String("src", "", "path to ALC_TAB.TXT")
	out := flag.String("out", "alctab.bin", "output blob path")
	flag.Parse()
	if *src == "" {
		log.Fatal("-src is required")
	}

	raw, err := os.ReadFile(*src)
	if err != nil {
		log.Fatalf("read source: %v", err)
	}
	sum := sha256.Sum256(raw)

	// tenths of a degree -> tenths of kg/m3 -> row
	byTemp := map[int]map[int]row{}
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		f := strings.Fields(strings.TrimRight(sc.Text(), "\r"))
		// Data rows carry five columns: temp, density, A, B, C. Rows with
		// only temp+density are density steps that fall outside the
		// alcohol range at that temperature; the table leaves them blank.
		if len(f) != 5 {
			continue
		}
		t, err1 := strconv.ParseFloat(f[0], 64)
		d, err2 := strconv.ParseFloat(f[1], 64)
		a, err3 := strconv.ParseFloat(f[2], 64)
		b, err4 := strconv.ParseFloat(f[3], 64)
		c, err5 := strconv.ParseFloat(f[4], 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil {
			continue
		}
		ti := int(math.Round(t * 10))
		di := int(math.Round(d * 10))
		if byTemp[ti] == nil {
			byTemp[ti] = map[int]row{}
		}
		byTemp[ti][di] = row{a: a, b: b, c: c}
	}
	if err := sc.Err(); err != nil {
		log.Fatalf("scan: %v", err)
	}

	temps := make([]int, 0, len(byTemp))
	for t := range byTemp {
		temps = append(temps, t)
	}
	sort.Ints(temps)
	if len(temps) == 0 {
		log.Fatal("no data rows parsed — is this really ALC_TAB.TXT?")
	}

	// The published table is a regular grid: temperatures every 0.5 C,
	// densities every 0.2 kg/m3, contiguous within each temperature.
	// Verify rather than assume — a silent gap would shift every lookup
	// after it.
	const tempStep, densStep = 5, 2
	for i, t := range temps {
		if i > 0 && t-temps[i-1] != tempStep {
			log.Fatalf("temperature grid is not 0.5 C regular: %v -> %v", temps[i-1], t)
		}
	}

	type tempBlock struct {
		densStart int
		rows      []row
	}
	blocks := make([]tempBlock, 0, len(temps))
	total := 0
	for _, t := range temps {
		ds := make([]int, 0, len(byTemp[t]))
		for d := range byTemp[t] {
			ds = append(ds, d)
		}
		sort.Ints(ds)
		for i, d := range ds {
			if i > 0 && d-ds[i-1] != densStep {
				log.Fatalf("density grid gap at %.1f C: %.1f -> %.1f",
					float64(t)/10, float64(ds[i-1])/10, float64(d)/10)
			}
		}
		rs := make([]row, len(ds))
		for i, d := range ds {
			rs[i] = byTemp[t][d]
		}
		blocks = append(blocks, tempBlock{densStart: ds[0], rows: rs})
		total += len(rs)
	}

	var buf []byte
	ap16 := func(v int) {
		if v < 0 || v > math.MaxUint16 {
			log.Fatalf("value %d does not fit in uint16", v)
		}
		buf = binary.BigEndian.AppendUint16(buf, uint16(v))
	}

	buf = append(buf, []byte(magic)...)
	buf = append(buf, sum[:]...)
	ap16(len(blocks))
	buf = binary.BigEndian.AppendUint16(buf, uint16(int16(temps[0])))
	ap16(tempStep)
	ap16(densStep)
	for _, b := range blocks {
		ap16(b.densStart)
		ap16(len(b.rows))
	}
	for _, b := range blocks {
		for _, r := range b.rows {
			ap16(int(math.Round(r.a * 10000)))
			ap16(int(math.Round(r.b * 10)))
			ap16(int(math.Round(r.c * 10000)))
		}
	}

	if err := os.WriteFile(*out, buf, 0o644); err != nil {
		log.Fatalf("write blob: %v", err)
	}
	fmt.Printf("wrote %s: %d temperatures, %d rows, %d bytes\nsource sha256: %x\n",
		*out, len(blocks), total, len(buf), sum)
}

const magic = "SHALCTB1"
