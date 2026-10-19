//
// GRIB2 template 5.3 "grid point data - complex packing with spatial
// differencing" decoding. The algorithm is calibrated against the eccodes
// reference implementation (ecmwf/eccodes master: unpack + post_process in
// src/eccodes/accessor/DataG22OrderPacking.cc, grib_decode_signed_longb in
// grib_bits.cc) and cross-validated from two directions:
// - an independent Python reimplementation checked against eccodes 2.41
// ground truth on S1_hrrr_f00_PRES_L25.grib2: 0/1,193,781 mismatches;
// - the independent oracle script scripts/xcheck/complex53-oracle.py,
// checked against eccodes on C6 (0/259,920) and S1 (0/1,193,781):
// max relative error = 0.

package pack

import (
	"fmt"
	"math"

	"github.com/knoweather/simple-grib/internal/octetio"
)

// TemplateComplex is data-representation template 5.3 (complex packing with
// spatial differencing). Template 5.2 (plain complex packing, no spatial
// differencing) shares this code path with orderOfSpatialDifferencing == 0 —
// eccodes binds both section 7 templates to the same accessor
// (templates/template.7.2.def: constant orderOfSpatialDifferencing = 0 and
// numberOfOctetsExtraDescriptors = 0, so ref_p starts at bit 0 with no
// extras/bias header and post_process is skipped — verified in
// DataG22OrderPacking.cc's unpack).
//
// Public AWS archives contain no real-world 5.2 samples, so acceptance for
// template 5.2 relies on a synthetic bidirectional fixture,
// complex_52_plain.grib2 (eccodes ground truth [100,102,…,120]).
const TemplateComplex = 3

// TemplateComplexPlain is data-representation template 5.2 (grid point data
// - complex packing, no spatial differencing).
const TemplateComplexPlain = 2

// ComplexPacking holds the template 5.3 header fields (section 5, 49 bytes
// total; field order per the spec table "5.3" and eccodes
// templates/template.5.3.def — both verified byte-by-byte against the S1
// sample).
type ComplexPacking struct {
	R                float32 // octets 12-15: reference value (pre-scaled by 10^D)
	B                int16   // octets 16-17: binary scale factor (sign-magnitude)
	D                int16   // octets 18-19: decimal scale factor (sign-magnitude)
	BitsPerVal       uint8   // octet 20: bits per group reference value
	OrigType         uint8   // octet 21: type of original field values (table 5.1)
	GroupSplit       uint8   // octet 22: group splitting method (table 5.4; parsed only, not used by the decoder)
	MissingMgmt      uint8   // octet 23: missing value management (table 5.5)
	PrimaryMiss      uint32  // octets 24-27: primary missing value substitute
	SecondaryMiss    uint32  // octets 28-31: secondary missing value substitute
	NumGroups        uint32  // octets 32-35: number of groups of data values (NG)
	RefGroupWidths   uint8   // octet 36: reference for group widths
	BitsGroupWidths  uint8   // octet 37: bits used for (reference-subtracted) group widths
	RefGroupLengths  uint32  // octets 38-41: reference for group lengths
	LenIncrement     uint8   // octet 42: group length increment
	TrueLastLen      uint32  // octets 43-46: actual length of the last group
	BitsScaledLens   uint8   // octet 47: bits used for scaled group lengths
	OrderSpatialDiff uint8   // octet 48: order of spatial differencing (table 5.6: 1 or 2)
	OctetsExtraDesc  uint8   // octet 49: number of octets for extra descriptors
}

