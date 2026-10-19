package grib1

import "strings"

// This file is the access layer over tables.go (the generated raw code tables):
// "table number + code" lookups collected into the package's semantic helpers, plus
// a small hand-built table (level-type units) that cannot be derived mechanically.

// levelTypeName returns the name of a table 3 level type; reserved codes return
// "Reserved" and unallocated codes return "".
func levelTypeName(code int) string { return lookupCode(table3LevelTypes, code) }

// levelTypeUnit returns the unit of the level's numeric value. Table 3 states units
// in prose inside the name strings ("pressure in hectoPascals (hPa) (2 octets)"),
// so they cannot be extracted mechanically; units for the common codes are
// summarized by hand from WMO table 3. Unlisted codes return "" (treated as
// unitless, never guessed).
func levelTypeUnit(code int) string {
	switch code {
	case 20:
		return "1/100 K"
	case 100, 102:
		return "hPa"
	case 101, 104, 106, 108, 110, 112, 114, 116, 120, 128, 141:
		return "layer"
	case 103, 105, 160:
		return "m"
	case 107, 119:
		return "1/10000"
	case 109:
		return "level"
	case 111:
		return "cm"
	case 113:
		return "K"
	case 115:
		return "hPa"
	case 117:
		return "10-9 K m2 kg-1 s-1"
	case 121:
		return "hPa"
	case 125:
		return "cm"
	case 200, 201:
		return "layer"
	case 210:
		return "Pa"
	default:
		return ""
	}
}

// timeUnitName returns the name of a table 4 time unit.
func timeUnitName(code int) string { return lookupCode(table4TimeUnits, code) }

// timeRangeName returns the meaning of a table 5 time range indicator.
func timeRangeName(code int) string { return lookupCode(table5TimeRanges, code) }

// gridTypeName returns the name of a table 6 data representation type.
func gridTypeName(code int) string { return lookupCode(table6GridTypes, code) }

// centreName returns the name of an originating centre (common table C-1).
func centreName(code int) string { return lookupCode(tableC1Centres, code) }

// section1FlagName returns the meaning of PDS section 1 flag bit (WMO numbering,
// 1-based) taking value.
func section1FlagName(bit, value int) string {
	return lookupBitFlag(table1Section1Flags, bit, value)
}

// resolutionFlagName returns the meaning of a table 7 resolution/component flag bit
// taking value.
func resolutionFlagName(bit, value int) string {
	return lookupBitFlag(table7ResolutionFlags, bit, value)
}

// dataFlagName returns the meaning of a table 11 data-flag bit taking value.
func dataFlagName(bit, value int) string {
	return lookupBitFlag(table11DataFlags, bit, value)
}

// scanModeName renders the scan mode byte (table 8) as one readable description,
// e.g. "+i direction, -j direction, adjacent points in i direction are consecutive".
// Table 8 defines bits 1-3 only; bits 4-8 are undefined in GRIB1, so non-zero
// values there are called out instead of silently ignored.
func scanModeName(mode uint8) string {
	var parts []string
	for bit := 1; bit <= 3; bit++ {
		v := int(mode>>(8-bit)) & 1
		if s := lookupBitFlag(table8ScanModes, bit, v); s != "" {
			parts = append(parts, s)
		}
	}
	if mode&0x1F != 0 {
		parts = append(parts, "undefined bit(s) 4-8 set")
	}
	return strings.Join(parts, ", ")
}

// Exported wrappers so the CLI (cmd/gribdump) and the wasm front end share this one
// set of WMO tables instead of copying their own.

// GridTypeName returns the name of a table 6 data representation type.
func GridTypeName(code int) string { return gridTypeName(code) }

// LevelTypeName returns the name of a table 3 level type.
func LevelTypeName(code int) string { return levelTypeName(code) }

// TimeUnitName returns the name of a table 4 time unit.
func TimeUnitName(code int) string { return timeUnitName(code) }

// TimeRangeName returns the meaning of a table 5 time range indicator.
func TimeRangeName(code int) string { return timeRangeName(code) }

// CentreName returns the name of an originating centre (common table C-1).
func CentreName(code int) string { return centreName(code) }

// ScanModeName renders the scan mode byte (table 8) as one readable description.
func ScanModeName(mode uint8) string { return scanModeName(mode) }

// IsLayerRangeLevelType reports whether a table 3 level type names a layer pair,
// i.e. its PDS level is two 1-byte top/bottom values rather than one 2-byte value.
// Writers (gribw1) need this to pick the PDS layout; same data as
// levelTypesWithRange, exported for reuse.
func IsLayerRangeLevelType(code int) bool { return levelTypesWithRange[code] }
