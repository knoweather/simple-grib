package grib

import (
	"math"
	"time"

	"github.com/knoweather/simple-grib/internal/octetio"
	"github.com/knoweather/simple-grib/spec"
)

// WMO/ecCodes "missing" sentinel values for section 4 fixed-surface fields.
const (
	missingValueU32     = uint32(0xFFFFFFFF) // unsigned[4] scaled value (Note 168)
	missingScaleFactor8 = uint8(0xFF)        // signed[1] scale factor (sign-magnitude)
)

// mulDuration multiplies a non-negative count by the unit duration, saturating
// at MaxInt64 rather than letting int64 overflow (bug.md §2.2). saturated
// reports whether saturation happened.
func mulDuration(raw uint32, unit time.Duration) (d time.Duration, saturated bool) {
	if raw == 0 || unit <= 0 {
		return 0, false
	}
	if int64(raw) > int64(math.MaxInt64)/int64(unit) {
		return time.Duration(math.MaxInt64), true
	}
	return time.Duration(int64(raw) * int64(unit)), false
}

// ForecastTime is the forecast-time part of a product definition. Unit is a
// table 4.4 code (0 = minutes, 1 = hours, 2 = days, 3 = months, 4 = years,
// ...); Raw is the stored count as-is, and Value converts Raw to a duration
// for units 0-2. For units >= 3 (months/years/...) there is no fixed length,
// so Value stays 0 and callers must interpret Unit themselves.
//
// Raw counts whose true duration exceeds time.Duration (about 292 years)
// saturate Value at MaxInt64 and set Overflow — the previous implementation
// let the multiplication wrap silently into a negative duration
// (bug.md §2.2).
type ForecastTime struct {
	Unit     int
	Raw      uint32        // original count in units of Unit (template 4.0 octets 19-22)
	Value    time.Duration // Raw × unit; saturates at MaxInt64 on overflow
	Overflow bool          // true when Value saturated
}

// Level is one fixed surface of a product definition: Type is a table 4.5
// code (e.g. 1 = isobaric surface (Pa), 3 = height above ground), Scale the
// decoded value obtained by applying the WMO scale factor to the raw integer
// (e.g. type 1 with scale factor 0 and raw 850 → 850 hPa).
type Level struct {
	Type     int
	Scale    float64 // decoded level value; meaningful only when HasValue is true
	HasValue bool    // false when the scaled-value octets are missing (all ones) — WMO Note 168 / ecCodes can_be_missing: this type carries no numeric level
}

// Product is the parsed section 4. The common core fields are extracted by
// absolute octet position and table-reference lookup, so they work across
// product templates without template-specific code. For a local or unknown
// template only TemplateNumber and NV are filled; the remaining fields stay
// zero-valued / nil.
type Product struct {
	// Edition is the message's GRIB edition (1 or 2). It determines the code
	// space of every code below: GRIB1 parameter codes come from table 2,
	// level type codes from table 3, and the generating-process id is a
	// centre-defined code; the GRIB2 counterparts come from tables 4.2 /
	// 4.5 / 4.3. Equal numbers do not imply equal meaning.
	Edition                 int
	TemplateNumber          int
	ParameterCategory       int // octet 10, table 4.1
	ParameterNumber         int // octet 11, table 4.2 (resolved per the discipline)
	ParameterName           string
	TypeOfGeneratingProcess int           // first TableRef=4.3 field (octet 12 in template 4.0)
	ForecastTime            *ForecastTime // nil = the template has no time-unit field
	Level1, Level2          *Level        // first/second TableRef=4.5 field; nil = absent
	NV                      int           // section 4 octets 6-7: number of coordinate values
}