// complexDecode implements templates 5.2/5.3 (plain complex packing /
// complex packing with spatial differencing — the same eccodes accessor,
// DataG22OrderPacking). The section 7 layout, verified against eccodes (see
// the package comment):
//
//	[extras × order, each noed×8 bits, unsigned]
//	[bias: noed×8 bits, sign-magnitude (bit 0 = sign)] (only for order > 0)
//	[refs: NG×bpv bits] (block starts right after the header)
//	[widths: NG×nbwg bits] (byte-aligned start)
//	[lengths: NG×nbsl bits] (byte-aligned start)
//	[values: Σ_i len_i×width_i bits] (byte-aligned start)
//
// Group i: width_i = the nbwg-bit read value + refGroupWidths;
// len_i = (the nbsl-bit read value)×lenIncrement + refGroupLengths (the last
// group takes trueLengthOfLastGroup); value_j = ref_i + the width_i-bit read
// value.
// Spatial-differencing reconstruction (post_process): order 2 —
// v0 = extras[0], v1 = extras[1] (overwritten), vj = coded_j + bias +
// 2·v(j-1) − v(j-2); order 1 — v0 = extras[0], vj = coded_j + bias + v(j-1).
// Final step: value = (raw × 2^B + R) × 10^(−D).
func complexDecode(tmpl int, p ComplexPacking, data []byte, numValues int) ([]float32, error) {
	if p.NumGroups == 0 {
		// eccodes ECC-2095 treats NG==0 as a constant field, but its return
		// path skips the (×2^B+R)×10^(−D) scaling — an uncalibrated edge
		// case with no real-world sample. Fail loud.
		return nil, fmt.Errorf("pack: template %d: numberOfGroupsOfDataValues = 0 (constant-field complex packing is not calibrated; refuse to guess)", tmpl)
	}
	switch p.OrderSpatialDiff {
	case 0: // plain complex packing (5.2): no header, identity reconstruction
	case 1, 2:
	default:
		return nil, &ErrUnsupportedValue{Template: tmpl, Field: "order of spatial differencing", Value: int64(p.OrderSpatialDiff)}
	}
	if p.OctetsExtraDesc == 0 && p.OrderSpatialDiff > 0 {
		// Degenerate header (no extra descriptors) with spatial differencing
		// enabled: no calibrated sample, refuse to guess. (With osd=0,
		// noed=0 is normal — template 5.2 has no such field at all.)
		return nil, &ErrUnsupportedValue{Template: tmpl, Field: "number of octets extra descriptors", Value: 0}
	}
	switch p.MissingMgmt {
	case 0: // no explicit missing values — the only calibrated case
	default:
		return nil, &ErrUnsupportedValue{Template: tmpl, Field: "missing value management used", Value: int64(p.MissingMgmt)}
	}
	switch p.OrigType {
	case 0, 1:
	default:
		return nil, &ErrUnsupportedValue{Template: tmpl, Field: "type of original field values", Value: int64(p.OrigType)}
	}

	order := int(p.OrderSpatialDiff)
	noedBits := int(p.OctetsExtraDesc) * 8
	ng := int(p.NumGroups)

	// Block layout (all byte-aligned starts, per the eccodes unpack):
	headerBits := (1 + order) * noedBits // extras×order + bias
	refBits := ng * int(p.BitsPerVal)
	widthBits := ng * int(p.BitsGroupWidths)
	lenBits := ng * int(p.BitsScaledLens)
	offWidth := (refBits + headerBits + 7) / 8 // ceil((NG×bpv + header)/8)
	offLen := offWidth + (widthBits+7)/8
	offVals := offLen + (lenBits+7)/8

	brW := octetio.NewBitReaderAt(data, offWidth*8)
	brL := octetio.NewBitReaderAt(data, offLen*8)
	brV := octetio.NewBitReaderAt(data, offVals*8)

	// section 7 header extras + bias (bits [0, headerBits));
	// group reference values then start at bit headerBits.
	brHead := octetio.NewBitReader(data)
	extras := make([]int64, order)
	for i := 0; i < order; i++ {
		v, err := brHead.ReadU(noedBits)
		if err != nil {
			return nil, fmt.Errorf("pack: template %d: extra descriptor %d: %w", tmpl, i, err)
		}
		extras[i] = int64(v)
	}
	bias := int64(0)
	if order > 0 {
		sign, err := brHead.ReadU(1)
		if err != nil {
			return nil, fmt.Errorf("pack: template %d: bias sign bit: %w", tmpl, err)
		}
		mag, err := brHead.ReadU(noedBits - 1)
		if err != nil {
			return nil, fmt.Errorf("pack: template %d: bias magnitude: %w", tmpl, err)
		}
		bias = int64(mag)
		if sign != 0 {
			bias = -bias
		}
	}
	brRef := octetio.NewBitReaderAt(data, headerBits)

	// Group loop: refs/widths/lengths are each read from their own block,
	// values sequentially.
	coded := make([]int64, 0, numValues)
	for i := 0; i < ng; i++ {
		refI, err := brRef.ReadU(int(p.BitsPerVal))
		if err != nil {
			return nil, fmt.Errorf("pack: template %d: group %d reference: %w", tmpl, i, err)
		}
		scaledLen, err := brL.ReadU(int(p.BitsScaledLens))
		if err != nil {
			return nil, fmt.Errorf("pack: template %d: group %d length: %w", tmpl, i, err)
		}
		widthI, err := brW.ReadU(int(p.BitsGroupWidths))
		if err != nil {
			return nil, fmt.Errorf("pack: template %d: group %d width: %w", tmpl, i, err)
		}
		lenI := int(scaledLen)*int(p.LenIncrement) + int(p.RefGroupLengths)
		if i == ng-1 {
			lenI = int(p.TrueLastLen)
		}
		wi := int(widthI) + int(p.RefGroupWidths)
		for j := 0; j < lenI; j++ {
			d, err := brV.ReadU(wi)
			if err != nil {
				return nil, fmt.Errorf("pack: template %d: group %d value %d: bitstream exhausted: %w", tmpl, i, j, err)
			}
			coded = append(coded, int64(refI)+int64(d))
		}
	}
	if len(coded) != numValues {
		return nil, fmt.Errorf("pack: template %d: group lengths sum to %d values but numberOfValues = %d", tmpl, len(coded), numValues)
	}

	// Spatial-differencing reconstruction (eccodes post_process).
	raw := make([]int64, len(coded))
	copy(raw, coded)
	// Seed guard (bug.md §1.1): order 2 writes raw[0] and raw[1], order 1
	// writes raw[0]. An all-zero value field (numberOfValues == 0 — reachable
	// end to end via a section 6 bitmap with popcount 0) has nothing to seed,
	// so return the empty result the caller asked for rather than indexing
	// out of bounds. A single value also cannot carry an order-2 seed pair;
	// such a declaration is self-contradictory, so reject it instead of
	// guessing.
	if len(raw) == 0 {
		return []float32{}, nil
	}
	if order == 2 && len(raw) < 2 {
		return nil, fmt.Errorf("pack: template %d: order-2 spatial differencing needs at least 2 values, got %d", tmpl, len(raw))
	}
	switch order {
	case 2:
		penultimate, last := extras[0], extras[1]
		raw[0], raw[1] = penultimate, last
		for j := 2; j < len(raw); j++ {
			raw[j] = raw[j] + bias + last + last - penultimate
			penultimate, last = last, raw[j]
		}
	case 1:
		last := extras[0]
		raw[0] = last
		for j := 1; j < len(raw); j++ {
			raw[j] = raw[j] + bias + last
			last = raw[j]
		}
	}

	twoB := math.Pow(2, float64(p.B))
	tenMinusD := math.Pow(10, -float64(p.D))
	out := make([]float32, numValues)
	for i, r := range raw {
		out[i] = float32((float64(r)*twoB + float64(p.R)) * tenMinusD)
	}
	return out, nil
}

