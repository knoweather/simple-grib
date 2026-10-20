package grib1

import (
	"fmt"
	"time"
)

// Identification is the "who and when" part of section 1 (PDS).
//
// Note: GRIB1 encodes the reference time as century + year-of-century, not as a
// 4-digit year.
type Identification struct {
	Centre            int    // octet 5, common table C-1
	CentreName        string // name from C-1; empty when the code is not catalogued
	SubCentre         int    // octet 26 (centre-defined)
	GeneratingProcess int    // octet 6, generating process id (centre-defined)
	GridID            int    // octet 7, grid identification (255 = see section 2)
	TableVersion      int    // octet 4, version of table 2
	ReferenceTime     time.Time
	Century           int  // octet 25
	LocalDefinition   int  // octet 41 when a local definition exists; -1 = none
	Climatological    bool // octet 13 = 255: climatological, no real reference time

	// MARS 标签（可选）。只有 centre/subCentre 为 ECMWF 且本地定义声明了
	// grib1/mars_labeling.def 时才有值；其余情况 HasMars 为 false，下面四个
	// 字段保持零值。判定与布局见 mars.go。
	//
	// MARS labelling (optional). Populated only when the centre is ECMWF and the
	// local definition declares grib1/mars_labeling.def; see mars.go.
	HasMars                 bool
	MarsClass               int    // octet 42, mars/class.table
	MarsType                int    // octet 43, mars/type.table
	MarsStream              int    // octets 44-45, mars/stream.table
	ExperimentVersionNumber string // octets 46-49, 4 characters
}

// Product is the "what field, which level, which time range" part of section 1 (PDS).
type Product struct {
	Parameter          int    // octet 9, table 2 parameter indicator
	ParameterName      string // needs the (centre, TableVersion) table 2; empty when unregistered, see RegisterTable2
	LevelType          int    // octet 10, table 3
	LevelTypeName      string // table 3 name
	LevelTop           int    // level value; the upper bound for layer-pair types
	LevelBottom        int    // level value; the lower bound for layer-pair types, equal to LevelTop for single levels
	LayerRange         bool   // the level type names a range between two levels (two 1-byte values in octets 11/12)
	LevelMissing       bool   // level value is the missing sentinel (all ones)
	TimeRangeUnit      int    // octet 18, table 4
	TimeRangeIndicator int    // octet 21, table 5
	P1                 int    // octet 19
	P2                 int    // octet 20
	NumberInAverage    int    // octets 22-23
	NumberMissing      int    // octet 24
	DecimalScaleFactor int    // octets 27-28 (sign-magnitude)
}

// Table 3 level types that use two 1-byte level values instead of one 2-byte value
// (the explicit list in ecCodes section.1.def). These are layer pairs:
// octet 11 = top, octet 12 = bottom.
var levelTypesWithRange = map[int]bool{
	101: true, 104: true, 106: true, 108: true, 110: true, 112: true,
	114: true, 116: true, 120: true, 121: true, 128: true, 141: true,
}

