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
	var unit time.Duration
	switch p.TimeRangeUnit {
	case 0, 254: // minutes (table 4 code 0) / seconds (code 254, the ecCodes extension)
		if p.TimeRangeUnit == 0 {
			unit = time.Minute
		} else {
			unit = time.Second
		}
	case 1:
		unit = time.Hour
	case 2:
		unit = 24 * time.Hour
	case 10:
		unit = 3 * time.Hour
	case 11:
		unit = 6 * time.Hour
	case 12:
		unit = 12 * time.Hour
	case 13:
		unit = 15 * time.Minute
	case 14:
		unit = 30 * time.Minute
	default:
		return 0, false
	}
	return time.Duration(p.P1) * unit, true
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
