//
// Binary-level regression for NEGATIVE corner fields encoded in the
// SIGN-MAGNITUDE convention (bit 31 = sign, low 31 bits = magnitude), per the
// contract ruling (, user decision option (a), ).
// Sign-magnitude is the de facto standard of every real-world implementation:
//   - NCEP g2c src/int8.c:int4() — explicit sign-magnitude branch
//     (high bit set -> negate low 31 bits); grid path uses int4, not int4_comp.
//   - ECMWF eccodes 2.41.0 (Debian libeccodes-tools): grib_set -s
//     latitudeOfFirstGridPoint=-45000000 writes 82AEA540 in place; reads back
// self-consistent. M independently reproduced ().
// - This build's eccodes 2.49.0: sign-magnitude on both sides .
// Two's-complement byte patterns in these fields decode to ~2^31 microdegree
// magnitudes (~2057-2147 deg) under this convention and are rejected by the
// range/span fail-loud checks — no such files exist in the real world.
//
// EXTERNAL CROSS-CHECK (closed, ): A's sign-magnitude all-negative
// variant testdata/ecc-xcheck/negcorners_signmag.grib2 (B's canonical
// poscorners base + sec3 corner patch only, md5 8dc1d45fc8143bd57814fc9d0432aebf)
// reads back EXACTLY on standard eccodes 2.41.0 (La1/Lo1/La2/Lo2/Di/Dj =
// -60/-90/-40/-70/+10/+10 deg); the NCEP-style longitude variant
// testdata/ecc-xcheck/negcorners_lon360.grib2 (Lo as unsigned [0,360):
// +270/+290, md5 e059cd1102060d0e86cdf59c9c64f0da) parses clean on wgrib2
// 3.8.0 (lat -60 to -40, lon 270 to 290, no BAD GDS). Both raw outputs posted
// to the board; both fixtures committed in 719ce73.
//
// Expected values below were derived by hand from the WMO §5 formulas (and
// the eccodes grib_geography.cc unrotate formula replicated in rotate.go),
// independently of this package's code, and verified with a standalone float64
// implementation before being written as literals. The tests pin the
// sign-magnitude interpretation and guard against regression to two's
// complement (which would mis-decode every real-world negative-corner file).
//
// WMO Note 45 (template 3.0, octets 64-67 & 68-71): "It is recommended to
// use unsigned direction increments." Scan direction therefore travels in the
// scan-mode bits; a signed-form Di/Dj byte pattern is outside the recommended
// form and must be rejected by the A-B5 fail-loud span check —
// TestNegCornerSignedIncrementFailsLoud documents exactly that.

package grid

import (
	"math"
	"strings"
	"testing"
)

// TestNegCornerRegularLL checks template 3.0 with all-negative corner
// latitudes/longitudes stored as sign-magnitude microdegrees:
//
//	La1 = -60 deg -> 83 93 87 00   Lo1 = -90 deg -> 85 5D 4A 80
//	La2 = -40 deg -> 82 62 5A 00   Lo2 = -70 deg -> 84 2C 1D 80
//	Di = Dj = +10 deg -> 00 98 96 80 (unsigned per WMO Note 45)
//	Ni = Nj = 3, span check: |La2-La1| = 20 = (Nj-1)*Dj, |Lo2-Lo1| = 20 = (Ni-1)*Di
//	scanMode = 0x40 (jScansPositively; i positive by default)
func TestNegCornerRegularLL(t *testing.T) {
	b := makeSec3(0, func(b []byte) {
		fillBaseRaw(b,
			0x83938700, // La1 = -60 deg (sign-magnitude of -60e6)
			0x855D4A80, // Lo1 = -90 deg
			0x82625A00, // La2 = -40 deg
			0x842C1D80, // Lo2 = -70 deg
			0x00989680, // Di = +10 deg
			0x00989680, // Dj = +10 deg
			3, 3, 0, 0x40)
	})
	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v (negative sign-magnitude corners must parse)", err)
	}
	if n := g.NumPoints(); n != 9 {
		t.Fatalf("NumPoints = %d, want 9", n)
	}
	// Point(0) is the first corner; point 8 the last (j=2,i=2).
	cases := []struct {
		i       int
		lat, ln float64
	}{
		{0, -60.0, -90.0},
		{1, -60.0, -80.0},
		{2, -60.0, -70.0},
		{3, -50.0, -90.0},
		{8, -40.0, -70.0},
	}
	for _, c := range cases {
		lat, lon, err := g.Point(c.i)
		if err != nil {
			t.Fatalf("Point(%d): %v", c.i, err)
		}
		if !almostEq(lat, c.lat, 1e-9) || !almostEq(lon, c.ln, 1e-9) {
			t.Errorf("Point(%d) = (%v, %v), want (%v, %v)", c.i, lat, lon, c.lat, c.ln)
		}
	}
}