// parsePDS parses section 1. Field offsets follow the WMO section 1 tables octet by octet.
func parsePDS(sec Section) (*Identification, *Product, error) {
	r := reader{sec.Data}
	if sec.Length < minPDSLen {
		return nil, nil, fmt.Errorf("grib1: section 1 length %d, want >= %d", sec.Length, minPDSLen)
	}

	tableVersion, err := r.u8(4)
	if err != nil {
		return nil, nil, err
	}
	centre, err := r.u8(5)
	if err != nil {
		return nil, nil, err
	}
	genProc, err := r.u8(6)
	if err != nil {
		return nil, nil, err
	}
	gridID, err := r.u8(7)
	if err != nil {
		return nil, nil, err
	}
	param, err := r.u8(9)
	if err != nil {
		return nil, nil, err
	}
	levelType, err := r.u8(10)
	if err != nil {
		return nil, nil, err
	}

	prod := &Product{
		Parameter:     int(param),
		LevelType:     int(levelType),
		LevelTypeName: levelTypeName(int(levelType)),
	}

	// Level values: layer-pair types use two 1-byte fields (top/bottom), everything
	// else one 2-byte field. All-ones marks a missing level.
	if levelTypesWithRange[int(levelType)] {
		top, err := r.u8(11)
		if err != nil {
			return nil, nil, err
		}
		bottom, err := r.u8(12)
		if err != nil {
			return nil, nil, err
		}
		prod.LayerRange = true
		prod.LevelTop, prod.LevelBottom = int(top), int(bottom)
		prod.LevelMissing = top == 0xFF && bottom == 0xFF
	} else {
		lv, err := r.u16(11)
		if err != nil {
			return nil, nil, err
		}
		prod.LevelTop, prod.LevelBottom = int(lv), int(lv)
		prod.LevelMissing = lv == 0xFFFF
	}

	id := &Identification{
		Centre:            int(centre),
		CentreName:        centreName(int(centre)),
		GeneratingProcess: int(genProc),
		GridID:            int(gridID),
		TableVersion:      int(tableVersion),
		LocalDefinition:   -1,
	}

	// Reference time: octets 13-17 = year-of-century, month, day, hour, minute
	// (seconds are always 0).
	yoc, err := r.u8(13)
	if err != nil {
		return nil, nil, err
	}
	month, err := r.u8(14)
	if err != nil {
		return nil, nil, err
	}
	day, err := r.u8(15)
	if err != nil {
		return nil, nil, err
	}
	hour, err := r.u8(16)
	if err != nil {
		return nil, nil, err
	}
	minute, err := r.u8(17)
	if err != nil {
		return nil, nil, err
	}
	century, err := r.u8(25)
	if err != nil {
		return nil, nil, err
	}
	id.Century = int(century)

	// Year conversion: year = (century-1)*100 + yearOfCentury (century 21 with
	// yoc 25 => 2025), the formula ecCodes G1Date::unpack_long implements.
	// yoc == 255 is the climatological sentinel: no real reference time, month/day
	// still name the climatological month and day.
	if yoc == 0xFF {
		id.Climatological = true
		if month < 1 || month > 12 {
			return nil, nil, fmt.Errorf("grib1: section 1: climatological reference month %d out of 1..12", month)
		}
	} else {
		year := (int(century)-1)*100 + int(yoc)
		if month < 1 || month > 12 {
			return nil, nil, fmt.Errorf("grib1: section 1: reference month %d out of 1..12", month)
		}
		if day < 1 || day > 31 {
			return nil, nil, fmt.Errorf("grib1: section 1: reference day %d out of 1..31", day)
		}
		if hour > 23 || minute > 59 {
			return nil, nil, fmt.Errorf("grib1: section 1: reference time %02d:%02d out of range", hour, minute)
		}
		t := time.Date(year, time.Month(month), int(day), int(hour), int(minute), 0, 0, time.UTC)
		// Normalization check: nonexistent dates like Feb 30 are rolled forward by
		// time.Date and must be rejected.
		if t.Month() != time.Month(month) || t.Day() != int(day) {
			return nil, nil, fmt.Errorf("grib1: section 1: reference date %04d-%02d-%02d does not exist", year, month, day)
		}
		id.ReferenceTime = t
	}

	subCentre, err := r.u8(26)
	if err != nil {
		return nil, nil, err
	}
	id.SubCentre = int(subCentre)

	// octets 27-28: decimal scale factor, sign-magnitude.
	dsf, err := r.smag16(27)
	if err != nil {
		return nil, nil, err
	}
	prod.DecimalScaleFactor = dsf

	unit, err := r.u8(18)
	if err != nil {
		return nil, nil, err
	}
	p1, err := r.u8(19)
	if err != nil {
		return nil, nil, err
	}
	p2, err := r.u8(20)
	if err != nil {
		return nil, nil, err
	}
	tri, err := r.u8(21)
	if err != nil {
		return nil, nil, err
	}
	nAvg, err := r.u16(22)
	if err != nil {
		return nil, nil, err
	}
	nMiss, err := r.u8(24)
	if err != nil {
		return nil, nil, err
	}
	prod.TimeRangeUnit = int(unit)
	prod.P1, prod.P2 = int(p1), int(p2)
	prod.TimeRangeIndicator = int(tri)
	prod.NumberInAverage = int(nAvg)
	prod.NumberMissing = int(nMiss)

	// octets 29-40 are reserved (present only when the section length exceeds 40);
	// octet 41 and onward hold the local definition. Read the local definition
	// number only when it exists — a PDS of exactly 40 bytes or less has none.
	if sec.Length > 40 {
		localDef, err := r.u8(41)
		if err != nil {
			return nil, nil, err
		}
		id.LocalDefinition = int(localDef)
		if err := parseMarsLabelling(r, id); err != nil {
			return nil, nil, err
		}
	}

	prod.ParameterName = parameterName(id.Centre, id.TableVersion, prod.Parameter)
	return id, prod, nil
}

// ForecastTime returns the forecast period (P1 converted via TimeRangeUnit) and
// whether the unit has the concept of a period at all.
//
// A numeric result is given only for units with a fixed duration (minutes, hours,
// days). Months, years and decades have no fixed duration, so ok is false there and
// the caller must interpret P1/P2 against TimeRangeUnit itself. This matches the
// GRIB2 side's Product.ForecastTime (which likewise refuses to convert units >= 3).
func (p *Product) ForecastTime() (d time.Duration, ok bool) {
	unit, ok := timeRangeUnitDuration(p.TimeRangeUnit)
	if !ok {
		return 0, false
	}
	return time.Duration(p.forecastCount()) * unit, true
}

