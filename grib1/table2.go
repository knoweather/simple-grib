package grib1

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
)

// GRIB1 table 2 (the parameter tables) used to be registration-only: the table
// varies by centre and version, ecCodes' table files are multi-column lines whose
// name and unit fields contain spaces, and a hand-transcribed name is worse than
// no name.
//
// What changed: the tables are now vendored data. paramdata/grib1 holds ecCodes
// 2.50.0's `2.<centre>.<table2Version>.table` files verbatim, and
// grib1/table2_gen.go is generated from them by cmd/paramgen using exactly the
// rule ecCodes' grib_load_codetable() uses (the title runs up to the last '(' of
// the line, then right-trimmed). The transcription is mechanical and checkable,
// and it was checked: every row of every reachable table was compared against
// ecCodes 2.50's `parameterName` and agrees byte for byte.
//
// Which table a message selects follows ecCodes grib1/section.1.def lines
// 137-145:
//
//	if (table2Version >= 128) {
//	  if (centre != 98 && subCentre == 98) centreForTable2 = subCentre;
//	  else                                 centreForTable2 = centre;
//	} else {
//	  centreForTable2 = WMO;   // 0 — the international exchange table
//	}
//
// so `table2Version < 128` ignores the centre part of the file name entirely
// (paramdata carries 2.82.1.table and 2.233.1.table, but no message can select
// them), and `table2Version >= 128` may switch to subCentre 98 so that ECMWF data
// redistributed by another centre still reads the ECMWF table.
//
// When the selected table has no entry for the code, or the table file does not
// exist at all, ecCodes' `codetable_title(indicatorOfParameter)` degrades to the
// decimal code, and so does this package: Product.ParameterName is never empty for
// GRIB1 now. Use Table2Versions to see which tables a caller registered itself.
//
// RegisterTable2 still wins over the built-in tables and is keyed by the message's
// own centre (the documented octet 5 / octet 4 pair), so an explicit registration
// for (centre, version) fully replaces the built-in table for that pair — a code
// missing from it then falls back to the decimal code, not to the built-in table.
//
// Important: never apply the WMO standard table to centre-local table versions
// (table2Version >= 128). The same number means different things in different
// versions (e.g. 167 is 2 metre temperature in the ECMWF table, something else
// elsewhere) — mismatching them yields confident-looking, wrong names, exactly the
// silent error class this library is built to avoid.

// Table2 is one parameter table (WMO table 2).
type Table2 struct {
	Centre  int            // originating centre (common table C-1)
	Version int            // table version number (PDS octet 4)
	Names   map[int]string // parameter code → name (WMO/centre wording, untranslated)
}

var (
	table2Mu sync.RWMutex
	table2   = map[[2]int]*Table2{}
)

// RegisterTable2 registers (or replaces) one parameter table. centre and version
// must match PDS octet 5 / octet 4; names is copied, so later changes to the
// caller's map do not affect the registered table. A registered table replaces the
// built-in one for that (centre, version) pair — see the file header.
//
// Errors: nil/empty names, or parameter codes out of the byte range (GRIB1 codes
// are one byte).
func RegisterTable2(centre, version int, names map[int]string) error {
	if centre < 0 || centre > 0xFF {
		return fmt.Errorf("grib1: RegisterTable2: centre %d out of 0..255", centre)
	}
	if version < 0 || version > 0xFF {
		return fmt.Errorf("grib1: RegisterTable2: table version %d out of 0..255", version)
	}
	if len(names) == 0 {
		return fmt.Errorf("grib1: RegisterTable2: centre %d version %d: empty table", centre, version)
	}
	cp := make(map[int]string, len(names))
	for code, name := range names {
		if code < 0 || code > 0xFF {
			return fmt.Errorf("grib1: RegisterTable2: parameter code %d out of 0..255", code)
		}
		cp[code] = name
	}
	table2Mu.Lock()
	defer table2Mu.Unlock()
	table2[[2]int{centre, version}] = &Table2{Centre: centre, Version: version, Names: cp}
	return nil
}

// Table2Versions lists the caller-registered (centre, table version) pairs in
// ascending numeric order — handy for diagnosing which tables a process added on
// top of the built-in ones. Built-in tables are not listed: they live in
// table2_gen.go and follow ecCodes' selection rule (see the file header).
func Table2Versions() [][2]int {
	table2Mu.RLock()
	defer table2Mu.RUnlock()
	out := make([][2]int, 0, len(table2))
	for k := range table2 {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

// table2Centre resolves the centre part of the table 2 file name, i.e. ecCodes'
// centreForTable2 (grib1/section.1.def lines 137-145). subCentre is PDS octet 26.
func table2Centre(centre, subCentre, version int) int {
	if version < 128 {
		return 0 // WMO: the international exchange table 2.0.<version>.table
	}
	if centre != 98 && subCentre == 98 {
		return subCentre
	}
	return centre
}

// parameterName returns the title of code in the table-2 file the message selects —
// the same value ecCodes reports as `parameterName`
// (section.1.def:50 `meta parameterName codetable_title(indicatorOfParameter)`).
//
// Selection order: a table registered through RegisterTable2 for the message's own
// (centre, version) wins outright; otherwise the built-in table for the pair
// resolved by table2Centre; otherwise ecCodes' degradation to the decimal code,
// which is also what a code missing from a selected table degrades to.
func parameterName(centre, subCentre, version, code int) string {
	key := [2]int{centre, version}
	table2Mu.RLock()
	t, ok := table2[key]
	var registered string
	if ok {
		registered = t.Names[code]
	}
	table2Mu.RUnlock()
	if ok {
		if registered != "" {
			return registered
		}
		return strconv.Itoa(code)
	}
	if m, hit := genTable2[[2]uint8{uint8(table2Centre(centre, subCentre, version)), uint8(version)}]; hit {
		if name, hit := m[uint8(code)]; hit {
			return name
		}
	}
	return strconv.Itoa(code)
}
