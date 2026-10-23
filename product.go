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
//
// ScaleFactorRaw / ScaledValueRaw keep the raw section bytes so callers that
// must reproduce ecCodes' per-key view (scaleFactorOfFirstFixedSurface /
// scaledValueOfFirstFixedSurface, which the parameter concepts condition on)
// can: (0, 2) and (1, 20) decode to the same Scale but are different keys.
type Level struct {
	Type     int
	Scale    float64 // decoded level value; meaningful only when HasValue is true
	HasValue bool    // false when the scaled-value octets are missing (all ones) — WMO Note 168 / ecCodes can_be_missing: this type carries no numeric level

	// ScaleFactorRaw / ScaledValueRaw are the untouched section bytes: the
	// sign-magnitude scale-factor octet (0xFF = missing) and the 4-octet
	// scaled value (0xFFFFFFFF = missing). Callers reproducing ecCodes'
	// per-key view of a surface (scaleFactorOfFirstFixedSurface /
	// scaledValueOfFirstFixedSurface, which the parameter concepts condition
	// on) need the pair, not the decoded Scale: (0, 2) and (1, 20) decode to
	// the same value but are different keys.
	ScaleFactorRaw uint8
	ScaledValueRaw uint32
}

// ProbabilityLimit is one event threshold of a probability template: the
// sign-magnitude scale factor octet and the 4-octet scaled value, kept raw
// for the same reason as Level's raw fields — the parameter concepts
// condition on the exact (scaleFactor, scaledValue) key pair ("at least
// 1 mm" is (0,1), and a (−1,10) encoding of the same value must not match
// that entry). Missing sentinels follow the Level convention: 0xFF /
// 0xFFFFFFFF, with HasValue false.
type ProbabilityLimit struct {
	ScaleFactorRaw uint8
	ScaledValueRaw uint32
	HasValue       bool
}

// ScaledField is one GRIB2 sign-magnitude field (a `signed[1]` scale factor or
// a `signed[4]` scaled value) from the chemistry/aerosol product templates.
// Valid is false when the field holds the all-ones missing sentinel or the
// section is truncated.
type ScaledField struct {
	Value int
	Valid bool
}

