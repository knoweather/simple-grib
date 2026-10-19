// tests for the template 5.0 simple-packing encoder (gribw.EncodeSimple).
// Golden vectors were pre-computed independently of the encoder (python mirror
// of the reader's decode arithmetic, all bit-exact; see the dev-prep note)
// and pin both halves of the inspectable API form (ruling B①): sec5
// header bytes and payload bytes are compared separately, then the pair is
// integrated through Message.SetDataSection → Build → reader round-trip.

package gribw

import (
	"bytes"
	"encoding/hex"
	"math"
	"testing"

	grib "github.com/knoweather/simple-grib"
)

func hexB(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex literal %q: %v", s, err)
	}
	return b
}

// goldenVector is one pre-computed (params → sec5/payload/expected values) case.
type goldenVector struct {
	name    string
	params  SimpleParams
	values  []float32
	sec5    string // expected section 5 header, hex
	payload string // expected section 7 data, hex ("" = empty constant field)
}

// The five vectors S1-S4 (+S5 as a fail-loud case below). R is the stored
// (pre-scaled by 10^D) reference value per / .
var simpleVectors = []goldenVector{
	{
		name:    "S1 trivial bits=16",
		params:  SimpleParams{R: 0, E: 0, D: 0, Bits: 16},
		values:  []float32{0, 1, 2, 3, 4, 5, 6, 7, 8},
		sec5:    "000000150500000009000000000000000000001000",
		payload: "000000010002000300040005000600070008",
	},
	{
		name:    "S2 decimal scaling D=1 R=-5 bits=8",
		params:  SimpleParams{R: -5.0, E: 0, D: 1, Bits: 8},
		values:  []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9},
		sec5:    "0000001505000000090000c0a00000000000010800",
		payload: "060708090a0b0c0d0e",
	},
	{
		name:    "S3 negative E=-3 (sign-magnitude path, §5.12 rev 2026-10-05)",
		params:  SimpleParams{R: 0, E: -3, D: 0, Bits: 8},
		values:  []float32{8, 16, 24, 8, 16, 24, 8, 16, 24},
		sec5:    "000000150500000009000000000000800300000800",
		payload: "4080c04080c04080c0",
	},
	{
		name:    "S4 bits=0 constant field 273.15 (R stored pre-scaled as 27315, D=2)",
		params:  SimpleParams{R: 27315.0, E: 0, D: 2, Bits: 0},
		values:  constantFieldValues(), // float32(float64(R)*10^-2) ×9 — see helper
		sec5:    "000000150500000009000046d56600000000020000",
		payload: "",
	},
}

// constantFieldValues reproduces the encoder's own constant expression so the
// inputs are exactly what the bits=0 check compares against.
func constantFieldValues() []float32 {
	c := float32(float64(SimpleParams{R: 27315.0, D: 2}.R) * math.Pow(10, -2))
	out := make([]float32, 9)
	for i := range out {
		out[i] = c
	}
	return out
}

func TestEncodeSimpleGoldenVectors(t *testing.T) {
	for _, v := range simpleVectors {
		t.Run(v.name, func(t *testing.T) {
			sec5, payload, err := EncodeSimple(v.values, v.params)
			if err != nil {
				t.Fatalf("EncodeSimple: %v", err)
			}
			wantSec5 := hexB(t, v.sec5)
			if !bytes.Equal(sec5, wantSec5) {
				t.Errorf("sec5 = %x, want %x", sec5, wantSec5)
			}
			wantPayload := hexB(t, v.payload)
			if !bytes.Equal(payload, wantPayload) {
				t.Errorf("payload = %x (%d bytes), want %x (%d bytes)", payload, len(payload), wantPayload, len(wantPayload))
			}
		})
	}
}

