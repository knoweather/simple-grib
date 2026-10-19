package pack

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func f32bits(f float32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, math.Float32bits(f))
	return b
}

func be32(v uint32) []byte { // be32 encodes v as a 4-byte big-endian slice
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

// assertF32s compares two float32 slices element-wise (exact, NaN-aware).
func assertF32s(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if math.IsNaN(float64(want[i])) {
			if !math.IsNaN(float64(got[i])) {
				t.Errorf("[%d] = %v, want NaN", i, got[i])
			}
			continue
		}
		if got[i] != want[i] {
			t.Errorf("[%d] = %v (bits %08X), want %v (bits %08X)", i, got[i], math.Float32bits(got[i]), want[i], math.Float32bits(want[i]))
		}
	}
}

// eccodes-verified formula: value = (raw × 2^E + R) × 10^(−D), R pre-scaled.

func TestSimpleDecodeBasic(t *testing.T) {
	// R=0, E=0, D=2: value = raw/100. Hand example: 100,200,300 → 1,2,3.
	data := append(append(append([]byte{}, be32(100)...), be32(200)...), be32(300)...)
	got, err := simpleDecode(SimplePacking{R: 0, E: 0, D: 2, Bits: 32, OrigType: 1}, data, 3)
	if err != nil {
		t.Fatal(err)
	}
	assertF32s(t, got, []float32{1.0, 2.0, 3.0})
}

func TestSimpleDecodeReferenceOffset(t *testing.T) {
	// D=0: value = raw + R. R=273.5 exact in binary.
	data := append(append(append([]byte{}, be32(0)...), be32(123)...), be32(4096)...)
	got, err := simpleDecode(SimplePacking{R: 273.5, E: 0, D: 0, Bits: 32, OrigType: 0}, data, 3)
	if err != nil {
		t.Fatal(err)
	}
	assertF32s(t, got, []float32{273.5, 396.5, 4369.5})
}

func TestSimpleDecodeDecimalScale(t *testing.T) {
	// D=3: value = raw/1000. R=0 → 12.345 (correctly-rounded literal).
	data := be32(12345)
	got, err := simpleDecode(SimplePacking{R: 0, E: 0, D: 3, Bits: 32, OrigType: 1}, data, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertF32s(t, got, []float32{float32(12.345)})
}

func TestSimpleDecodeNegativeDecimalScale(t *testing.T) {
	// D=-1: value = raw×10.
	data := append(be32(7), be32(123)...)
	got, err := simpleDecode(SimplePacking{R: 0, E: 0, D: -1, Bits: 32, OrigType: 1}, data, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertF32s(t, got, []float32{70, 1230})
}

func TestSimpleDecodePreScaledReference(t *testing.T) {
	// R is stored pre-scaled by 10^D: reference 1.5 with D=4 → stored R=15000.
	// raw=1 → (1 + 15000) × 10^-4 = 1.5001.
	data := be32(1)
	got, err := simpleDecode(SimplePacking{R: 15000, E: 0, D: 4, Bits: 32, OrigType: 0}, data, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertF32s(t, got, []float32{float32(1.5001)})
}

func TestSimpleDecodeBinaryScaleFactor(t *testing.T) {
	// E=1: value = raw×2. R=0, D=0.
	data := be32(5)
	got, err := simpleDecode(SimplePacking{R: 0, E: 1, D: 0, Bits: 32, OrigType: 1}, data, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertF32s(t, got, []float32{10})

	// negative E: raw=8, E=-2 → 8/4 = 2.
	data = be32(8)
	got, err = simpleDecode(SimplePacking{R: 0, E: -2, D: 0, Bits: 32, OrigType: 1}, data, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertF32s(t, got, []float32{2})
}

func TestSimpleDecodeCombinedScales(t *testing.T) {
	// Real-data shape (temperature-like): D=2, R=27315 (→ 273.15), E=1, raw=123
	// → (123×2 + 27315)/100 = 27561/100 = 275.61.
	data := be32(123)
	got, err := simpleDecode(SimplePacking{R: 27315, E: 1, D: 2, Bits: 32, OrigType: 0}, data, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertF32s(t, got, []float32{float32(275.61)})
}

func TestSimpleDecodeAllOnesIsValue(t *testing.T) {
	// eccodes applies no all-ones NaN sentinel in simple packing ():
	// raw=0xFFFFFFFF decodes as an ordinary value → 2^32−1 ≈ 2^32 in float32.
	data := be32(math.MaxUint32)
	got, err := simpleDecode(SimplePacking{R: 0, E: 0, D: 0, Bits: 32, OrigType: 0}, data, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := float32(math.MaxUint32) // integer 2^32-1 -> nearest float32 is exactly 2^32
	assertF32s(t, got, []float32{want})
}

func TestSimpleDecodeEmpty(t *testing.T) {
	got, err := simpleDecode(SimplePacking{R: 0, E: 0, D: 0, Bits: 32, OrigType: 1}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestSimpleDecodeErrors(t *testing.T) {
	data := append(be32(1), be32(2)...)

	if _, err := simpleDecode(SimplePacking{R: 0, E: 0, D: 0, Bits: 32, OrigType: 1}, data, 3); err == nil {
		t.Error("data shorter than numValues×4: expected error")
	}
	if _, err := simpleDecode(SimplePacking{R: 0, E: 0, D: 0, Bits: 32, OrigType: 1}, data, 1); err == nil {
		t.Error("data longer than numValues×4: expected error")
	}

	var e *ErrUnsupportedValue
	if _, err := simpleDecode(SimplePacking{R: 0, E: 0, D: 0, Bits: 32, OrigType: 255}, data, 2); !errors.As(err, &e) {
		t.Errorf("origType=255: expected *ErrUnsupportedValue, got %v", err)
	} else if e.Template != TemplateSimple || e.Value != 255 {
		t.Errorf("ErrUnsupportedValue = %+v", e)
	}
}

func TestIEEEDecodeRoundTrip(t *testing.T) {
	vals := []float32{1.5, -0.5, 0, float32(math.Inf(1))}
	var data []byte
	for _, v := range vals {
		data = append(data, f32bits(v)...)
	}
	got, err := ieeeDecode(IEEE{Precision: 1}, data, len(vals))
	if err != nil {
		t.Fatal(err)
	}
	assertF32s(t, got, vals)
}

func TestIEEEDecodeNaN(t *testing.T) {
	data := f32bits(float32(math.NaN()))
	got, err := ieeeDecode(IEEE{Precision: 1}, data, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(float64(got[0])) {
		t.Errorf("got %v, want NaN", got[0])
	}
}

func TestIEEEDecodeErrors(t *testing.T) {
	data := f32bits(1.5)
	var e *ErrUnsupportedValue
	for _, prec := range []uint8{0, 2, 3, 255} {
		if _, err := ieeeDecode(IEEE{Precision: prec}, data, 1); !errors.As(err, &e) {
			t.Errorf("precision=%d: expected *ErrUnsupportedValue, got %v", prec, err)
		}
	}
	if _, err := ieeeDecode(IEEE{Precision: 1}, data[:3], 1); err == nil {
		t.Error("short data: expected error")
	}
}
