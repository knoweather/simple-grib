// Owner: bugfix pass (bug.md pack items 1-9)
//
// Regression tests for the pack-package fail-loud fixes. Each test is
// written to fail (or panic) against the pre-fix code and pass afterwards;
// none depends on the network or on fixtures that are not in the repo.

package pack

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// --- bug.md §1.1: complex packing must not index an empty result -----------

// zeroValueComplexParams builds a template 5.3 header whose single group
// carries zero values (BitsPerVal/width/length all 0, true last length 0), so
// the group loop yields an empty coded slice while numValues == 0.
func zeroValueComplexParams(order uint8, trueLastLen uint32) ComplexPacking {
	return ComplexPacking{
		R: 0, B: 0, D: 0, BitsPerVal: 0, OrigType: 0, GroupSplit: 0,
		MissingMgmt: 0, NumGroups: 1, RefGroupWidths: 0, BitsGroupWidths: 0,
		RefGroupLengths: 0, LenIncrement: 1, TrueLastLen: trueLastLen, BitsScaledLens: 0,
		OrderSpatialDiff: order, OctetsExtraDesc: 1,
	}
}

func TestComplexDecodeZeroValuesNoPanic(t *testing.T) {
	for _, order := range []uint8{1, 2} {
		p := zeroValueComplexParams(order, 0)
		// Header is (1+order) 8-bit descriptors; 3 bytes covers both orders.
		got, err := complexDecode(TemplateComplex, p, make([]byte, 3), 0)
		if err != nil {
			t.Fatalf("order %d: complexDecode: %v", order, err)
		}
		if len(got) != 0 {
			t.Fatalf("order %d: got %d values, want 0", order, len(got))
		}
	}
}

// An order-2 field with a single value cannot carry its two seeds; that is an
// inconsistent declaration and must fail loud, not panic on raw[1].
func TestComplexDecodeOrder2SingleValueFailsLoud(t *testing.T) {
	p := zeroValueComplexParams(2, 1)
	if _, err := complexDecode(TemplateComplex, p, make([]byte, 3), 1); err == nil {
		t.Fatal("order-2 with one value: expected an error, got none")
	}
}

// --- bug.md §2.5: simple packing must reject bitsPerValue > 32 always ------

func TestSimpleDecodeRejectsBitsAbove32(t *testing.T) {
	for _, n := range []int{0, 1} {
		var data []byte
		if n > 0 {
			data = make([]byte, (33*n+7)/8)
		}
		if _, err := simpleDecode(SimplePacking{R: 0, E: 0, D: 0, Bits: 33, OrigType: 0}, data, n); err == nil {
			t.Fatalf("bits=33 numValues=%d: expected an error, got none", n)
		}
	}
	// bits == 0 (constant field = R) must keep working.
	got, err := simpleDecode(SimplePacking{R: 2.5, E: 0, D: 0, Bits: 0, OrigType: 0}, nil, 3)
	if err != nil {
		t.Fatalf("bits=0: %v", err)
	}
	if len(got) != 3 || got[0] != 2.5 || got[2] != 2.5 {
		t.Fatalf("bits=0: got %v, want three 2.5 values", got)
	}
}

// --- bug.md §1.5: JPEG 2000 SIZ tile/offset geometry must be validated -----

func TestJPCSIZValidation(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("..", "testdata", "p7-3-j2k", "f07.j2k"))
	if err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	if _, _, _, _, err := jpcDecodeSamples(base); err != nil {
		t.Fatalf("baseline fixture does not decode: %v", err)
	}
	// SIZ payload starts at offset 6: Xsiz@8 Ysiz@12 XOsiz@16 YOsiz@20
	// XTsiz@24 YTsiz@28 XTOsiz@32 YTOsiz@36 (f07: 50x30, all origins 0).
	cases := []struct {
		name string
		off  int
		val  uint32
	}{
		{"XTsiz=0", 24, 0},
		{"YTsiz=0", 28, 0},
		{"XOsiz > Xsiz", 16, 1 << 20},
		{"YOsiz > Ysiz", 20, 1 << 20},
		{"XTOsiz > Xsiz", 32, 1 << 20},
		{"YTOsiz > Ysiz", 36, 1 << 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := append([]byte(nil), base...)
			binary.BigEndian.PutUint32(out[c.off:c.off+4], c.val)
			_, _, _, _, err := jpcDecodeSamples(out)
			var mal *ErrJPCMalformed
			if err == nil || !errors.As(err, &mal) {
				t.Fatalf("err = %v, want ErrJPCMalformed", err)
			}
		})
	}
}

// --- bug.md §1.6: bitsPerValue == 0 must respect the sample-count bound ----

func TestJPCBitsZeroNumValuesGuard(t *testing.T) {
	sec5 := sec5_40(0, 1.0, 0, 0, 0, 0, 0)
	ctx := &Context{
		TemplateNumber: TemplateJPEG2000,
		Fields:         walk540(t, sec5),
		Data:           nil,
		NumValues:      (1 << 24) + 1,
	}
	_, err := Unpack(ctx)
	var mal *ErrJPCMalformed
	if err == nil || !errors.As(err, &mal) {
		t.Fatalf("err = %v, want ErrJPCMalformed for an over-limit NumValues", err)
	}
}

