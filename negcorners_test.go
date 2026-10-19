// WMO-compliant negative-corner vector testdata/ecc-xcheck/negcorners.grib2.
//
// WHY THIS FILE EXISTS: signed[4] GRIB grid fields use SIGN-MAGNITUDE in
// every real implementation ( evidence chain, : NCEP
// g2c int8.c:int4 source + gdb value -2102483648; ECMWF eccodes 2.41.0
// grib_set writes -45e6 as 0x82AEA540 and reads it back; local eccodes 2.49.0
// probe). The ruling (user decision) switched this decoder to the
// same convention. The fixture is hand-built byte-by-byte in that convention
// (bit 31 = sign, low 31 bits = magnitude; microdegrees): La1=+45deg,
// Lo1=-90deg (0x855D4A80), La2=-45deg (0x82AEA540), Lo2=0deg, Ni=Nj=3,
// Di=Dj=+45deg, scan mode 0. It is simultaneously:
//   * the binary cross-check target for our signed decoding (this test), and
//   * the external-oracle vector: wgrib2/g2c and standard eccodes read these
// bytes correctly ( dual-oracle run).
// The archived two's-complement byte version lives in
// negcorners_twos_outofrange.grib2 (fail-loud test: negcorners_twos_test.go).
//
// The A-B5 span check doubles as an end-to-end two's-complement validator:
// |La2-La1| = 90 = (Nj-1)*Dj and |Lo2-Lo1| = 90 = (Ni-1)*Di hold ONLY if the
// sign bits are decoded correctly — a sign-magnitude misread fails New() with
// the span contradiction error.

package grib

import (
	"os"
	"testing"
)

func TestNegCornersFullChain(t *testing.T) {
	f, err := os.Open("testdata/ecc-xcheck/negcorners.grib2")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	rdr, err := OpenReader(f, st.Size())
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer rdr.Close()
	m, err := rdr.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v (structure should be valid)", err)
	}

	g, err := m.Grid()
	if err != nil {
		t.Fatalf("Grid: %v (two's-complement decode + A-B5 span check must pass)", err)
	}
	if g.NumPoints() != 9 {
		t.Fatalf("NumPoints = %d, want 9", g.NumPoints())
	}
	// scan mode 0: i-fastest, -j -> rows at lat 45/0/-45, cols at lon -90/-45/0.
	type pt struct{ lat, lon float64 }
	want := []pt{
		{45, -90}, {45, -45}, {45, 0},
		{0, -90}, {0, -45}, {0, 0},
		{-45, -90}, {-45, -45}, {-45, 0},
	}
	for i, w := range want {
		lat, lon, err := g.Point(i)
		if err != nil {
			t.Fatalf("Point(%d): %v", i, err)
		}
		if !almostEq(lat, w.lat, 1e-9) || !almostEq(lon, w.lon, 1e-9) {
			t.Errorf("Point(%d) = (%v,%v), want (%v,%v)", i, lat, lon, w.lat, w.lon)
		}
	}

	vals, err := m.Values()
	if err != nil {
		t.Fatalf("Values(): %v", err)
	}
	if len(vals) != 9 || !almostEq(float64(vals[0]), 273.15, 1e-4) || !almostEq(float64(vals[8]), 273.23, 1e-4) {
		t.Errorf("Values() = %v, want 9 values 273.15..273.23", vals)
	}
}
