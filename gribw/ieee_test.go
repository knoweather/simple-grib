// Tests for the template 5.4 IEEE floating-point encoder
// (gribw.EncodeIEEE). Template 5.4 exists to carry arbitrary float32 bit
// patterns — NaN, ±Inf, denormals, and both zero signs — so these tests pin
// bit-exact payload bytes (golden) and bit-exact round-trips through the
// frame. The -0.0 case must be constructed with math.Float32frombits(0x80000000):
// a Go float32 literal -0.0 can lose its sign in constant folding.

package gribw

import (
	"bytes"
	"encoding/hex"
	"math"
	"testing"

	grib "github.com/knoweather/simple-grib"
)

// ieeeGolden is the value vector: every float32 bit-pattern class the
// reader's pack_test.go exercises, plus the signed-zero pair.
var ieeeGolden = []float32{
	0.0,                              // +0.0 → 0x00000000
	math.Float32frombits(0x80000000), // -0.0 (NOT the literal -0.0, see above)
	1.5,
	-0.5,
	float32(math.Inf(1)),             // +Inf → 0x7F800000
	float32(math.Inf(-1)),            // -Inf → 0xFF800000
	math.Float32frombits(0x7fc00000), // qNaN
	math.SmallestNonzeroFloat32,      // smallest denormal 2^-149 → 0x00000001 (Go's naming)
	math.MaxFloat32,                  // → 0x7F7FFFFF
}

// expectedIEEEPayload is the hand-derived golden: NV × 4 bytes big-endian
// IEEE 754 binary32, in vector order.
const expectedIEEEPayload = "00000000" + // +0.0
	"80000000" + // -0.0
	"3fc00000" + // 1.5
	"bf000000" + // -0.5
	"7f800000" + // +Inf
	"ff800000" + // -Inf
	"7fc00000" + // qNaN
	"00000001" + // smallest denormal (2^-149)
	"7f7fffff" // MaxFloat32

// expectedIEEESec5: 12-byte minimum template 5.4 header — len=12, section=5,
// NV=9, template number=4, precision=1 (binary32, §5.13 writer clause).
const expectedIEEESec5 = "0000000c" + // len 12
	"05" + // section 5
	"00000009" + // NV = 9
	"0004" + // template number 4
	"01" // precision 1

func TestEncodeIEEESec5AndPayloadGolden(t *testing.T) {
	sec5, payload, err := EncodeIEEE(ieeeGolden, IEEEParams{Precision: 1})
	if err != nil {
		t.Fatalf("EncodeIEEE: %v", err)
	}
	wantSec5, _ := hex.DecodeString(expectedIEEESec5)
	if !bytes.Equal(sec5, wantSec5) {
		t.Errorf("sec5 = %x, want %x", sec5, wantSec5)
	}
	wantPayload, _ := hex.DecodeString(expectedIEEEPayload)
	if !bytes.Equal(payload, wantPayload) {
		t.Errorf("payload = %x (%d bytes), want %x (%d bytes)", payload, len(payload), wantPayload, len(wantPayload))
	}
}

// TestEncodeIEEERoundTrip: the encoder's (sec5, payload) goes through the
// frame and back through the reader; every bit pattern must survive,
// including the -0.0 sign bit and the NaN payload bits.
func TestEncodeIEEERoundTrip(t *testing.T) {
	m := New()
	if err := m.SetIdentification(canonicalID()); err != nil {
		t.Fatal(err)
	}
	if err := m.SetGrid(fixtureGrid(t)); err != nil {
		t.Fatal(err)
	}
	if err := m.SetProduct(canonicalProduct()); err != nil {
		t.Fatal(err)
	}
	sec5, payload, err := EncodeIEEE(ieeeGolden, IEEEParams{Precision: 1})
	if err != nil {
		t.Fatalf("EncodeIEEE: %v", err)
	}
	if err := m.SetDataSection(sec5, payload); err != nil {
		t.Fatalf("SetDataSection: %v", err)
	}
	b, err := m.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := readBack(t, b).Values()
	if err != nil {
		t.Fatalf("reader Values: %v", err)
	}
	for i := range ieeeGolden {
		if math.Float32bits(got[i]) != math.Float32bits(ieeeGolden[i]) {
			t.Errorf("value[%d] bits = %08x, want %08x (%v)", i,
				math.Float32bits(got[i]), math.Float32bits(ieeeGolden[i]), ieeeGolden[i])
		}
	}
	// Explicit sign-bit check for the zero pair (the trap):
	if got[0] != 0 || math.Float32bits(got[0]) != 0x00000000 {
		t.Errorf("+0.0 round-trip = %v (bits %08x), want +0.0", got[0], math.Float32bits(got[0]))
	}
	if math.Float32bits(got[1]) != 0x80000000 {
		t.Errorf("-0.0 round-trip bits = %08x, want 80000000 (sign must survive)", math.Float32bits(got[1]))
	}
}