// --- bug.md §1.7: synthesize's join scratch must fit the column pass ------

func TestJPCSynthesizeColumnTmpBound(t *testing.T) {
	// w=1, h=100: the column pass joins r.h=100 values, so jpcJoin1D stashes
	// (100+1)/2 = 50 entries; a tmp sized to t.w (=1) overflows.
	img := &jpcImage{cs: jpcCodingStyle{numdlvls: 1}}
	td := &jpcTileDecoder{img: img, w: 1, h: 100, data: make([]int32, 100)}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("synthesize panicked (join scratch undersized): %v", r)
		}
	}()
	td.synthesize()
}

// --- bug.md §2.9: the JPC bit reader must report EOF as an error -----------

func TestJPCBitReaderEOFSignalsError(t *testing.T) {
	b := newJPCBitReader([]byte{0x00}, 0)
	for i := 0; i < 8; i++ {
		if v := b.getbit(); v != 0 {
			t.Fatalf("bit %d = %d, want 0", i, v)
		}
	}
	if v := b.getbit(); v != -1 {
		t.Fatalf("getbit past end = %d, want -1", v)
	}
	if v := b.getbits(3); v != -1 {
		t.Fatalf("getbits(3) past end = %d, want -1", v)
	}
	// A tag-tree decode on an exhausted reader must fail loud instead of
	// turning the (previously fabricated) padding bit into a value.
	tt := newTagTree(1, 1)
	if _, err := tt.decode(0, 5, newJPCBitReader(nil, 0)); err == nil {
		t.Fatal("tag tree decode past end: want an error")
	}
}

// --- bug.md §2.10: CCSDS zero budget with data is corruption ---------------

func TestCcsdsZeroBudgetCorruption(t *testing.T) {
	params := ccsdsParams{Flags: 14, BitsPerVal: 16, BlockSize: 32, RSI: 128}
	if _, err := ccsdsDecode(params, []byte{1, 2, 3}, 0); err == nil {
		t.Error("wantSamples=0 with a data section: want an error")
	}
	got, err := ccsdsDecode(params, nil, 0)
	if err != nil {
		t.Fatalf("wantSamples=0 with no data: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("wantSamples=0: got %d values, want 0", len(got))
	}
	if _, err := ccsdsDecode(params, nil, -1); err == nil {
		t.Error("negative wantSamples: want an error")
	}
}

// --- bug.md §2.11: CCSDS B/D must not poison values with Inf ---------------

func TestCcsdsScaleFactorRange(t *testing.T) {
	for _, c := range []struct {
		name string
		B, D int16
	}{
		{"B=+32767", 32767, 0},
		{"B=-32767", -32767, 0},
		{"D=+32767", 0, 32767},
		{"D=-32767", 0, -32767},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := ccsdsScaleFactors(c.B, c.D)
			var e *ErrUnsupportedValue
			if err == nil || !errors.As(err, &e) {
				t.Fatalf("err = %v, want *ErrUnsupportedValue", err)
			}
		})
	}
	// The boundary values must stay finite (2^1023, 10^308).
	twoB, tenMinusD, err := ccsdsScaleFactors(ccsdsMaxBinaryExp, ccsdsMaxDecimalExp)
	if err != nil {
		t.Fatalf("boundary: %v", err)
	}
	if math.IsInf(twoB, 0) || math.IsInf(tenMinusD, 0) {
		t.Fatalf("boundary produced Inf: %v %v", twoB, tenMinusD)
	}
	if _, _, err := ccsdsScaleFactors(-ccsdsMaxBinaryExp, -ccsdsMaxDecimalExp); err != nil {
		t.Fatalf("negative boundary: %v", err)
	}
}

// --- bug.md §5.1: the ROS zero-block run must fill the interval ------------

func TestCcsdsZeroBlockROSBound(t *testing.T) {
	// Fundamental sequence 0000 1 -> fs = 4 -> zero_blocks = fs+1 = 5 == ROS.
	// The reference (libaec decode.c m_zero_block) then sets
	// zero_blocks = MIN(rsi - b, 64 - (b%64)); with one block left in the
	// interval it must fill exactly to the end. rsiPos = 127*32 + 1 models
	// the reference sample of the interval's first block already consumed.
	d := &ccsdsDecoder{
		blockSize: 32,
		params:    ccsdsParams{RSI: 128, BlockSize: 32, BitsPerVal: 16},
		pp:        true,
		ref:       1,
		xmax:      0xFFFF,
		in:        []byte{0x08},
	}
	d.rsiBuf = make([]uint32, 128*32)
	d.rsiPos = 127*32 + 1

	if got := d.mZeroBlock(); got != mContinue {
		t.Fatalf("mZeroBlock = %d, want mContinue", got)
	}
	// Filling the interval makes mNextCDS flush it (and reset rsiPos), so the
	// observable effect of a correct run is exactly RSI*blockSize samples.
	if len(d.out) != len(d.rsiBuf) {
		t.Fatalf("flushed %d samples, want %d (ROS run must fill the reference-sample interval)", len(d.out), len(d.rsiBuf))
	}
}
