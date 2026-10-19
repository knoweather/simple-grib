package grib1

import (
	"fmt"
	"sort"
	"sync"
)

// Why table 2 (parameter tables) is not built in:
//
// It varies by centre and table version (WMO specifies only the international
// exchange version; centres add hundreds of local codes), and ecCodes' table files
// are multi-column `code alias shortname name unit` lines whose name and unit
// fields contain spaces, so the column boundary itself is ambiguous — mechanical
// transcription would mangle the names, and a mangled name is worse than no name.
//
// So this package only provides a registration entry point and never guesses.
// Register a centre's table as soon as you have it; unregistered codes leave
// Product.ParameterName empty and the caller sees the raw numeric code, which is
// always correct. Same design stance as spec.RegisterLocalTable on the GRIB2 side.
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
// caller's map do not affect the registered table.
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

// Table2Versions lists the registered (centre, table version) pairs in ascending
// numeric order — handy for diagnosing "why is this file's parameter unnamed".
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

// parameterName looks up a parameter name by (centre, tableVersion). Missing table
// or missing code both return "". It never falls back to a different table — see
// the file header.
func parameterName(centre, version, code int) string {
	table2Mu.RLock()
	defer table2Mu.RUnlock()
	t, ok := table2[[2]int{centre, version}]
	if !ok {
		return ""
	}
	return t.Names[code]
}