// Chemical carries the section-4 chemistry and aerosol keys. They exist only
// on the templates that WMO dedicates to atmospheric chemical constituents,
// aerosols and optical properties (4.40–4.50, 4.57–4.85, 4.108–4.111 and
// 4.124–4.199). A negative interval/type field means the template has no such
// field.
type Chemical struct {
	ConstituentType                   int // table 4.230, or the 4.233/CCT-14 aerosol type
	AerosolType                       int // alias of ConstituentType on aerosol templates; -1 otherwise
	SourceSinkChemicalPhysicalProcess int // table 4.238; -1 absent

	TypeOfSizeInterval      int // table 4.91, size interval; -1 absent
	ScaleFactorOfFirstSize  ScaledField
	ScaledValueOfFirstSize  ScaledField
	ScaleFactorOfSecondSize ScaledField
	ScaledValueOfSecondSize ScaledField

	TypeOfWavelengthInterval      int // table 4.91, wavelength interval; -1 absent
	ScaleFactorOfFirstWavelength  ScaledField
	ScaledValueOfFirstWavelength  ScaledField
	ScaleFactorOfSecondWavelength ScaledField
	ScaledValueOfSecondWavelength ScaledField
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
	Edition           int
	TemplateNumber    int
	ParameterCategory int // octet 10, table 4.1
	ParameterNumber   int // octet 11, table 4.2 (resolved per the discipline)
	// ParameterName is ecCodes' "parameterName": the title of the table 4.2 row
	// for (discipline, category, number) **in the table version this message
	// declares** (section 1 octets 10/11, plus the originating centre for the
	// local tables) — not the newest WMO table. When the declared version has
	// no such table, or the table has no such code, ecCodes renders the bare
	// decimal code ("224", "13", "0"); ParameterName does the same, so it is
	// never empty for a parsed GRIB2 parameter. See spec.ParameterNameFor and
	// spec.ParameterName (the version-independent, always-newest variant).
	//
	// On GRIB1 it stays whatever grib1's table 2 registration produced (empty
	// until RegisterTable2 registers that centre/version).
	ParameterName           string
	TypeOfGeneratingProcess int           // first TableRef=4.3 field (octet 12 in template 4.0)
	ForecastTime            *ForecastTime // nil = the template has no time-unit field
	// EndOfOverallInterval is the "time of end of overall time interval"
	// block carried by the WMO statistical product templates (4.8-4.14). nil
	// when the template has no such field, when the field sits at a
	// parametrized position (see spec.EndIntervalParametrized), when the
	// bytes are truncated or name an impossible date, and always on GRIB1.
	//
	// It is what turns ForecastTime (the interval *start*) into an interval
	// *end* — the anchor for ecCodes' "step" and "validityDate". On the WMO
	// statistical templates the 4.4 forecast-time field marks the beginning
	// of the overall interval, not the end, so a parser that reads only
	// ForecastTime reports step=0 and a validity time one full interval too
	// early on every accumulated / averaged product (tp 6h, mx2t 24h, …).
	//
	// On GRIB1 the equivalent range lives in the section-1 P1/P2 pair under
	// table 5 time-range semantics; Message.StepRange decodes it, this field
	// stays nil there on purpose (different code space, see Edition).
	EndOfOverallInterval *time.Time
	// TypeOfStatisticalProcessing is the table 4.7 code of the outermost
	// time range of the WMO statistical templates (4.8-4.14: 0 = average,
	// 1 = accumulation, 2 = maximum, …). -1 when the template has no such
	// field at a fixed position (instantaneous templates, and the few whose
	// field sits at a parametrized position). The parameter concepts'
	// shortName entries for accumulated / averaged fields condition on this
	// value, so without it "6h accumulation of total precipitation" cannot
	// be told apart from instantaneous specific humidity sharing the same
	// (discipline, category, number).
	TypeOfStatisticalProcessing int
	Level1, Level2              *Level // first/second TableRef=4.5 field; nil = absent
	// ProbabilityType is the table 4.9 code of the probability templates
	// (4.5/4.9 and derivatives, including the NT-parametrized 4.112/4.123:
	// 0 = probability of event below lower limit, 1 = above upper limit,
	// 3 = within intervals, …). -1 when the template has no probability
	// fields or the section is too short to hold them. The event thresholds
	// follow it — see ProbabilityLower / ProbabilityUpper.
	ProbabilityType  int
	ProbabilityLower *ProbabilityLimit // 事件下限；nil = 模板无概率字段
	ProbabilityUpper *ProbabilityLimit // 事件上限；nil = 同上
	// Chemical is non-nil only for the chemistry/aerosol templates; it keeps
	// the table 4.230/4.233 constituent, table 4.238 source/sink process and
	// the size/wavelength intervals the parameter concepts condition on.
	Chemical *Chemical
	NV       int // section 4 octets 6-7: number of coordinate values
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

	p := &Product{Edition: 2, TemplateNumber: int(tmplNo), NV: int(nv), TypeOfStatisticalProcessing: -1, ProbabilityType: -1}
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
		// ecCodes' parameterName is the codetable_title of parameterNumber, and
		// the codetable itself is versioned: the master directory is
		// grib2/tables/<masterTablesVersionNumber> (section 1 octet 10), with
		// grib2/tables/local/<centre mnemonic>/<localTablesVersion> merged in
		// when octet 11 is neither 0 nor 255. spec.ParameterNameFor replicates
		// that lookup, including ecCodes' fallback to the bare decimal code.
		// Section 1 is mandatory in GRIB2; if it is absent or unparsable the
		// versions are unknown, and version 0 selects no table directory
		// either — the same numeric fallback ecCodes produces.
		ref := spec.ParamNameRef{Discipline: m.Discipline(), Category: p.ParameterCategory, Number: p.ParameterNumber}
		if id, err := m.Identification(); err == nil {
			ref.MasterVersion = id.MasterTableVersion
			ref.LocalVersion = id.LocalTableVersion
			ref.Centre = id.Centre
		}
		p.ParameterName = spec.ParameterNameFor(ref)
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

	// End of the statistical interval: the "year…end of overall time
	// interval …" block of the WMO statistical templates (4.8-4.14). Absent
	// on every other template, and absent (nil) for the templates where the
	// field sits at a parametrized position — see spec.EndIntervalOffset.
	p.EndOfOverallInterval = endOfOverallInterval(def.ID, r, sec.Data)

	// Type of statistical processing (table 4.7): the byte ecCodes exposes as
	// typeOfStatisticalProcessing. For messages with more than one time range
	// ecCodes' scalar key is the LAST record of the repeated block (verified
	// against 2.47), so spec.StatProcessOffsetFor walks (NT-1)*12 bytes to it.
	// -1 when the template has none at a fixed position. ~2000 entries of the
	// ECMWF parameter concepts condition on this value, so it is what turns
	// "unknown local parameter" into the right shortName for
	// accumulated/averaged fields.
	if off, ok := spec.StatProcessOffsetFor(def.ID, sec.Data); ok && int(off) < len(sec.Data) {
		if v, err := r.U8(off); err == nil {
			p.TypeOfStatisticalProcessing = int(v)
		}
	}

	// 概率模板（4.5/4.9 及派生）的 event 阈值：Probability type 之后的布局
	// 在所有模板里一致（lower: +1 scale/+2..+5 value；upper: +6/+7..+10），
	// 由 specgen 校验过的锚点保证。4.112/4.123 的概率块跟着 time-range
	// 列表平移，位置由 NT（section 4 八位组 42）算出。
	if off, ok := spec.ProbTypeOffset(def.ID); ok && off+11 <= len(sec.Data) {
		p.ProbabilityType = int(mustU8(r, off))
		p.ProbabilityLower = probabilityLimit(mustU8(r, off+1), mustU32(r, off+2))
		p.ProbabilityUpper = probabilityLimit(mustU8(r, off+6), mustU32(r, off+7))
	} else if off, ok := spec.ProbTypeOffsetParametrized(def.ID, sec.Data); ok {
		p.ProbabilityType = int(mustU8(r, off))
		p.ProbabilityLower = probabilityLimit(mustU8(r, off+1), mustU32(r, off+2))
		p.ProbabilityUpper = probabilityLimit(mustU8(r, off+6), mustU32(r, off+7))
	}

	p.Chemical = chemicalFrom(def, sec.Data)
	return p, nil
}