// complexUnpacker decodes template 5.3 from the section 5 Walk (field order
// per the spec table "5.3": 0=R F32, 1=B U16, 2=D U16, 3=bpv U8, 4=origType
// U8, 5=groupSplit U8, 6=missingMgmt U8, 7/8=missing substitutes U32, 9=NG
// U32, 10=refW U8, 11=widthBits U8, 12=refL U32, 13=lenInc U8, 14=trueLast
// U32, 15=scaledLenBits U8, 16=osd U8, 17=noed U8) — never from hardcoded
// assumptions.
type complexUnpacker struct{}

func (complexUnpacker) Name() string { return "complex packing + spatial differencing (5.3)" }

func (complexUnpacker) Unpack(ctx *Context) ([]float32, error) {
	if ctx.Fields == nil || ctx.Fields.Len() < 18 {
		return nil, fmt.Errorf("pack: template 5.3: section 5 fields missing or truncated (walk len %d, want 18)", walkLen(ctx.Fields))
	}
	f := ctx.Fields
	p := ComplexPacking{
		R:                f.At(0).F,
		B:                int16(smInt16(uint16(f.At(1).U))),
		D:                int16(smInt16(uint16(f.At(2).U))),
		BitsPerVal:       uint8(f.At(3).U),
		OrigType:         uint8(f.At(4).U),
		GroupSplit:       uint8(f.At(5).U),
		MissingMgmt:      uint8(f.At(6).U),
		PrimaryMiss:      uint32(f.At(7).U),
		SecondaryMiss:    uint32(f.At(8).U),
		NumGroups:        uint32(f.At(9).U),
		RefGroupWidths:   uint8(f.At(10).U),
		BitsGroupWidths:  uint8(f.At(11).U),
		RefGroupLengths:  uint32(f.At(12).U),
		LenIncrement:     uint8(f.At(13).U),
		TrueLastLen:      uint32(f.At(14).U),
		BitsScaledLens:   uint8(f.At(15).U),
		OrderSpatialDiff: uint8(f.At(16).U),
		OctetsExtraDesc:  uint8(f.At(17).U),
	}
	return complexDecode(TemplateComplex, p, ctx.Data, ctx.NumValues)
}