// TestEncodeSimpleRoundTrip: each golden vector's (sec5, payload) pair goes
// through the frame (SetDataSection → Build) and back through the reader;
// values must come out bit-exact.
func TestEncodeSimpleRoundTrip(t *testing.T) {
	for _, v := range simpleVectors {
		t.Run(v.name, func(t *testing.T) {
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
			sec5, payload, err := EncodeSimple(v.values, v.params)
			if err != nil {
				t.Fatalf("EncodeSimple: %v", err)
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
			for i := range v.values {
				if math.Float32bits(got[i]) != math.Float32bits(v.values[i]) {
					t.Errorf("value[%d] bits = %08x, want %08x (%v)", i,
						math.Float32bits(got[i]), math.Float32bits(v.values[i]), v.values[i])
				}
			}
		})
	}
}

// TestEncodeSimpleConstantFieldFailLoud: bits=0 accepts only inputs that all
// equal the decoded constant R×10^(−D) (ruling B②).
func TestEncodeSimpleConstantFieldFailLoud(t *testing.T) {
	vals := constantFieldValues()
	oneOff := append(append([]float32{}, vals...), 0)[:8]
	oneOff[4] = vals[4] + 1 // one value deviates from the constant

	if _, _, err := EncodeSimple(oneOff, SimpleParams{R: 27315.0, E: 0, D: 2, Bits: 0}); err == nil {
		t.Fatal("bits=0 with a non-constant value: expected fail-loud error, got nil")
	} else if !bytes.Contains([]byte(err.Error()), []byte("bits=0 constant field")) {
		t.Errorf("error %q does not identify the bits=0 constant-field rule", err.Error())
	}

	// The all-equal case must succeed with an empty payload.
	sec5, payload, err := EncodeSimple(vals, SimpleParams{R: 27315.0, E: 0, D: 2, Bits: 0})
	if err != nil {
		t.Fatalf("all-constant bits=0: %v", err)
	}
	if len(payload) != 0 {
		t.Errorf("bits=0 payload = %x, want empty", payload)
	}
	if !bytes.Equal(sec5, hexB(t, "000000150500000009000046d56600000000020000")) {
		t.Errorf("bits=0 sec5 = %x, want golden S4", sec5)
	}
}

// TestEncodeSimpleFailLoud: parameter range and value-range violations.
func TestEncodeSimpleFailLoud(t *testing.T) {
	nine := func(v float32) []float32 {
		out := make([]float32, 9)
		for i := range out {
			out[i] = v
		}
		return out
	}
	nanVals := nine(0)
	nanVals[3] = math.Float32frombits(0x7fc00000)
	cases := []struct {
		name   string
		values []float32
		p      SimpleParams
		want   string
	}{
		{"E below int16", nine(0), SimpleParams{R: 0, E: -32769, D: 0, Bits: 8}, "sign-magnitude int16"},
		{"E above int16", nine(0), SimpleParams{R: 0, E: 32768, D: 0, Bits: 8}, "sign-magnitude int16"},
		{"D below int16", nine(0), SimpleParams{R: 0, E: 0, D: -32769, Bits: 8}, "sign-magnitude int16"},
		{"bits negative", nine(0), SimpleParams{R: 0, E: 0, D: 0, Bits: -1}, "[0,32]"},
		{"bits above 32", nine(0), SimpleParams{R: 0, E: 0, D: 0, Bits: 33}, "[0,32]"},
		{"NaN value", nanVals, SimpleParams{R: 0, E: 0, D: 0, Bits: 16}, "not finite"},
		{"+Inf value", nine(float32(math.Inf(1))), SimpleParams{R: 0, E: 0, D: 0, Bits: 16}, "not finite"},
		// S5: raw overflow — 300 needs 9 bits at bits=8.
		{"raw above 2^bits", nine(300), SimpleParams{R: 0, E: 0, D: 0, Bits: 8}, "outside [0,"},
		// Negative raw — value below the reference with R=0.
		{"negative raw", nine(-1), SimpleParams{R: 0, E: 0, D: 0, Bits: 8}, "outside [0,"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := EncodeSimple(tc.values, tc.p)
			if err == nil {
				t.Fatalf("expected fail-loud error, got nil")
			}
			if !bytes.Contains([]byte(err.Error()), []byte(tc.want)) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestEncodeSimpleIntegrationWithFile: the encoder's output inside a two-
// message file (one simple-packed, one IEEE) round-trips end to end — the
// dual-message oracle shape with real encoder output.
func TestEncodeSimpleIntegrationWithFile(t *testing.T) {
	simpleVals := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9}
	ieeeVals := []float32{1.5, -0.5, 0, float32(math.Inf(1)), math.Float32frombits(0x7fc00000),
		math.SmallestNonzeroFloat32, -math.SmallestNonzeroFloat32, math.MaxFloat32, 42}

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
	s5, pl, err := EncodeSimple(simpleVals, SimpleParams{R: -5.0, E: 0, D: 1, Bits: 8})
	if err != nil {
		t.Fatalf("EncodeSimple: %v", err)
	}
	b, err := File(mk(s5, pl), mk(ieeeSec5(), f32s(ieeeVals...)))
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
	m0, _ := f.Message(0)
	got0, err := m0.Values()
	if err != nil {
		t.Fatal(err)
	}
	for i := range simpleVals {
		if math.Float32bits(got0[i]) != math.Float32bits(simpleVals[i]) {
			t.Errorf("msg0 value[%d] = %v, want %v", i, got0[i], simpleVals[i])
		}
	}
	m1, _ := f.Message(1)
	got1, err := m1.Values()
	if err != nil {
		t.Fatal(err)
	}
	for i := range ieeeVals {
		if math.IsNaN(float64(ieeeVals[i])) {
			if !math.IsNaN(float64(got1[i])) {
				t.Errorf("msg1 value[%d] = %v, want NaN", i, got1[i])
			}
			continue
		}
		if math.Float32bits(got1[i]) != math.Float32bits(ieeeVals[i]) {
			t.Errorf("msg1 value[%d] bits = %08x, want %08x", i, math.Float32bits(got1[i]), math.Float32bits(ieeeVals[i]))
		}
	}
}

// TestEncodeSimpleSmagBoundary pins the sign-magnitude range contract
// (§5.12 rev 2026-10-05, internal E/D bug report): -32768 is NOT representable
// in sign-magnitude int16 and must fail loud (it was accepted under the old
// two's-complement convention); -32767 is the most negative representable
// value and encodes as 0xFFFF. The -32767 byte check uses the Bits=0 constant
// path because any non-constant field with E=-32767 must fail loud in the
// packing arithmetic (2^32767 overflows float64 — verified behavior, not a bug).
func TestEncodeSimpleSmagBoundary(t *testing.T) {
	_, _, err := EncodeSimple([]float32{0, 1}, SimpleParams{R: 0, E: -32768, D: 0, Bits: 8})
	if err == nil {
		t.Fatal("EncodeSimple with E=-32768: want sign-magnitude range error, got nil")
	}
	_, _, err = EncodeSimple([]float32{0, 1}, SimpleParams{R: 0, E: 0, D: -32768, Bits: 8})
	if err == nil {
		t.Fatal("EncodeSimple with D=-32768: want sign-magnitude range error, got nil")
	}
	// Constant field (Bits=0) bypasses the raw arithmetic — clean byte check.
	sec5, _, err := EncodeSimple([]float32{5, 5}, SimpleParams{R: 5, E: -32767, D: 0, Bits: 0})
	if err != nil {
		t.Fatalf("EncodeSimple with E=-32767 (constant): %v", err)
	}
	if got := sec5[15:17]; string(got) != "\xff\xff" {
		t.Errorf("E=-32767 encoded as %x, want ffff (sign-magnitude max magnitude)", got)
	}
	// Non-constant field with E=-32767 must fail loud in the packing arithmetic.
	_, _, err = EncodeSimple([]float32{0, 1}, SimpleParams{R: 0, E: -32767, D: 0, Bits: 8})
	if err == nil {
		t.Fatal("EncodeSimple with E=-32767 non-constant: want packing-arithmetic overflow error, got nil")
	}
}

// TestEncodeSimpleSmagRoundTrip is the in-repo regression for the E/D bug
// : encode with negative E through the frame and decode back
// through the reader — values must be bit-exact, and the on-wire E bytes must
// be the sign-magnitude pattern 0x8006 (NOT two's complement 0xFFFA). The
// real-data half of this check is old_enfo_2023 (the Linux bench host /root/aws-ecmwf-samples,
// scripts/xcheck/ed-smag-oracle.sh; not in repo by size policy).
func TestEncodeSimpleSmagRoundTrip(t *testing.T) {
	// 9 values (fixtureGrid is 3x3): v_k = -1 + k/64 with raws below 2^12.
	vals := []float32{-1.0, -0.5, 0.0, 17.90625, -0.921875, 0.5625, 2.125, 3.6875, 53.28125}
	params := SimpleParams{R: -1.0, E: -6, D: 0, Bits: 12}
	sec5, payload, err := EncodeSimple(vals, params)
	if err != nil {
		t.Fatalf("EncodeSimple: %v", err)
	}
	if got := sec5[15:17]; string(got) != "\x80\x06" {
		t.Fatalf("E=-6 encoded as %x, want 8006 (sign-magnitude)", got)
	}
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
	b, err := File(m)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	f, err := grib.OpenReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()
	m0, _ := f.Message(0)
	got, err := m0.Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	for k := range vals {
		if math.Float32bits(got[k]) != math.Float32bits(vals[k]) {
			t.Errorf("round-trip got[%d]=%v (bits %08x), want %v (bits %08x)",
				k, got[k], math.Float32bits(got[k]), vals[k], math.Float32bits(vals[k]))
		}
	}
}
