// oracle fixture: a two-message GRIB2 file exercising both supported
// data-representation templates in one file (the dual-message shape the
// oracle must cover, ruling ). Message 0 is simple-packed (S2 vector:
// D=1, E=0, R stored -5.0, bits=8 → {0.1..0.9}); message 1 is IEEE 5.4
// (arbitrary bit patterns incl. ±Inf/NaN/-0.0/denormal). Both messages share
// the canonical identification/product/grid of the fixtures.

package gribw

import (
	"os"
	"path/filepath"
	"testing"
)

// TestP35DualFixtureEmit writes testdata/gribw-xcheck/p35_dual.grib2 when
// GRIBW_GOLDEN_EMIT=1 (same env-gate pattern as TestGoldenEmit). The emitted
// file is committed as an oracle input fixture; its md5 is asserted by the
// in-repo round-trip tests and re-verified by the the Linux bench host oracle harness.
func TestP35DualFixtureEmit(t *testing.T) {
	if os.Getenv("GRIBW_GOLDEN_EMIT") != "1" {
		t.Skip("set GRIBW_GOLDEN_EMIT=1 to (re)generate the dual fixture")
	}

	simpleVals := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9}

	mk := func(sec5, payload []byte) *Message {
		m := New()
		if err := m.SetDiscipline(10); err != nil { // oceanographic — canonical identity
			t.Fatal(err)
		}
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
	i5, ip, err := EncodeIEEE(ieeeGolden, IEEEParams{Precision: 1})
	if err != nil {
		t.Fatalf("EncodeIEEE: %v", err)
	}

	b, err := File(mk(s5, pl), mk(i5, ip))
	if err != nil {
		t.Fatalf("File: %v", err)
	}

	dir := filepath.Join("..", "testdata", "gribw-xcheck")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "p35_dual.grib2")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%d bytes)", path, len(b))
}
