//
// xcheck_p6_b_gribw — section D: gribw encode 5.0/5.4 round-trip +
// writer-side fail-loud (D5).
//
// Method : the expected
// values are my own input vectors chosen so that the packing is exact for
// the parameters used — no assertion anchor from the tested side is reused.
// Each case: gribw writes a full GRIB2 message → self-read round-trip via
// the reader must be BIT-EXACT (Float32bits equality). The written files are
// also emitted to testdata/p6-b-xcheck/ for the the Linux bench host eccodes read-back
// cross-check (independent decoder; commands + sha256 recorded in the
// report post).
//
// Internal package gribw on purpose: reuses fixtureGrid/canonicalID/
// canonicalProduct/readBack from message_test.go (test infrastructure, not
// assertion anchors).

package gribw

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/knoweather/simple-grib/pack"
)

// p6bBuild writes a canonical message with the given sec5+payload.
func p6bBuild(t *testing.T, values []float32, sec5, payload []byte) []byte {
	t.Helper()
	if g := fixtureGrid(t); len(values) != g.NumPoints() {
		t.Fatalf("vector length %d != grid point count %d", len(values), g.NumPoints())
	}
	m := New()
	if err := m.SetDiscipline(10); err != nil {
		t.Fatalf("SetDiscipline: %v", err)
	}
	if err := m.SetIdentification(canonicalID()); err != nil {
		t.Fatalf("SetIdentification: %v", err)
	}
	if err := m.SetGrid(fixtureGrid(t)); err != nil {
		t.Fatalf("SetGrid: %v", err)
	}
	if err := m.SetProduct(canonicalProduct()); err != nil {
		t.Fatalf("SetProduct: %v", err)
	}
	if err := m.SetDataSection(sec5, payload); err != nil {
		t.Fatalf("SetDataSection: %v", err)
	}
	b, err := m.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return b
}

// p6bRoundTrip asserts the self-read values are bit-identical to in.
func p6bRoundTrip(t *testing.T, name string, in []float32, sec5, payload []byte) {
	t.Helper()
	b := p6bBuild(t, in, sec5, payload)
	m := readBack(t, b)
	got, err := m.Values()
	if err != nil {
		t.Fatalf("%s: self-read Values: %v", name, err)
	}
	if len(got) != len(in) {
		t.Fatalf("%s: got %d values, want %d", name, len(got), len(in))
	}
	for i := range in {
		if math.Float32bits(got[i]) != math.Float32bits(in[i]) {
			t.Errorf("%s: value[%d] = %v (bits %08x), want %v (bits %08x)",
				name, i, got[i], math.Float32bits(got[i]), in[i], math.Float32bits(in[i]))
		}
	}
}

// p6bEmitFixture writes the encoded message to testdata/p6-b-xcheck/<name>
// (idempotent: deterministic bytes) and reports its sha256 for the manifest.
func p6bEmitFixture(t *testing.T, name string, b []byte) {
	t.Helper()
	dir := filepath.Join("..", "testdata", "p6-b-xcheck")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", p, err)
	}
	sum := sha256.Sum256(b)
	t.Logf("fixture %s sha256=%s", name, hex.EncodeToString(sum[:]))
}

// --- D1: 5.0 simple packing, integers, positive/negative/zero mix ----------
// R=-8192 (pre-scaled form; D=0), E=0, bits=16 → raw = v+8192 ∈ [0, 65536).

func TestP6B_D1_SimpleIntegers(t *testing.T) {
	in := []float32{0, -7, 3, 128, -129, 4096, -4097, 1, -1}
	sec5, payload, err := EncodeSimple(in, SimpleParams{R: -8192, E: 0, D: 0, Bits: 16})
	if err != nil {
		t.Fatalf("EncodeSimple: %v", err)
	}
	p6bRoundTrip(t, "D1", in, sec5, payload)
	b := p6bBuild(t, in, sec5, payload)
	p6bEmitFixture(t, "d1_simple_integers.grib2", b)
}

// --- D2: 5.0 simple packing, decimals k/10^D exactly representable ---------
// D=2, E=0, R=-1234 (= -12.34 pre-scaled) → raw = round(v×100 + 1234).

func TestP6B_D2_SimpleDecimals(t *testing.T) {
	in := []float32{0.13, -0.25, 0.00, 1.75, -12.34, 99.99, 0.01, -0.01, 3.14}
	sec5, payload, err := EncodeSimple(in, SimpleParams{R: -1234, E: 0, D: 2, Bits: 16})
	if err != nil {
		t.Fatalf("EncodeSimple: %v", err)
	}
	p6bRoundTrip(t, "D2", in, sec5, payload)
	b := p6bBuild(t, in, sec5, payload)
	p6bEmitFixture(t, "d2_simple_decimals.grib2", b)
}

// --- D3: 5.0 constant field (Bits=0) ----------------------------------------

func TestP6B_D3_SimpleConstant(t *testing.T) {
	in := make([]float32, 9)
	for i := range in {
		in[i] = 5.5
	}
	sec5, payload, err := EncodeSimple(in, SimpleParams{R: 5.5, E: 0, D: 0, Bits: 0})
	if err != nil {
		t.Fatalf("EncodeSimple: %v", err)
	}
	if len(payload) != 0 {
		t.Fatalf("constant field payload = %d bytes, want 0", len(payload))
	}
	p6bRoundTrip(t, "D3", in, sec5, payload)
	b := p6bBuild(t, in, sec5, payload)
	p6bEmitFixture(t, "d3_simple_constant.grib2", b)
}