// forecastCount is P1 as the table 5 wording defines the forecast period.
// Indicator 10 is special: "P1 occupies octets 19 and 20", so its period is
// the 16-bit big-endian pair (octet 19 << 8 | octet 20) rather than a single
// octet — ecCodes computes stepRange the same way (P1=6, P2=12, unit=3h →
// 4644h). Every other indicator uses octet 19 alone.
func (p *Product) forecastCount() int {
	if p.TimeRangeIndicator == 10 {
		return p.P1<<8 | p.P2
	}
	return p.P1
}

// timeRangeUnitDuration maps a table 4 time-range unit code onto its fixed
// duration. Months, years, decades and unrecognised codes have no fixed
// duration and yield ok=false.
func timeRangeUnitDuration(unit int) (time.Duration, bool) {
	switch unit {
	case 0: // minute
		return time.Minute, true
	case 1: // hour
		return time.Hour, true
	case 2: // day
		return 24 * time.Hour, true
	case 10:
		return 3 * time.Hour, true
	case 11:
		return 6 * time.Hour, true
	case 12:
		return 12 * time.Hour, true
	case 13:
		return 15 * time.Minute, true
	case 14:
		return 30 * time.Minute, true
	case 254: // second (the ecCodes extension)
		return time.Second, true
	default:
		return 0, false
	}
}

// StepRange returns the product's time range as (begin, end) offsets relative
// to the section-1 reference time, following the WMO table 5 wording encoded
// in table5TimeRanges (transcribed from ecCodes' definitions/grib1/5.table).
//
// The table 5 indicator decides which of P1/P2 are interval bounds and with
// which sign:
//
//	0, 1, 10, 51 — instantaneous / initialised: begin = end = P1 (P1=0 for 1;
//	               indicator 10's P1 is the two-octet pair 19-20)
//	2, 3, 4, 5   — range given as reference+P1 .. reference+P2
//	6            — average over reference-P1 .. reference-P2 (both negative)
//	7            — average over reference-P1 .. reference+P2
//	113-125      — statistics over N forecasts: only the per-product forecast
//	               period P1 is well-defined, so begin = end = P1
//
// Anything else (including an unknown indicator) is reported as begin = end =
// P1: the forecast time itself is always valid, the interval is the part this
// library refuses to invent. oks false only for units without a fixed
// duration (months, years, …).
func (p *Product) StepRange() (begin, end time.Duration, ok bool) {
	unit, ok := timeRangeUnitDuration(p.TimeRangeUnit)
	if !ok {
		return 0, 0, false
	}
	p1 := time.Duration(p.forecastCount()) * unit
	p2 := time.Duration(p.P2) * unit
	switch p.TimeRangeIndicator {
	case 6: // reference-P1 .. reference-P2
		return -p1, -p2, true
	case 7: // reference-P1 .. reference+P2
		return -p1, p2, true
	case 2, 3, 4, 5: // reference+P1 .. reference+P2
		return p1, p2, true
	default: // 0, 1, 10, 51, 113-125, unknown: only P1 is well-defined
		return p1, p1, true
	}
}

// TimeRange2 returns P2 as a duration when the time-range unit has a fixed
// duration. It is the raw building block StepRange uses; exposed because
// table 5 variants outside the well-defined set still want P2's magnitude.
func (p *Product) TimeRange2() (time.Duration, bool) {
	unit, ok := timeRangeUnitDuration(p.TimeRangeUnit)
	if !ok {
		return 0, false
	}
	return time.Duration(p.P2) * unit, true
}

// LevelDescription renders the level as a readable string, e.g. "1000 hPa",
// "2 m" or "101-104 layer". Layer pairs show the range; a missing level is
// reported as missing, not fabricated.
func (p *Product) LevelDescription() string {
	unit := levelTypeUnit(p.LevelType)
	if p.LevelMissing {
		return "level missing"
	}
	if p.LayerRange {
		if unit != "" && unit != "layer" {
			return fmt.Sprintf("%d-%d %s", p.LevelTop, p.LevelBottom, unit)
		}
		return fmt.Sprintf("%d-%d", p.LevelTop, p.LevelBottom)
	}
	if unit != "" && unit != "layer" {
		return fmt.Sprintf("%d %s", p.LevelTop, unit)
	}
	return fmt.Sprintf("%d", p.LevelTop)
}

// TimeUnitName returns the name of the table 4 time unit.
func (p *Product) TimeUnitName() string { return timeUnitName(p.TimeRangeUnit) }

// TimeRangeName returns the meaning of the table 5 time range indicator.
func (p *Product) TimeRangeName() string { return timeRangeName(p.TimeRangeIndicator) }