func mustU8(r *octetio.Reader, off int) uint8 {
	v, _ := r.U8(off)
	return v
}

func mustU32(r *octetio.Reader, off int) uint32 {
	v, _ := r.U32(off)
	return v
}

func probabilityLimit(sf uint8, sv uint32) *ProbabilityLimit {
	l := &ProbabilityLimit{ScaleFactorRaw: sf, ScaledValueRaw: sv}
	l.HasValue = sf != missingScaleFactor8 && sv != missingValueU32
	return l
}

// chemicalFrom extracts the chemistry/aerosol keys from a product template by
// table reference. It returns nil when the template has none of them, so
// non-chemical products pay only one pass over their (short) field list.
//
// The size/wavelength blocks are identical to ecCodes
// template.4.aerosol_size.def / template.4.optical.def: one table-4.91
// interval byte, then signed[1] scale factor + signed[4] scaled value for the
// first and second bounds. Aerosol templates put the size block first and the
// wavelength block second; the optical-only templates 4.108–4.111 have a
// single wavelength block and no aerosol type.
func chemicalFrom(def *spec.TemplateDef, data []byte) *Chemical {
	c := &Chemical{
		ConstituentType:                   -1,
		AerosolType:                       -1,
		SourceSinkChemicalPhysicalProcess: -1,
		TypeOfSizeInterval:                -1,
		TypeOfWavelengthInterval:          -1,
	}
	var intervals []int
	found := false
	for _, f := range def.Fields {
		switch {
		case f.TableSec == 4 && f.TableFlag == 230 && f.Count == 2:
			if v, ok := readU16(data, f.Start); ok {
				c.ConstituentType = int(v)
				found = true
			}
		case (f.TableSec == 4 && f.TableFlag == 233) ||
			(f.TableSec == 255 && f.TableFlag == 14): // CCT-14 aerosol type
			if v, ok := readU16(data, f.Start); ok {
				c.AerosolType = int(v)
				c.ConstituentType = int(v) // ecCodes alias
				found = true
			}
		case f.TableSec == 4 && f.TableFlag == 238 && f.Count == 1:
			if f.Start < len(data) {
				c.SourceSinkChemicalPhysicalProcess = int(data[f.Start])
				found = true
			}
		case f.TableSec == 4 && f.TableFlag == 91 && f.Count == 1:
			intervals = append(intervals, f.Start)
		}
	}
	if len(intervals) > 0 {
		sizeOff, waveOff := -1, -1
		if c.AerosolType >= 0 {
			sizeOff = intervals[0]
			if len(intervals) > 1 {
				waveOff = intervals[1]
			}
		} else {
			waveOff = intervals[0]
		}
		if sizeOff >= 0 && sizeOff+11 <= len(data) {
			c.TypeOfSizeInterval = int(data[sizeOff])
			c.ScaleFactorOfFirstSize = signed8Field(data[sizeOff+1])
			c.ScaledValueOfFirstSize = signed32Field(data[sizeOff+2 : sizeOff+6])
			c.ScaleFactorOfSecondSize = signed8Field(data[sizeOff+6])
			c.ScaledValueOfSecondSize = signed32Field(data[sizeOff+7 : sizeOff+11])
			found = true
		}
		if waveOff >= 0 && waveOff+11 <= len(data) {
			c.TypeOfWavelengthInterval = int(data[waveOff])
			c.ScaleFactorOfFirstWavelength = signed8Field(data[waveOff+1])
			c.ScaledValueOfFirstWavelength = signed32Field(data[waveOff+2 : waveOff+6])
			c.ScaleFactorOfSecondWavelength = signed8Field(data[waveOff+6])
			c.ScaledValueOfSecondWavelength = signed32Field(data[waveOff+7 : waveOff+11])
			found = true
		}
	}
	if !found {
		return nil
	}
	return c
}

