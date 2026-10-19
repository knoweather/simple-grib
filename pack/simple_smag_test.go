package pack

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/knoweather/simple-grib/spec"
)

// makeSec5Smag builds a synthetic section 5 (octet 1 based) with the E/D
// fields written as RAW GRIB2 sign-magnitude bytes (bit 15 = sign, low 15
// bits = magnitude) — the ecosystem convention verified against real ECMWF
// data and eccodes grib_set .
// Unlike makeSec5 (which takes int16 and two's-complements it), the caller
// supplies the exact on-wire bytes for E and D.
func makeSec5Smag(tmpl uint16, nv uint32, R float32, eRaw, dRaw uint16, bits, origType byte) []byte {
	b := make([]byte, 24)
	binary.BigEndian.PutUint32(b[0:4], uint32(len(b)))
	b[4] = 5
	binary.BigEndian.PutUint32(b[5:9], nv)                    // octets 6-9: numberOfValues
	binary.BigEndian.PutUint16(b[9:11], tmpl)                 // octets 10-11: template number
	binary.BigEndian.PutUint32(b[11:15], math.Float32bits(R)) // octets 12-15: R (pre-scaled by 10^D)
	binary.BigEndian.PutUint16(b[15:17], eRaw)                // octets 16-17: E raw smag bytes
	binary.BigEndian.PutUint16(b[17:19], dRaw)                // octets 18-19: D raw smag bytes
	b[19] = bits                                              // octet 20
	b[20] = origType                                          // octet 21
	return b
}

// TestUnpackSmagNegativeE is the regression test for the two's-complement E/D
// bug : real ECMWF file old_enfo_2023 stores E=-6 as 0x8006;
// the old int16 two's-complement read gave -32762 → 2^E underflow → every
// value collapsed to R. Golden vector (hand-verified, .b-tmp archive):
// NV=4, R=-1.0, E=-6 (0x8006), D=0, b=12, values {-1,-0.5,0,17.90625}
// → raws {0,32,64,1210} → payload 00 00 20 04 04 ba.
func TestUnpackSmagNegativeE(t *testing.T) {
	vals := []float32{-1.0, -0.5, 0.0, 17.90625} // all exactly representable in float32
	payload := []byte{0x00, 0x00, 0x20, 0x04, 0x04, 0xba}
	sec5 := makeSec5Smag(0, uint32(len(vals)), -1.0, 0x8006, 0x0000, 12, 0)
	def, ok := spec.Template(5, 0)
	if !ok {
		t.Fatal("spec has no def for template 5.0")
	}
	w, err := spec.NewWalk(def, sec5)
	if err != nil {
		t.Fatalf("NewWalk: %v", err)
	}
	ctx := &Context{TemplateNumber: 0, Fields: w, Data: payload, NumValues: len(vals)}
	got, err := Unpack(ctx)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	for k := range vals {
		if got[k] != vals[k] {
			t.Errorf("got[%d] = %v (bits %08x), want %v (bits %08x)",
				k, got[k], math.Float32bits(got[k]), vals[k], math.Float32bits(vals[k]))
		}
	}
}

// TestUnpackSmagNegativeD covers the D field with the same convention:
// D=-2 stored as 0x8002 (eccodes grib_set calibration byte pattern).
// E=0, R=-25.75 (stored pre-scaled), b=8, raws {0,1}:
// value_k = (raw_k + R) × 10^2 → {-2575, -2475}, exact in float32.
func TestUnpackSmagNegativeD(t *testing.T) {
	vals := []float32{-2575.0, -2475.0}
	payload := []byte{0x00, 0x01}
	sec5 := makeSec5Smag(0, uint32(len(vals)), -25.75, 0x0000, 0x8002, 8, 0)
	def, ok := spec.Template(5, 0)
	if !ok {
		t.Fatal("spec has no def for template 5.0")
	}
	w, err := spec.NewWalk(def, sec5)
	if err != nil {
		t.Fatalf("NewWalk: %v", err)
	}
	ctx := &Context{TemplateNumber: 0, Fields: w, Data: payload, NumValues: len(vals)}
	got, err := Unpack(ctx)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	for k := range vals {
		if got[k] != vals[k] {
			t.Errorf("got[%d] = %v, want %v", k, got[k], vals[k])
		}
	}
}

// TestUnpackSmagNegativeZero checks the 0x8000 edge: sign bit set with zero
// magnitude decodes as -0 ≡ 0 (not -32768). E=0x8000, R=0, D=0, b=8,
// raws {1,2} → values {1,2}.
func TestUnpackSmagNegativeZero(t *testing.T) {
	vals := []float32{1.0, 2.0}
	payload := []byte{0x01, 0x02}
	sec5 := makeSec5Smag(0, uint32(len(vals)), 0.0, 0x8000, 0x0000, 8, 0)
	def, ok := spec.Template(5, 0)
	if !ok {
		t.Fatal("spec has no def for template 5.0")
	}
	w, err := spec.NewWalk(def, sec5)
	if err != nil {
		t.Fatalf("NewWalk: %v", err)
	}
	ctx := &Context{TemplateNumber: 0, Fields: w, Data: payload, NumValues: len(vals)}
	got, err := Unpack(ctx)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	for k := range vals {
		if got[k] != vals[k] {
			t.Errorf("got[%d] = %v, want %v", k, got[k], vals[k])
		}
	}
}
