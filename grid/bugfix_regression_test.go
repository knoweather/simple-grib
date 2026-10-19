// Regression tests for the grid-package correctness fixes (bug.md §1.3/§1.4/
// §3.1/§3.3). Each cites the WMO note or ecCodes definition that fixes the
// semantics.

package grid

import (
	"math"
	"testing"
)

// Ni×Nj must be rejected fail-loud when it overflows int or exceeds the
// supported maximum, never overflowing to a negative count (bug.md §1.3).
func TestPointCountOverflowRejected(t *testing.T) {
	b := makeSec3(0, func(b []byte) {
		putU32(b, 30, 4_000_000_000) // Ni
		putU32(b, 34, 4_000_000_000) // Nj
		putU32(b, 42, missingU32)
		putU32(b, 63, missingU32) // Di missing -> derived (0)
		putU32(b, 67, missingU32) // Dj missing -> derived (0)
	})
	if _, err := New(b); err == nil {
		t.Errorf("New accepted Ni=Nj=4e9; want a fail-loud point-count error")
	}
}

// A quasi-regular point list whose entries sum beyond the cap must be
// rejected, not drive an unbounded allocation (bug.md §1.4).
func TestReducedPointListBounded(t *testing.T) {
	b := makeSec3(0, func(b []byte) {
		b[10] = 4 // octet 11: point-list element width
		putU32(b, 30, missingU32)
		putU32(b, 34, 3) // Nj = 3 rows
		for i := 72; i < len(b); i++ {
			b[i] = 0xFF // every entry = 0xFFFFFFFF points
		}
	})
	if _, err := New(b); err == nil {
		t.Errorf("New accepted an unbounded quasi-regular point list")
	}
}

// Earth-dimension scale factors are unsigned[1] (ecCodes
// template.3.shape_of_the_earth.def); 0xFF is the missing sentinel and yields
// NaN (bug.md §3.3).
func TestEarthScaleUnsignedAndMissing(t *testing.T) {
	mk := func(scale byte) *Definition {
		b := makeSec3(0, func(b []byte) {
			b[14] = 1 // shape 1: spherical, radius given
			b[15] = scale
			putU32(b, 16, 6371229)
			putU32(b, 30, 1)
			putU32(b, 34, 1)
			putU32(b, 42, missingU32)
			putU32(b, 63, 0)
			putU32(b, 67, 0)
		})
		g, err := New(b)
		if err != nil {
			t.Fatalf("New(scale=%#x): %v", scale, err)
		}
		return g
	}
	if got := mk(0).Radius(); got != 6371229 {
		t.Errorf("scale 0: Radius() = %v, want 6371229", got)
	}
	if got, want := mk(1).Radius(), 6371229.0/10; !almostEq(got, want, 1e-6) {
		t.Errorf("scale 1: Radius() = %v, want %v", got, want)
	}
	// 0x81 = unsigned 129, NOT sign-magnitude -1.
	if got, want := mk(0x81).Radius(), 6371229*math.Pow(10, -129); got != want {
		t.Errorf("scale 0x81: Radius() = %v, want %v (unsigned 129)", got, want)
	}
	if got := mk(0xFF).Radius(); !math.IsNaN(got) {
		t.Errorf("scale 0xFF (missing): Radius() = %v, want NaN", got)
	}
}

// Di/Dj are unsigned[4] (WMO template 3.0 Note 5 "recommended to use unsigned
// direction increments"; ecCodes template.3.latlon.def). A sign-magnitude
// "negative" increment decodes to a huge value and is rejected fail-loud
// (bug.md §3.1).
func TestDiUnsignedIncrement(t *testing.T) {
	mk := func(diRaw uint32) (*Definition, error) {
		b := makeSec3(0, func(b []byte) {
			b[14] = 6
			putU32(b, 30, 3)
			putU32(b, 34, 1)
			putU32(b, 42, missingU32)
			putSmag(b, 46, 0)
			putSmag(b, 50, 0)
			putSmag(b, 55, 0)
			putSmag(b, 59, 2_000_000) // Lo2 = +2°
			putU32(b, 63, diRaw)
			putU32(b, 67, 0)
			b[71] = 64
		})
		return New(b)
	}

	g, err := mk(1_000_000) // +1° unsigned, consistent with the span
	if err != nil {
		t.Fatalf("New(unsigned Di=+1): %v", err)
	}
	lons, err := g.Lons()
	if err != nil {
		t.Fatalf("Lons(): %v", err)
	}
	if len(lons) != 3 || !almostEq(lons[1], 1, 1e-9) || !almostEq(lons[2], 2, 1e-9) {
		t.Errorf("Lons() = %v, want [0 1 2]", lons)
	}

	if _, err := mk(0x80000001); err == nil {
		t.Errorf("New accepted a sign-magnitude negative Di; want a fail-loud span error")
	}
}

// basicAngle = missing (all ones) means the default 1 / 1e6 (WMO template 3.0
// Note 1) — the whole grid was previously off by ~10 orders of magnitude
// (bug.md §3.2).
func TestBasicAngleMissingDefaults(t *testing.T) {
	b := makeSec3(0, func(b []byte) {
		b[14] = 6
		putU32(b, 30, 1)
		putU32(b, 34, 1)
		putU32(b, 38, missingU32) // basicAngle = missing
		putU32(b, 42, 1_000_000)  // subdivisions
		putSmag(b, 46, 45_000_000)
		putSmag(b, 50, 10_000_000)
		putSmag(b, 55, 45_000_000)
		putSmag(b, 59, 11_000_000)
		putU32(b, 63, 0)
		putU32(b, 67, 0)
	})
	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lat, lon, err := g.Point(0)
	if err != nil {
		t.Fatalf("Point(0): %v", err)
	}
	if !almostEq(lat, 45, 1e-9) || !almostEq(lon, 10, 1e-9) {
		t.Errorf("Point(0) = (%v,%v), want (45,10)", lat, lon)
	}
}

// The Gaussian parameter N is bounded: building the parallel list is O((2N)²),
// so an unbounded N is a CPU/memory denial-of-service.
func TestGaussianNBound(t *testing.T) {
	b := makeSec3(40, func(b []byte) {
		fillBase(b, 60, 0, 30, 20, 0, 0, 2, 2, 0, 0)
		putU32(b, 67, 100_000_000) // octets 68-71: N (not Dj) for template 3.40
	})
	if _, err := New(b); err == nil {
		t.Errorf("New accepted N=1e8; want a fail-loud bound error")
	}
}