func readU16(data []byte, off int) (uint16, bool) {
	if off < 0 || off+2 > len(data) {
		return 0, false
	}
	return uint16(data[off])<<8 | uint16(data[off+1]), true
}

func signed8Field(b byte) ScaledField {
	if b == missingScaleFactor8 {
		return ScaledField{}
	}
	return ScaledField{Value: spec.Signed8(b), Valid: true}
}

func signed32Field(b []byte) ScaledField {
	u := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	if u == missingValueU32 {
		return ScaledField{}
	}
	return ScaledField{Value: spec.Signed32(u), Valid: true}
}

// endIntervalLen is the width of the WMO end-of-interval timestamp:
// 2-byte year plus month/day/hour/minute/second (1 byte each).
const endIntervalLen = 8

// endOfOverallInterval reads the "time of end of overall time interval"
// timestamp, or returns nil when the template has no such field at a static
// offset or the bytes are truncated/invalid. Invalid dates are rejected the
// same way Identification rejects them — a silently normalised date (Feb 30)
// is a corrupting lie.
func endOfOverallInterval(templateID string, r *octetio.Reader, data []byte) *time.Time {
	off, ok := spec.EndIntervalOffset(templateID)
	if !ok || off < 0 {
		return nil
	}
	if len(data) < off+endIntervalLen {
		return nil
	}
	year, _ := r.U16(off)
	month, _ := r.U8(off + 2)
	day, _ := r.U8(off + 3)
	hour, _ := r.U8(off + 4)
	minute, _ := r.U8(off + 5)
	second, _ := r.U8(off + 6)
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 || second > 59 {
		return nil
	}
	t := time.Date(int(year), time.Month(month), int(day), int(hour), int(minute), int(second), 0, time.UTC)
	if t.Year() != int(year) || t.Month() != time.Month(month) || t.Day() != int(day) {
		return nil
	}
	return &t
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
	lv := &Level{Type: int(typeVal), ScaleFactorRaw: scaleByte, ScaledValueRaw: raw}
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