// TestNegCornerRotatedSpLat checks template 3.1 with a NEGATIVE southern-pole
// latitude (the binary path left untested by P1 because the probe-era eccodes
// build could not encode it compliantly):
//
//	rotated-frame base: La1 = +30, Lo1 = +15, Di = +15, Dj = +20, Ni = Nj = 3
//	                      (scanMode 0x40; spans consistent: 70 = 30+2*20, 45 = 15+2*15)
//	spLat = -45 deg -> 82 AE A5 40   spLon = +60 deg -> 03 93 87 00 (unsigned)
//	angle = F32(90.0) = 42 B4 00 00 (exact in float32)
//
// Expected geographic coordinates are the hand-derived values of
// unrotate(lat, lon, angle, spLat, spLon) per the eccodes grib_geography.cc
// formula (see rotate.go), computed independently:
//
//	Point(0): rotated ( 30,  15) -> ( 70.91941569989237,  13.28830828476191)
//	Point(8): rotated ( 70,  45) -> ( 56.665132367912726, 123.89020099690401)
func TestNegCornerRotatedSpLat(t *testing.T) {
	b := makeSec3(1, func(b []byte) {
		fillBase(b, 30, 15, 70, 45, 15, 20, 3, 3, 0, 0x40)
		putU32(b, 72, 0x82AEA540)             // octets 73-76: spLat = -45 deg (sign-magnitude)
		putU32(b, 76, 0x03938700)             // octets 77-80: spLon = +60 deg (unsigned)
		putU32(b, 80, math.Float32bits(90.0)) // octets 81-84: rotation angle F32
	})
	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []struct {
		i       int
		lat, ln float64
	}{
		{0, 70.91941569989237, 13.28830828476191},
		{8, 56.665132367912726, 123.89020099690401},
	}
	for _, c := range cases {
		lat, lon, err := g.Point(c.i)
		if err != nil {
			t.Fatalf("Point(%d): %v", c.i, err)
		}
		if !almostEq(lat, c.lat, 1e-9) || !almostEq(lon, c.ln, 1e-9) {
			t.Errorf("Point(%d) = (%v, %v), want (%v, %v)", c.i, lat, lon, c.lat, c.ln)
		}
	}
}

// TestNegCornerSignedIncrementFailsLoud documents the WMO Note 45 boundary:
// direction increments are recommended to be UNSIGNED (direction travels in
// the scan-mode bits). A signed-form Dj byte pattern is read as a huge
// unsigned magnitude (0xFF676980 -> 4284.967296 deg), which contradicts the
// La1/La2 span and must be rejected by the A-B5 fail-loud check at New() —
// not silently mis-decoded.
func TestNegCornerSignedIncrementFailsLoud(t *testing.T) {
	b := makeSec3(0, func(b []byte) {
		fillBaseRaw(b,
			0x83938700, // La1 = -60 deg
			0x855D4A80, // Lo1 = -90 deg
			0x82625A00, // La2 = -40 deg
			0x842C1D80, // Lo2 = -70 deg
			0x00989680, // Di = +10 deg (consistent)
			0xFF676980, // Dj = signed-form byte pattern (NOT the recommended unsigned form)
			3, 3, 0, 0x40)
	})
	_, err := New(b)
	if err == nil {
		t.Fatalf("New accepted a signed-form Dj; want fail-loud span error (WMO Note 45 + A-B5)")
	}
	if !strings.Contains(err.Error(), "contradicts La1/La2 span") {
		t.Errorf("error = %q, want the stored-Dj span contradiction message", err)
	}
}