// TestEncodeIEEEFailLoud: §5.13 writer clause — precision=1 (binary32) only;
// every other table 5.7 value fails loud, symmetric with the reader's
// supported range.
func TestEncodeIEEEFailLoud(t *testing.T) {
	for _, prec := range []int{0, 2, 3, -1, 255} {
		t.Run("precision", func(t *testing.T) {
			_, _, err := EncodeIEEE(ieeeGolden, IEEEParams{Precision: prec})
			if err == nil {
				t.Fatalf("precision=%d: expected fail-loud error, got nil", prec)
			}
			if !bytes.Contains([]byte(err.Error()), []byte("not supported in ")) {
				t.Errorf("error %q does not cite the §5.13 writer clause", err.Error())
			}
		})
	}
}

// TestEncodeIEEEEmptyAndSingle: degenerate sizes — zero values (empty
// payload, NV=0) and one value (4-byte payload).
func TestEncodeIEEEEmptyAndSingle(t *testing.T) {
	sec5, payload, err := EncodeIEEE(nil, IEEEParams{Precision: 1})
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if len(payload) != 0 {
		t.Errorf("empty payload = %x, want empty", payload)
	}
	if got := sec5[5:9]; bytes.Compare(got, []byte{0, 0, 0, 0}) != 0 {
		t.Errorf("empty NV = %x, want 00000000", got)
	}

	one := []float32{-12.5}
	sec5, payload, err = EncodeIEEE(one, IEEEParams{Precision: 1})
	if err != nil {
		t.Fatalf("single: %v", err)
	}
	if !bytes.Equal(payload, []byte{0xc1, 0x48, 0x00, 0x00}) { // -12.5 = 0xC1480000
		t.Errorf("single payload = %x, want c1480000", payload)
	}
	if got := sec5[5:9]; bytes.Compare(got, []byte{0, 0, 0, 1}) != 0 {
		t.Errorf("single NV = %x, want 00000001", got)
	}
}

// TestEncodeIEEEFileIntegration: IEEE-encoded message alongside a
// simple-packed one in a two-message file — both round-trip bit-exact.
func TestEncodeIEEEFileIntegration(t *testing.T) {
	simpleVals := []float32{0, 1, 2, 3, 4, 5, 6, 7, 8}
	s5, pl, err := EncodeSimple(simpleVals, SimpleParams{R: 0, E: 0, D: 0, Bits: 16})
	if err != nil {
		t.Fatalf("EncodeSimple: %v", err)
	}
	i5, ip, err := EncodeIEEE(ieeeGolden, IEEEParams{Precision: 1})
	if err != nil {
		t.Fatalf("EncodeIEEE: %v", err)
	}

	mk := func(sec5, payload []byte) *Message {
		m := New()
		if err := m.SetIdentification(canonicalID()); err != nil {
			t.Fatal(err)
		}
		if err := m.SetGrid(fixtureGrid(t)); err != nil {
			t.Fatal(err)
		}
		if err := m.SetProduct(canonicalProduct()); err != nil {
			t.Fatal(err)
		}
		if err := m.SetDataSection(sec5, payload); err != nil {
			t.Fatal(err)
		}
		return m
	}
	b, err := File(mk(s5, pl), mk(i5, ip))
	if err != nil {
		t.Fatalf("File: %v", err)
	}

	f, err := grib.OpenReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()
	if n, _ := f.NumMessages(); n != 2 {
		t.Fatalf("NumMessages = %d, want 2", n)
	}
	m1, _ := f.Message(1)
	got, err := m1.Values()
	if err != nil {
		t.Fatal(err)
	}
	for i := range ieeeGolden {
		if math.Float32bits(got[i]) != math.Float32bits(ieeeGolden[i]) {
			t.Errorf("msg1 value[%d] bits = %08x, want %08x", i,
				math.Float32bits(got[i]), math.Float32bits(ieeeGolden[i]))
		}
	}
}