// complexPlainUnpacker decodes template 5.2 from the section 5 Walk (field
// order per the spec table "5.2": the first 16 fields of "5.3" minus the
// osd/noed pair — 0=R F32, 1=B U16, 2=D U16, 3=bpv U8, 4=origType U8,
// 5=groupSplit U8, 6=missingMgmt U8, 7/8=missing substitutes U32, 9=NG U32,
// 10=refW U8, 11=widthBits U8, 12=refL U32, 13=lenInc U8, 14=trueLast U32,
// 15=scaledLenBits U8).
// The two spatial-differencing fields do not exist in template 5.2 (section
// 5 totals 47 bytes including the header); as in eccodes
// templates/template.7.2.def, they are pinned to 0.
type complexPlainUnpacker struct{}

func (complexPlainUnpacker) Name() string { return "complex packing (5.2)" }

func (complexPlainUnpacker) Unpack(ctx *Context) ([]float32, error) {
	if ctx.Fields == nil || ctx.Fields.Len() < 16 {
		return nil, fmt.Errorf("pack: template 5.2: section 5 fields missing or truncated (walk len %d, want 16)", walkLen(ctx.Fields))
	}
	f := ctx.Fields
	p := ComplexPacking{
		R:               f.At(0).F,
		B:               int16(smInt16(uint16(f.At(1).U))),
		D:               int16(smInt16(uint16(f.At(2).U))),
		BitsPerVal:      uint8(f.At(3).U),
		OrigType:        uint8(f.At(4).U),
		GroupSplit:      uint8(f.At(5).U),
		MissingMgmt:     uint8(f.At(6).U),
		PrimaryMiss:     uint32(f.At(7).U),
		SecondaryMiss:   uint32(f.At(8).U),
		NumGroups:       uint32(f.At(9).U),
		RefGroupWidths:  uint8(f.At(10).U),
		BitsGroupWidths: uint8(f.At(11).U),
		RefGroupLengths: uint32(f.At(12).U),
		LenIncrement:    uint8(f.At(13).U),
		TrueLastLen:     uint32(f.At(14).U),
		BitsScaledLens:  uint8(f.At(15).U),
		// OrderSpatialDiff / OctetsExtraDesc stay 0: template 5.2 has no such
		// fields (see the type comment) — and eccodes injects the same constants.
	}
	return complexDecode(TemplateComplexPlain, p, ctx.Data, ctx.NumValues)
}
