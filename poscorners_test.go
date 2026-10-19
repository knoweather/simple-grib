// all-positive-corner vector testdata/ecc-xcheck/poscorners.grib2, with an
// EXTERNAL cross-check against NCEP wgrib2 / g2c.
//
// WHY POSITIVE CORNERS: signed[4] GRIB fields are decoded by every major
// implementation (ECMWF eccodes 2.41.0 Debian pkg, NCEP g2c 2.1.0, this
// machine's eccodes 2.49.0 build) as SIGN-MAGNITUDE, not two's complement —
// proven byte-exact on 2026-10-05 . For positive
// values the two conventions are identical, so an all-positive grid is
// readable by BOTH our decoder and the external oracles — making it the
// widest-possible cross-check surface for geometry, scan order and packing.
//
// EXTERNAL EVIDENCE (wgrib2 built from NOAA-EMC/wgrib2 develop on the Linux bench host,
// linked against Debian libg2c0 2.1.0; run via the wgrib2 C API):
//
//	GRIDMETA:
//	grid_template=0:winds(N/S):
//		lat-lon grid:(3 x 3) units 1e-06 input WE:NS output WE:SN res 0
//		lat 45.000000 to 25.000000 by 0.000000
//		lon 10.000000 to 30.000000 by 0.000000 #points=9
//	values: 273.21 273.22 273.23 273.18 273.19 273.2 273.15 273.16 273.17
//	points (lon,lat): (10, 25) (20, 25) (30, 25) (10, 35) (20, 35) (30, 35)
//	                   (10, 45) (20, 45) (30, 45)
//
// wgrib2 emits its output order as WE:SN (rows south-to-north), so its value
// list is the row-reversal of our raw storage order — the two agree point
// for point once that documented reordering is applied. Grid fields
// (template 0, 3x3, La1=45/La2=25, Lo1=10/Lo2=30, scan input WE:NS) match
// our decoder exactly.
//
// The fixture: La1=+45, Lo1=+10, La2=+25, Lo2=+30, Ni=Nj=3, Di=Dj=+10 deg,
// scan mode 0 (i fastest, j decreasing); simple packing R=273.15, decimal
// scale 2, binary scale 0, bits=8, payload 0..8 -> values 273.15+k*0.01.

package grib

import (
	"os"
	"testing"
)

func TestPosCornersWgrib2CrossCheck(t *testing.T) {
	f, err := os.Open("testdata/ecc-xcheck/poscorners.grib2")
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
		t.Fatalf("Message(0): %v", err)
	}

	g, err := m.Grid()
	if err != nil {
		t.Fatalf("Grid: %v", err)
	}
	if g.NumPoints() != 9 {
		t.Fatalf("NumPoints = %d, want 9 (wgrib2: #points=9)", g.NumPoints())
	}
	// scan mode 0: i-fastest, -j -> rows at lat 45/35/25, cols at lon 10/20/30.
	type pt struct{ lat, lon float64 }
	want := []pt{
		{45, 10}, {45, 20}, {45, 30},
		{35, 10}, {35, 20}, {35, 30},
		{25, 10}, {25, 20}, {25, 30},
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
	if len(vals) != 9 {
		t.Fatalf("len(Values) = %d, want 9", len(vals))
	}
	for k := 0; k < 9; k++ {
		wantV := 273.15 + float64(k)*0.01
		if !almostEq(float64(vals[k]), wantV, 1e-4) {
			t.Errorf("Values()[%d] = %v, want %v", k, vals[k], wantV)
		}
	}
}