// Product parses section 4.
func (m *Message) Product() (*Product, error) {
	if m.g1 != nil {
		return productFromGRIB1(m.g1), nil
	}
	sec, err := m.Section(4)
	if err != nil {
		return nil, err
	}
	r := octetio.New(sec.Data)
	nv, _ := r.U16(5)     // octets 6-7: NV
	tmplNo, _ := r.U16(7) // octets 8-9: template number

	p := &Product{Edition: 2, TemplateNumber: int(tmplNo), NV: int(nv)}
	def, ok := spec.Template(4, int(tmplNo))
	if !ok {
		return p, nil // local/unknown template: metadata only; Values() fails loudly in the pack layer
	}
	w, err := spec.NewWalk(def, sec.Data)
	if err != nil {
		return nil, err
	}

	// Parameter category = octet 10 (TableRef 4.1); number = octet 11
	// (TableRef 4.2).
	if i, ok := fieldAtOctet(def, 10, 4, 1); ok {
		p.ParameterCategory = int(w.At(i).U)
	}
	if i, ok := fieldAtOctet(def, 11, 4, 2); ok {
		p.ParameterNumber = int(w.At(i).U)
		if name, err := spec.ParameterName(m.Discipline(), p.ParameterCategory, p.ParameterNumber); err == nil {
			p.ParameterName = name
		}
	}

	// Generating process: the first TableRef 4.3 field.
	if i, ok := firstTableField(def, 4, 3); ok {
		p.TypeOfGeneratingProcess = int(w.At(i).U)
	}

	// Forecast time: the first TableRef 4.4 field (unit) followed by the
	// 4-byte raw count (WMO "forecast time in units defined by the table 4.4
	// octet"; not scaled, GRIB2 §4.0).
	if i, ok := firstTableField(def, 4, 4); ok {
		f := def.Fields[i]
		rawOff := f.Start + f.Count
		if rawOff+4 <= len(sec.Data) {
			raw, _ := r.U32(rawOff)
			ft := &ForecastTime{Unit: int(w.At(i).U), Raw: raw}
			switch ft.Unit {
			case 0: // minutes
				ft.Value, ft.Overflow = mulDuration(raw, time.Minute)
			case 1: // hours
				ft.Value, ft.Overflow = mulDuration(raw, time.Hour)
			case 2: // days
				ft.Value, ft.Overflow = mulDuration(raw, 24*time.Hour)
			}
			p.ForecastTime = ft
		}
	}

	// Levels: the first and second TableRef 4.5 fields, each followed by
	// scale (1 byte) + value (4 bytes).
	if idxs := tableFields(def, 4, 5); len(idxs) > 0 {
		p.Level1 = m.levelFrom(sec.Data, r, def.Fields[idxs[0]])
	}
	if idxs := tableFields(def, 4, 5); len(idxs) > 1 {
		p.Level2 = m.levelFrom(sec.Data, r, def.Fields[idxs[1]])
	}
	return p, nil
}

// levelFrom reads a TableRef=4.5 field (type) plus the scale byte and 4-octet
// scaled value that follow; on truncation it returns nil (fail-soft: the
// field is simply not reported).
//
// The field shapes follow ecCodes template.4.horizontal.def:
//   - the scale factor is signed[1] in sign-magnitude form, decoded like
//     grib_decode_signed_long (bit 7 = sign); 0xFF is its can_be_missing
//     sentinel;
//   - the scaled value is unsigned[4]; 0xFFFFFFFF is the WMO "missing"
//     sentinel (Note 168: fixed-surface types without a numeric level leave
//     it unused).
//
// When either is missing, HasValue stays false and Scale stays 0 — callers
// must check HasValue before using Scale.
func (m *Message) levelFrom(data []byte, r *octetio.Reader, f spec.Field) *Level {
	typeVal, err := r.U8(f.Start)
	if err != nil {
		return nil
	}
	off := f.Start + f.Count
	if off+5 > len(data) {
		return nil
	}
	scaleByte, _ := r.U8(off)
	raw, _ := r.U32(off + 1)
	lv := &Level{Type: int(typeVal)}
	if scaleByte != missingScaleFactor8 && raw != missingValueU32 {
		lv.Scale = spec.Scale(raw, spec.Signed8(scaleByte))
		lv.HasValue = true
	}
	return lv
}

// fieldAtOctet finds the field starting at 1-based section octet `octet`
// with table reference (tsec, tflag).
func fieldAtOctet(def *spec.TemplateDef, octet, tsec, tflag int) (int, bool) {
	for i, f := range def.Fields {
		if f.Start+1 == octet && f.TableSec == tsec && f.TableFlag == tflag {
			return i, true
		}
	}
	return 0, false
}

// firstTableField finds the first field with table reference (tsec, tflag).
func firstTableField(def *spec.TemplateDef, tsec, tflag int) (int, bool) {
	idxs := tableFields(def, tsec, tflag)
	if len(idxs) == 0 {
		return 0, false
	}
	return idxs[0], true
}

// tableFields lists all fields with table reference (tsec, tflag), in
// definition order.
func tableFields(def *spec.TemplateDef, tsec, tflag int) []int {
	var out []int
	for i, f := range def.Fields {
		if f.TableSec == tsec && f.TableFlag == tflag {
			out = append(out, i)
		}
	}
	return out
}