// --- D4: 5.4 IEEE binary32 — normal/denormal/extreme/Inf/NaN ----------------
// EncodeIEEE copies the raw 32-bit pattern (gribw/ieee.go); the reader decodes
// via Float32frombits → round-trip is bit-exact by construction for EVERY
// float32 bit pattern. The eccodes read-back on the Linux bench host is the independent check.

func TestP6B_D4_IEEE(t *testing.T) {
	in := []float32{
		1.0,
		-1.0,
		0.0,
		3.4028234663852886e+38, // +max normal
		1.401298464324817e-45,  // smallest positive denormal
		2.2250738585072014e-30, // smallest positive normal
		float32(math.Inf(1)),   // +Inf (exact conversion per Go spec)
		float32(math.Inf(-1)),  // -Inf
		float32(math.NaN()),    // NaN
	}
	sec5, payload, err := EncodeIEEE(in, IEEEParams{Precision: 1})
	if err != nil {
		t.Fatalf("EncodeIEEE: %v", err)
	}
	p6bRoundTrip(t, "D4", in, sec5, payload)
	b := p6bBuild(t, in, sec5, payload)
	p6bEmitFixture(t, "d4_ieee_extremes.grib2", b)
}

// --- D5: writer-side fail-loud ----------------------------------------------
// EncodeIEEE rejects precision != 1 with a PLAIN error (gribw/ieee.go), not
// *pack.ErrUnsupportedValue — reader/writer asymmetry recorded in the report.
// EncodeSimple rejects non-finite values and out-of-range raws.

func TestP6B_D5_WriterFailLoud(t *testing.T) {
	t.Run("IEEE precision 0/2/3", func(t *testing.T) {
		for _, prec := range []int{0, 2, 3} {
			if _, _, err := EncodeIEEE([]float32{1}, IEEEParams{Precision: prec}); err == nil {
				t.Errorf("EncodeIEEE(precision=%d) succeeded; want fail-loud", prec)
			}
		}
	})
	t.Run("simple packing NaN/Inf rejected", func(t *testing.T) {
		for _, v := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
			if _, _, err := EncodeSimple([]float32{v}, SimpleParams{R: 0, E: 0, D: 0, Bits: 16}); err == nil {
				t.Errorf("EncodeSimple(%v) succeeded; want fail-loud", v)
			}
		}
	})
	t.Run("simple packing raw out of range rejected", func(t *testing.T) {
		// bits=8 → raw ∈ [0,256); R=0, E=0, D=0: value 300 needs raw=300.
		if _, _, err := EncodeSimple([]float32{300}, SimpleParams{R: 0, E: 0, D: 0, Bits: 8}); err == nil {
			t.Error("EncodeSimple(300, bits=8) succeeded; want fail-loud (raw out of range)")
		}
	})
	t.Run("constant field mismatch rejected", func(t *testing.T) {
		if _, _, err := EncodeSimple([]float32{5.5, 6.5}, SimpleParams{R: 5.5, E: 0, D: 0, Bits: 0}); err == nil {
			t.Error("EncodeSimple(constant mismatch) succeeded; want fail-loud")
		}
	})
}

// --- Cross-package sanity: reader error type for a hand-built bad sec5 ------
// (kept here so the gribw import surface is exercised in one place; the pack
// level cases live in pack/xcheck_p6_b_errors_test.go)

func TestP6B_D5b_ReaderIEEEPrecisionError(t *testing.T) {
	// Build a valid message, then swap its section 5 for a hand-assembled
	// IEEE header with precision=2 — the reader must fail loud with
	// *pack.ErrUnsupportedValue.
	in := make([]float32, 9)
	sec5, payload, err := EncodeIEEE(in, IEEEParams{Precision: 1})
	if err != nil {
		t.Fatalf("EncodeIEEE: %v", err)
	}
	b := p6bBuild(t, in, sec5, payload)

	// Locate section 5 and replace it with the precision=2 header.
	pos := 16
	found := false
	for pos+5 <= len(b) {
		slen := int(binary.BigEndian.Uint32(b[pos : pos+4]))
		num := b[pos+4]
		if num == 5 {
			found = true
			bad := make([]byte, 12)
			copy(bad, sec5) // same NV (9) at octets 6-9
			bad[11] = 2     // precision = 2 (64-bit), octet 12
			newb := append(append([]byte{}, b[:pos]...), bad...)
			newb = append(newb, b[pos+slen:]...)
			// Fix section 0 totalLen (U64 big-endian at bytes 8-15).
			binary.BigEndian.PutUint64(newb[8:16], uint64(len(newb)))
			b = newb
			break
		}
		pos += slen
	}
	if !found {
		t.Fatal("section 5 not found in built message")
	}

	m := readBack(t, b)
	if _, err := m.Values(); err == nil {
		t.Fatal("Values() succeeded on precision=2 IEEE; want fail-loud")
	} else {
		var e *pack.ErrUnsupportedValue
		if !errors.As(err, &e) {
			t.Fatalf("want *pack.ErrUnsupportedValue, got %T: %v", err, err)
		}
		if e.Template != pack.TemplateIEEE || e.Field != "precision" || e.Value != 2 {
			t.Errorf("got {Template: %d, Field: %q, Value: %d}", e.Template, e.Field, e.Value)
		}
	}
}
