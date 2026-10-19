//
// : template 5.3 decode tests. Golden values are
// dual-source calibrated: the fixture bytes and expected values were
// verified against eccodes 2.41 on the Linux bench host (grib_get_data), matching 's
// independent oracle scripts/xcheck/complex53-oracle.py and
// 's full-array reimplementation (0/1,193,781 mismatches on S1).

package pack

import (
	"encoding/hex"
	"math"
	"strings"
	"testing"
)

// fixture53 is testdata/gribw-xcheck/complex_53_spd.grib2's section 5 body
// and section 7 payload (9 values, R=100, B=+1, D=0, bpv=4, NG=3, widths 2,
// lengths 3/3/3(tllg), osd=2, noed=1). eccodes ground truth:
// [100, 102, 104, 108, 110, 112, 116, 118, 120].
var (
	fixture53Sec5, _ = hex.DecodeString("00000009000342c8000000010000040001000000000000000000000000030002000000000100000003020201")
	fixture53Sec7, _ = hex.DecodeString("0001810000a8fc061840")
)

func mustComplexParams(t *testing.T) ComplexPacking {
	t.Helper()
	// Mirror complexUnpacker's field extraction (spec table "5.3" order).
	p := ComplexPacking{
		R:                100.0,
		B:                1,
		D:                0,
		BitsPerVal:       4,
		OrigType:         0,
		GroupSplit:       1,
		MissingMgmt:      0,
		NumGroups:        3,
		RefGroupWidths:   0,
		BitsGroupWidths:  2,
		RefGroupLengths:  0,
		LenIncrement:     1,
		TrueLastLen:      3,
		BitsScaledLens:   2,
		OrderSpatialDiff: 2,
		OctetsExtraDesc:  1,
	}
	return p
}

func TestComplexDecodeFixture53(t *testing.T) {
	got, err := complexDecode(TemplateComplex, mustComplexParams(t), fixture53Sec7, 9)
	if err != nil {
		t.Fatalf("complexDecode: %v", err)
	}
	want := []float32{100, 102, 104, 108, 110, 112, 116, 118, 120}
	if len(got) != len(want) {
		t.Fatalf("len %d != %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("value %d = %v, want %v (eccodes ground truth)", i, got[i], want[i])
		}
	}
}

