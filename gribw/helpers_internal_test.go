// Unit tests for the pure helpers: the sign-magnitude accessors and the WMO
// scaling utilities. These sit at the bottom of the fail-loud,
// bit-exact-with-eccodes behaviour and were previously only covered
// indirectly through encode/decode integration tests; they are pinned here
// individually.

package gribw

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/knoweather/simple-grib/spec"
)

// TestSmu16 pins the sign-magnitude uint16 encoding (bit 15 = sign, low 15
// bits = magnitude): it is NOT two's complement. -1 must encode as 0x8001, not
// the 0xFFFF of two's complement.
func TestSmu16(t *testing.T) {
	cases := []struct {
		in   int32
		want uint16
	}{
		{0, 0x0000},
		{1, 0x0001},
		{32767, 0x7FFF},
		{-1, 0x8001}, // key: not 0xFFFF (two's complement)
		{-2, 0x8002}, // eccodes writes D=-2 as 0x8002
		{-32767, 0xFFFF},
	}
	for _, c := range cases {
		if got := smu16(c.in); got != c.want {
			t.Errorf("smu16(%d) = 0x%04X, want 0x%04X", c.in, got, c.want)
		}
	}
	// Reverse confirmation: a two's complement convention would give a
	// different result, this assertion fails if the encoding is ever silently
	// switched to two's complement.
	negOne := int16(-1)
	if smu16(-1) == uint16(negOne) {
		t.Errorf("smu16(-1) == two's-complement int16(-1): sign-magnitude contract broken")
	}
}

// TestEncodeAngle pins the sign-magnitude int32 angle encoding (bit 31 = sign,
// low 31 bits = magnitude). Values must be exact multiples of the angle unit or
// the call fails loud.
func TestEncodeAngle(t *testing.T) {
	const unit = 1e-6 // microdegrees, the GRIB2 default
	cases := []struct {
		name string
		deg  float64
		want uint32
	}{
		{"zero", 0, 0},
		{"positive", 90, 90000000},               // 90 degrees = 90000000 microdegrees
		{"negative", -90, 0x80000000 | 90000000}, // sign bit set, magnitude 90000000
		{"thirty", 30, 30000000},
	}
	for _, c := range cases {
		got, err := encodeAngle(c.deg, unit)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: encodeAngle(%v) = 0x%08X, want 0x%08X", c.name, c.deg, got, c.want)
		}
	}

	// Not an exact multiple of the unit → fail loud (no silent rounding).
	if _, err := encodeAngle(30.0000001, unit); err == nil {
		t.Error("encodeAngle with a non-multiple angle: want error, got nil")
	}
	// Beyond the sign-magnitude int31 range → fail loud.
	if _, err := encodeAngle(2148, unit); err == nil { // 2148 deg = 2148000000 units > MaxInt32
		t.Error("encodeAngle beyond int31 range: want error, got nil")
	}
	// Non-finite values → fail loud.
	if _, err := encodeAngle(math.NaN(), unit); err == nil {
		t.Error("encodeAngle(NaN): want error, got nil")
	}
	if _, err := encodeAngle(math.Inf(1), unit); err == nil {
		t.Error("encodeAngle(+Inf): want error, got nil")
	}
}

// TestEncodeScaled pins the encode-side counterpart of the WMO scaled-decimal
// rules: find the smallest scale s in [0,9] such that v×10^s is an exact
// non-negative integer fitting in U32. Negative and out-of-range values fail
// loud.
func TestEncodeScaled(t *testing.T) {
	cases := []struct {
		name    string
		v       float64
		wantS   int8
		wantR   uint32
		wantErr bool
	}{
		{"zero", 0, 0, 0, false},
		{"exact int", 123456789, 0, 123456789, false},
		{"needs scale 1", 1.5, 1, 15, false},         // 1.5×10 = 15
		{"needs scale 2", 1.55, 2, 155, false},       // 1.55×100 = 155
		{"two decimals", 0.05, 2, 5, false},          // 0.05×100 = 5
		{"negative", -1, 0, 0, true},                 // non-negativity constraint
		{"non-representable", 1.0 / 7.0, 0, 0, true}, // 1/7 leaves a remainder at every power of ten, beyond tolerance
		{"too big for U32", 1e12, 0, 0, true},        // exceeds U32 even at s=9
	}
	for _, c := range cases {
		s, r, err := encodeScaled(c.v)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: want error, got s=%d r=%d", c.name, s, r)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
			continue
		}
		if s != c.wantS || r != c.wantR {
			t.Errorf("%s: got (s=%d, r=%d), want (s=%d, r=%d)", c.name, s, r, c.wantS, c.wantR)
		}
	}
}

// TestAngleUnit pins the §5.3 defaults: a basic angle of 0 or missing reads as
// 1, and subdivisions 0 or missing read as 1e6. The default unit is therefore
// microdegrees (1/1e6).
func TestAngleUnit(t *testing.T) {
	cases := []struct {
		basicAngle, subdivisions int
		want                     float64
	}{
		{0, 0, 1e-6},    // both defaulted → microdegrees
		{-1, 0, 1e-6},   // basicAngle missing (-1) → 1
		{0, -1, 1e-6},   // subdivisions missing → 1e6
		{1, 1000, 1e-3}, // 1/1000 degree
		{1, 360, 1.0 / 360.0},
	}
	for _, c := range cases {
		if got := angleUnit(c.basicAngle, c.subdivisions); got != c.want {
			t.Errorf("angleUnit(%d,%d) = %g, want %g", c.basicAngle, c.subdivisions, got, c.want)
		}
	}
}

// TestWriteScaledField pins the byte layout of the WMO scale factor + scaled
// value pair: a -1 on either side writes the missing sentinel (255 / all-ones).
func TestWriteScaledField(t *testing.T) {
	def, ok := spec.Template(3, 0)
	if !ok {
		t.Fatal("no spec definition for template 3.0")
	}
	buf := make([]byte, 72)
	// Normal value: scale=3 lands at octet 16, value=6371000 at octets 17-20.
	if err := writeScaledField(def, buf, 16, 17, 3, 6371000); err != nil {
		t.Fatalf("writeScaledField normal: %v", err)
	}
	if buf[15] != 3 {
		t.Errorf("scale byte (octet 16) = %d, want 3", buf[15])
	}
	if v := binary.BigEndian.Uint32(buf[16:20]); v != 6371000 {
		t.Errorf("value (octets 17-20) = %d, want 6371000", v)
	}
	// Missing: scale=-1 → 255; value=-1 → all ones.
	if err := writeScaledField(def, buf, 16, 17, -1, -1); err != nil {
		t.Fatalf("writeScaledField missing: %v", err)
	}
	if buf[15] != 255 {
		t.Errorf("missing scale byte = %d, want 255", buf[15])
	}
	if v := binary.BigEndian.Uint32(buf[16:20]); v != 0xFFFFFFFF {
		t.Errorf("missing value = 0x%08X, want 0xFFFFFFFF", v)
	}
	// Scale factor out of range → fail loud.
	if err := writeScaledField(def, buf, 16, 17, 10, 0); err == nil {
		t.Error("scale factor 10: want error, got nil")
	}
}