func TestComplexDecodeFirstOrder(t *testing.T) {
	// Hand-built order-1 case: extras=[5], bias=-1, one group of 4 values
	// (bpv=3, width 0 bits -> all deltas 0), R=0, B=0, D=0.
	// coded = [0,0,0,0]; reconstruction: v0=5, vj = 0 -1 + v(j-1)
	// -> [5, 4, 3, 2].
	p := ComplexPacking{
		R: 0, B: 0, D: 0, BitsPerVal: 3, OrigType: 0, GroupSplit: 1,
		MissingMgmt: 0, NumGroups: 1, RefGroupWidths: 0, BitsGroupWidths: 2,
		RefGroupLengths: 4, LenIncrement: 1, TrueLastLen: 4, BitsScaledLens: 2,
		OrderSpatialDiff: 1, OctetsExtraDesc: 1,
	}
	// sec7: [extra=5 (8b)][bias sign=1,mag=1 (8b) = -1]
	// refs: NG*bpv=3 bits from bit 16; widths: NG*2=2 bits; lengths: NG*2=2 bits.
	// header=16 bits, ref block ends at bit 19 -> width byte = ceil(19/8)=3,
	// length byte = 3+ceil(2/8)=4, values byte = 4+ceil(2/8)=5.
	data := []byte{0x05, 0x81, 0x00, 0x00, 0x04, 0x00} // len=6 bytes; last group len=tllg=4
	got, err := complexDecode(TemplateComplex, p, data, 4)
	if err != nil {
		t.Fatalf("complexDecode: %v", err)
	}
	want := []float32{5, 4, 3, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("value %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestComplexDecodeFailLoud(t *testing.T) {
	base := mustComplexParams(t)
	cases := []struct {
		name string
		mut  func(*ComplexPacking)
		nv   int
	}{
		{"NG zero (constant field uncalibrated)", func(p *ComplexPacking) { p.NumGroups = 0 }, 9},
		{"order 0", func(p *ComplexPacking) { p.OrderSpatialDiff = 0 }, 9},
		{"order 3", func(p *ComplexPacking) { p.OrderSpatialDiff = 3 }, 9},
		{"missing mgmt 1", func(p *ComplexPacking) { p.MissingMgmt = 1 }, 9},
		{"origType 2", func(p *ComplexPacking) { p.OrigType = 2 }, 9},
		{"group sum != NV", nil, 8}, // fixture sums to 9
	}
	for _, c := range cases {
		p := base
		if c.mut != nil {
			c.mut(&p)
		}
		if _, err := complexDecode(TemplateComplex, p, fixture53Sec7, c.nv); err == nil {
			t.Errorf("%s: expected error, got none", c.name)
		} else if c.name == "group sum != NV" && !strings.Contains(err.Error(), "sum to 9") {
			t.Logf("note: group-sum error text = %v", err)
		}
	}

	// Bitstream exhaustion: truncate section 7 mid-values.
	if _, err := complexDecode(TemplateComplex, base, fixture53Sec7[:4], 9); err == nil {
		t.Error("truncated data: expected bitstream-exhaustion error")
	}
}

// TestComplexDecodeS1Head pins the S1 calibration constants (HRRR f00 PRES
// L25, internal post): extras/bias values and the first decoded value, so a
// layout regression is caught even without the full 989KB bitstream.
func TestComplexDecodeS1Head(t *testing.T) {
	// S1 sec7 head: extras=[35601, 35602] (2×24b), bias=-15575 (24b smag:
	// sign=1, mag=0x3CD7). R=2937404.0 F32, B=2, D=2.
	head := []byte{0x00, 0x8b, 0x11, 0x00, 0x8b, 0x12, 0x80, 0x3c, 0xd7}
	// Reconstruct just the header to check parsing semantics.
	p := ComplexPacking{
		R: 2937404.0, B: 2, D: 2, BitsPerVal: 15, OrigType: 0, GroupSplit: 1,
		MissingMgmt: 0, NumGroups: 1, RefGroupWidths: 0, BitsGroupWidths: 4,
		RefGroupLengths: 1, LenIncrement: 1, TrueLastLen: 2, BitsScaledLens: 6,
		OrderSpatialDiff: 2, OctetsExtraDesc: 3,
	}
	// Minimal continuation so the group loop consumes exactly 2 values:
	// refs: 1×15 bits from bit 72; widths: 4 bits; lengths: 6 bits.
	// header=(1+2)*24=72 bits -> width byte=ceil((15+72)/8)=11, length byte=
	// 11+ceil(4/8)=12, values byte=12+ceil(6/8)=13.
	tail := []byte{0x00, 0x00, // refs: group ref = 0 (15 bits from bit 72)
		0x00,       // widths: 4 bits = 0 -> width 0
		0x00, 0x00, // lengths: 6 bits = 0 -> len = 0*1+1 = 1 (not last? it IS last -> tllg=2)
		0x00, // values: 2×0-bit reads consume nothing
	}
	data := append(append([]byte{}, head...), tail...)
	got, err := complexDecode(TemplateComplex, p, data, 2)
	if err != nil {
		t.Fatalf("complexDecode: %v", err)
	}
	// post_process overwrites both values with extras (last group len 2):
	want0 := float32((float64(35601)*4 + 2937404.0) / 100) // = 30798.08
	want1 := float32((float64(35602)*4 + 2937404.0) / 100) // = 30798.12
	if math.Abs(float64(got[0]-want0)) > 1e-4 {
		t.Errorf("value 0 = %v, want %v (S1 first value)", got[0], want0)
	}
	if math.Abs(float64(got[1]-want1)) > 1e-4 {
		t.Errorf("value 1 = %v, want %v (S1 second value)", got[1], want1)
	}
}

// : template 5.2 (plain complex packing, no spatial
// differencing). fixture52 is testdata/gribw-xcheck/complex_52_plain.grib2's
// section 7 payload (9 values, R=100, B=+1, D=0, bpv=4, NG=3, widths 2/2/2,
// lengths 3/3/3(tllg), osd=0/noed=0 — no header). eccodes 2.41 ground truth
// (the Linux bench host grib_get_data, verified at start): [100, 102, …, 120].
var fixture52Sec7, _ = hex.DecodeString("0480a8fc186180")

func TestComplexDecodeFixture52(t *testing.T) {
	p := ComplexPacking{
		R:               100.0,
		B:               1,
		D:               0,
		BitsPerVal:      4,
		OrigType:        1,
		GroupSplit:      0,
		MissingMgmt:     0,
		NumGroups:       3,
		RefGroupWidths:  0,
		BitsGroupWidths: 2,
		RefGroupLengths: 0,
		LenIncrement:    1,
		TrueLastLen:     3,
		BitsScaledLens:  2,
		// OrderSpatialDiff / OctetsExtraDesc = 0: template 5.2 has no such fields.
	}
	got, err := complexDecode(TemplateComplexPlain, p, fixture52Sec7, 9)
	if err != nil {
		t.Fatalf("complexDecode: %v", err)
	}
	want := []float32{100, 102, 104, 108, 110, 112, 116, 118, 120}
	if len(got) != len(want) {
		t.Fatalf("len %d != %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("value %d = %v, want %v (eccodes ground truth)", i, got[i], want[i])
		}
	}
}

// Template 5.2 fail-loud matrix: same red lines as 5.3 (NG=0, mvm≠0,
// origType∉{0,1}, group-sum mismatch, truncation). osd/noed are not fields of
// 5.2 — they cannot be malformed from the Walk, so no osd cases here.
func TestComplexDecodeFailLoud52(t *testing.T) {
	base := ComplexPacking{
		R: 100.0, B: 1, D: 0, BitsPerVal: 4, OrigType: 1, MissingMgmt: 0,
		NumGroups: 3, RefGroupWidths: 0, BitsGroupWidths: 2,
		RefGroupLengths: 0, LenIncrement: 1, TrueLastLen: 3, BitsScaledLens: 2,
	}
	cases := []struct {
		name string
		mut  func(*ComplexPacking)
		nv   int
	}{
		{"NG=0 constant field", func(p *ComplexPacking) { p.NumGroups = 0 }, 9},
		{"mvm=1", func(p *ComplexPacking) { p.MissingMgmt = 1 }, 9},
		{"origType=2", func(p *ComplexPacking) { p.OrigType = 2 }, 9},
		{"group sum != NV", func(p *ComplexPacking) {}, 8},
	}
	for _, c := range cases {
		p := base
		c.mut(&p)
		if _, err := complexDecode(TemplateComplexPlain, p, fixture52Sec7, c.nv); err == nil {
			t.Errorf("%s: expected error, got none", c.name)
		} else if c.name == "group sum != NV" && !strings.Contains(err.Error(), "sum to 9") {
			t.Logf("note: group-sum error text = %v", err)
		}
	}
	if _, err := complexDecode(TemplateComplexPlain, base, fixture52Sec7[:3], 9); err == nil {
		t.Error("truncated data: expected bitstream-exhaustion error")
	}
}
