package grid

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeSec3 builds a synthetic section 3 byte slice (octet 1 based, including
// the 4-byte length prefix) with template number tmpl and the given base
// fields filled; fill may overwrite anything. Angles are microdegrees unless
// the caller changes basicAngle/subdivisions.
func makeSec3(tmpl uint16, fill func(b []byte)) []byte {
	b := make([]byte, 100)
	binary.BigEndian.PutUint32(b[0:4], uint32(len(b)))
	b[4] = 3 // section number
	// octet 6 source, octets 7-10 declared points (0), octet 11 width,
	// octet 12 interp left zero
	binary.BigEndian.PutUint16(b[12:14], tmpl)
	if fill != nil {
		fill(b)
	}
	return b
}

// putU32 writes a big-endian U32 at 0-based offset off.
func putU32(b []byte, off int, v uint32) { binary.BigEndian.PutUint32(b[off:off+4], v) }

// putSmag writes a sign-magnitude encoded int32 (bit 31 = sign, low 31 bits =
// magnitude) at 0-based offset off — the signed[4] convention of every real
// GRIB2 implementation .
func putSmag(b []byte, off int, v int32) {
	var raw uint32
	if v < 0 {
		raw = 0x80000000 | uint32(-v)
	} else {
		raw = uint32(v)
	}
	binary.BigEndian.PutUint32(b[off:off+4], raw)
}

// fillBase fills the template 3.0 base fields in degrees
// with the default microdegree unit; Di/Dj given explicitly. Signed angle
// fields are written sign-magnitude ( convention).
func fillBase(b []byte, La1, Lo1, La2, Lo2, Di, Dj float64, Ni, Nj uint32, resFlags, scanMode byte) {
	b[14] = 6        // shape: sphere R=6371229 (table 3.2)
	putU32(b, 16, 0) // radius scale/value unused for lat/lon coords
	putU32(b, 30, Ni)
	putU32(b, 34, Nj)
	// basicAngle=0 / subdivisions=missing -> unit 1e-6 deg (§5.3 defaults)
	putU32(b, 42, missingU32)
	putSmag(b, 46, int32(La1*1e6))
	putSmag(b, 50, int32(Lo1*1e6))
	b[54] = resFlags
	putSmag(b, 55, int32(La2*1e6))
	putSmag(b, 59, int32(Lo2*1e6))
	putSmag(b, 63, int32(Di*1e6))
	putSmag(b, 67, int32(Dj*1e6))
	b[71] = scanMode
}

func almostEq(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// TestDefinitionRegularGlobal checks the full Definition layer against a
// hand-built 72x37 global 5-degree grid (scan mode 0).
func TestDefinitionRegularGlobal(t *testing.T) {
	b := makeSec3(0, func(b []byte) {
		fillBase(b, 90, -180, -90, 175, 5, 5, 72, 37, 0, 0)
	})
	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if g.TemplateNumber() != 0 || g.SourceOfGridDefinition() != 0 || g.NumPointsDeclared() != 0 {
		t.Errorf("metadata = (%d,%d,%d), want (0,0,0)", g.TemplateNumber(), g.SourceOfGridDefinition(), g.NumPointsDeclared())
	}
	if g.ShapeOfEarth() != 6 || g.Radius() != 6371229 {
		t.Errorf("shape/radius = %d/%v, want 6/6371229", g.ShapeOfEarth(), g.Radius())
	}
	if n := g.NumPoints(); n != 72*37 {
		t.Fatalf("NumPoints = %d, want %d", n, 72*37)
	}
	cases := []struct {
		i       int
		lat, ln float64
	}{
		{0, 90, -180},
		{1, 90, -175},
		{71, 90, 175},
		{72, 85, -180},
		{2663, -90, 175},
	}
	for _, c := range cases {
		lat, lon, err := g.Point(c.i)
		if err != nil {
			t.Fatalf("Point(%d): %v", c.i, err)
		}
		if !almostEq(lat, c.lat, 1e-9) || !almostEq(lon, c.ln, 1e-9) {
			t.Errorf("Point(%d) = (%v,%v), want (%v,%v)", c.i, lat, lon, c.lat, c.ln)
		}
	}
	lats, err := g.Lats()
	if err != nil || len(lats) != 37 || lats[0] != 90 || lats[36] != -90 {
		t.Errorf("Lats: %v (err %v), want [90..-90]", lats, err)
	}
	lons, err := g.Lons()
	if err != nil || len(lons) != 72 || lons[0] != -180 || lons[71] != 175 {
		t.Errorf("Lons: first/last = %v/%v (err %v), want -180/175", lons[0], lons[71], err)
	}
}

// TestDefinitionRegularXcheck runs Definition end-to-end against the
// eccodes-generated grid_a/grid_b vectors (the Linux bench host differential harness):
// section 3 bytes are rebuilt from the vector's stored raw microdegree fields
// and every point must match eccodes' own latitude/longitude lists.
func TestDefinitionRegularXcheck(t *testing.T) {
	for _, name := range []string{"grid_a.json", "grid_b.json"} {
		raw, err := os.ReadFile(filepath.Join("..", "testdata", "ecc-xcheck", name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var env struct {
			Extra             map[string]any `json:"extra"`
			EccodesLatitudes  []any          `json:"eccodes_latitudes"`
			EccodesLongitudes []any          `json:"eccodes_longitudes"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		getf := func(k string) uint32 {
			v, ok := env.Extra[k]
			if !ok {
				return 0
			}
			f, ok := v.(float64)
			if !ok {
				t.Fatalf("%s: extra field %q not a number: %T", name, k, v)
			}
			// Reconstruct the original file's signed[4] bytes: sign-magnitude
			// (the convention every real GRIB2 implementation uses; ).
			if f < 0 {
				return 0x80000000 | uint32(-f)
			}
			return uint32(f)
		}
		nii := int(getf("Ni"))
		nji := int(getf("Nj"))
		b := makeSec3(0, func(b []byte) {
			fillBaseRaw(b, getf("latitudeOfFirstGridPoint"), getf("longitudeOfFirstGridPoint"),
				getf("latitudeOfLastGridPoint"), getf("longitudeOfLastGridPoint"),
				getf("iDirectionIncrement"), getf("jDirectionIncrement"),
				uint32(nii), uint32(nji), 0, scanModeFromExtra(env.Extra))
		})
		g, err := New(b)
		if err != nil {
			t.Fatalf("%s: New: %v", name, err)
		}
		n := nii * nji
		if g.NumPoints() != n {
			t.Fatalf("%s: NumPoints = %d, want %d", name, g.NumPoints(), n)
		}
		maxDLat, maxDLon := 0.0, 0.0
		for i := 0; i < n; i++ {
			lat, lon, err := g.Point(i)
			if err != nil {
				t.Fatalf("%s: Point(%d): %v", name, i, err)
			}
			elat := toFloat64(env.EccodesLatitudes[i])
			elon := toFloat64(env.EccodesLongitudes[i])
			if d := math.Abs(lat - elat); d > maxDLat {
				maxDLat = d
			}
			if d := math.Abs(lon - elon); d > maxDLon {
				maxDLon = d
			}
		}
		t.Logf("%s: %d points, max |dLat|=%g deg, max |dLon|=%g deg", name, n, maxDLat, maxDLon)
		if maxDLat > 1e-6 || maxDLon > 1e-6 {
			t.Errorf("%s: point mismatch vs eccodes (max dLat %g, dLon %g)", name, maxDLat, maxDLon)
		}
	}
}

// fillBaseRaw is fillBase with raw microdegree values already scaled.
func fillBaseRaw(b []byte, La1, Lo1, La2, Lo2, Di, Dj uint32, Ni, Nj uint32, resFlags, scanMode byte) {
	b[14] = 6 // shape: sphere R=6371229 (table 3.2)
	putU32(b, 30, Ni)
	putU32(b, 34, Nj)
	putU32(b, 42, missingU32) // subdivisions missing -> unit 1e-6 deg
	putU32(b, 46, La1)
	putU32(b, 50, Lo1)
	b[54] = resFlags
	putU32(b, 55, La2)
	putU32(b, 59, Lo2)
	putU32(b, 63, Di)
	putU32(b, 67, Dj)
	b[71] = scanMode
}

// scanModeFromExtra reconstructs the scan mode byte from eccodes accessor
// flags stored in the vector (iScansNegatively=bit128, jPointsAreConsecutive=bit32).
func scanModeFromExtra(extra map[string]any) byte {
	var sm byte
	if v, ok := extra["iScansNegatively"].(float64); ok && v == 1 {
		sm |= 128
	}
	if v, ok := extra["jPointsAreConsecutive"].(float64); ok && v == 1 {
		sm |= 32
	}
	return sm
}

// TestDefinitionMissingDiDj checks the §5.3 missing-increment default
// algorithm (all-ones raw + resFlags bit).
func TestDefinitionMissingDiDj(t *testing.T) {
	b := makeSec3(0, func(b []byte) {
		fillBase(b, 60, 0, -60, 120, 15, 10, 9, 13, 0, 0)
		putU32(b, 63, missingU32) // Di missing
		putU32(b, 67, missingU32) // Dj missing
		b[54] = 0x0C              // resFlags: bits 3+4 (i and j increments not given)
	})
	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Di = |120-0|/(9-1) = 15; Dj = |-60-60|/(13-1) = 10
	lat, lon, _ := g.Point(0)
	if !almostEq(lat, 60, 1e-9) || !almostEq(lon, 0, 1e-9) {
		t.Errorf("Point(0) = (%v,%v), want (60,0)", lat, lon)
	}
	lat, lon, _ = g.Point(8)
	if !almostEq(lat, 60, 1e-9) || !almostEq(lon, 120, 1e-9) {
		t.Errorf("Point(8) = (%v,%v), want (60,120)", lat, lon)
	}
	lat, _, _ = g.Point(9) // row 1 (j=1): 60 - 10 = 50 (Ni=9 per row)
	if !almostEq(lat, 50, 1e-9) {
		t.Errorf("Point(9).lat = %v, want 50", lat)
	}
}

// TestDefinitionSerpentine checks scan mode bit16 (full serpentine per A's
// ruling on ): odd rows reverse the i direction. BOUNDARY: eccodes 2.49.0
// exposes no sample/vector taking this scan path — 无外部对拍 (no external
// cross-check); expectations derive from the dual-source §5.5 reading.
func TestDefinitionSerpentine(t *testing.T) {
	b := makeSec3(0, func(b []byte) {
		fillBase(b, 45, 0, -45, 90, 45, 45, 3, 3, 0, 16) // scanMode = bit16 only
	})
	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantLons := [][]float64{
		{0, 45, 90}, // row 0: +i
		{90, 45, 0}, // row 1: reversed
		{0, 45, 90}, // row 2: +i again (full serpentine)
	}
	for j := 0; j < 3; j++ {
		for k := 0; k < 3; k++ {
			i := j*3 + k
			_, lon, err := g.Point(i)
			if err != nil {
				t.Fatalf("Point(%d): %v", i, err)
			}
			if !almostEq(lon, wantLons[j][k], 1e-9) {
				t.Errorf("Point(%d).lon = %v, want %v (row %d pos %d)", i, lon, wantLons[j][k], j, k)
			}
		}
	}
}

// TestDefinitionReduced checks quasi-regular grids (resFlags bit1 + point
// list): Ni varies per row, Nj fixed.
func TestDefinitionReduced(t *testing.T) {
	// Nj=3 fixed with La1=60, La2=-60 -> row latitudes 60/0/-60 (Dj derived
	// as |La2-La1|/(Nj-1) = 45). The point list (octets 73+) gives per-row
	// counts [4,3,2].
	b := makeSec3(0, func(b []byte) {
		fillBase(b, 60, 0, -60, 90, 0, 60, missingU32, 3, 1, 0) // Dj=60: rows at 60/0/-60
		b[10] = 1
		b[72], b[73], b[74] = 4, 3, 2 // point list: rows have 4/3/2 points
	})
	// The section ends right after the 3-entry point list (octets 73-75).
	b = b[:75]
	putU32(b, 0, 75)
	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !g.IsQuasiRegular() {
		t.Fatal("IsQuasiRegular = false, want true")
	}
	if n := g.NumPoints(); n != 9 {
		t.Fatalf("NumPoints = %d, want 9", n)
	}
	type pt struct{ lat, lon float64 }
	want := []pt{
		// row 0 (lat 60): 4 points over [0,90] -> spacing 30
		{60, 0}, {60, 30}, {60, 60}, {60, 90},
		// row 1 (lat 0): 3 points over [0,90] -> spacing 45
		{0, 0}, {0, 45}, {0, 90},
		// row 2 (lat -60): 2 points over [0,90]
		{-60, 0}, {-60, 90},
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
	if _, err := g.Lats(); err == nil {
		t.Error("Lats on reduced grid: expected error")
	}
}

// TestDefinitionGaussianXcheck runs the Gaussian Definition end-to-end against
// the eccodes gauss_a vector (T63, 128x64): section 3 bytes rebuilt from the
// stored fields must reproduce eccodes' Gaussian latitudes exactly.
func TestDefinitionGaussianXcheck(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "ecc-xcheck", "gauss_a.json"))
	if err != nil {
		t.Fatalf("read gauss_a: %v", err)
	}
	var env struct {
		Ni                       int   `json:"Ni"`
		Nj                       int   `json:"Nj"`
		EccodesGaussianLatitudes []any `json:"eccodes_gaussian_latitudes"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("parse gauss_a: %v", err)
	}
	La1 := toFloat64(env.EccodesGaussianLatitudes[0])
	La2 := toFloat64(env.EccodesGaussianLatitudes[len(env.EccodesGaussianLatitudes)-1])
	b := makeSec3(40, func(b []byte) {
		fillBase(b, La1, 0, La2, 360-360.0/float64(env.Ni), 360.0/float64(env.Ni), 0, uint32(env.Ni), uint32(env.Nj), 0, 0)
		putU32(b, 67, uint32(env.Nj/2)) // N = Nj/2 for the global grid (order 2N = Nj)
	})
	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if g.TemplateNumber() != 40 || g.NumPoints() != env.Ni*env.Nj {
		t.Fatalf("metadata: tmpl=%d points=%d", g.TemplateNumber(), g.NumPoints())
	}
	maxD := 0.0
	for j := 0; j < env.Nj; j++ {
		lat, _, err := g.Point(j * env.Ni) // first point (i=0) of row j
		if err != nil {
			t.Fatalf("Point(%d): %v", j, err)
		}
		elat := toFloat64(env.EccodesGaussianLatitudes[j])
		if d := math.Abs(lat - elat); d > maxD {
			maxD = d
		}
	}
	t.Logf("gauss_a: max |dLat| vs eccodes = %g deg", maxD)
	if maxD > 1e-9 {
		t.Errorf("Gaussian latitudes differ from eccodes by up to %g deg", maxD)
	}
}

// TestDefinitionGaussianSubArea checks the §5.10 sub-area window: Nj < 2N,
// La1 found in the full list, window stepped in the j scan direction.
// F-A2-2 (): the full parallel list runs north to south, so
// jScansPositively=0 walks index++ (southward) and =1 walks index-- (northward)
// — matching eccodes Gaussian.cc. Both directions are pinned here.
func TestDefinitionGaussianSubArea(t *testing.T) {
	// Full T31-like grid: N=16 -> 32 parallels; take a 4-row window at row 4.
	full := make([]float64, 32)
	colats, err := gaussianColatitudes(32)
	if err != nil {
		t.Fatal(err)
	}
	const deg = 180.0 / math.Pi
	for i, th := range colats {
		full[i] = 90 - th*deg
	}

	run := func(name string, scanMode byte, want []float64) {
		t.Run(name, func(t *testing.T) {
			La1 := full[4]
			b := makeSec3(40, func(b []byte) {
				fillBase(b, La1, 0, want[len(want)-1], 360-360/24, 360.0/24, 0, 24, 4, 0, scanMode)
				putU32(b, 67, 16) // N=16 -> 32 parallels total
			})
			g, err := New(b)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			for j := 0; j < 4; j++ {
				lat, _, err := g.Point(j * 24) // first point (i=0) of row j; Ni=24
				if err != nil {
					t.Fatalf("Point(%d): %v", j*24, err)
				}
				if !almostEq(lat, want[j], 1e-9) {
					t.Errorf("row %d lat = %v, want %v", j, lat, want[j])
				}
			}
		})
	}

	run("jScansPositively=0 southward (rows 4..7)", 0, []float64{full[4], full[5], full[6], full[7]})
	run("jScansPositively=1 northward (rows 4..1)", 64, []float64{full[4], full[3], full[2], full[1]})
}

// TestDefinitionGaussianSubAreaMissingLa1 checks fail-loud when La1 is not a
// Gaussian parallel.
func TestDefinitionGaussianSubAreaMissingLa1(t *testing.T) {
	b := makeSec3(40, func(b []byte) {
		fillBase(b, 42.123456, 0, 30, 360-15, 15, 0, 24, 4, 0, 0) // La1 not a P_32 root latitude
		putU32(b, 67, 16)
	})
	if _, err := New(b); err == nil {
		t.Fatal("New: expected error for non-Gaussian La1")
	}
}

// TestDefinitionGaussianSpanContradiction pins the A-B5 TRUE-POSITIVE that
// must survive the F-A2-1 tolerance widening (M constraint, ): a
// Gaussian grid whose stored Di contradicts the Lo1/Lo2 span by far more than
// the quantization bound ((Ni-1)*5e-4 deg) is still rejected fail-loud.
func TestDefinitionGaussianSpanContradiction(t *testing.T) {
	// Ni=24, span |Lo2-Lo1| = 345 deg; stored Di = 16 deg -> |Ni-1|*Di = 384,
	// off by 39 deg (wrap alternative 15 also off by 369). Bound: 23*5e-4.
	b := makeSec3(40, func(b []byte) {
		fillBase(b, 87.863799, 0, -87.863799, 345, 16, 0, 24, 64, 0, 0)
		putU32(b, 67, 32) // N=32 -> 64 parallels (Nj=64 = full grid)
	})
	_, err := New(b)
	if err == nil {
		t.Fatal("New: expected span-contradiction rejection (true positive must survive F-A2-1)")
	}
	if !strings.Contains(err.Error(), "contradicts Lo1/Lo2 span") {
		t.Fatalf("error = %v, want the span-check message", err)
	}
}

// TestDefinitionGaussianWindowOverrun pins fail-loud when a sub-area window
// would overrun the parallel list. (eccodes Gaussian.cc wraps at the
// boundary; we refuse to guess — documented difference, internal finding F-A2-2.)
func TestDefinitionGaussianWindowOverrun(t *testing.T) {
	full := make([]float64, 32)
	colats, err := gaussianColatitudes(32)
	if err != nil {
		t.Fatal(err)
	}
	const deg = 180.0 / math.Pi
	for i, th := range colats {
		full[i] = 90 - th*deg
	}
	// La1 = full[2], Nj=30, jScansPositively=1 (northward): rows 2..-27 -> overrun.
	b := makeSec3(40, func(b []byte) {
		fillBase(b, full[2], 0, full[31], 360-360/24, 360.0/24, 0, 24, 30, 0, 64)
		putU32(b, 67, 16)
	})
	if _, err := New(b); err == nil {
		t.Fatal("New: expected window-overrun rejection")
	}
}

// TestDefinitionRotatedRoundTrip checks template 3.1 against the exact
// eccodes unrotate formula (§5.8): rotating a point back must recover the
// unrotated regular-grid coordinate.
func TestDefinitionRotatedRoundTrip(t *testing.T) {
	var spLat, spLon, angle = -60.0, 25.0, 17.5 // vars: negative angles need two's-complement uint32 encoding
	b := makeSec3(1, func(b []byte) {
		fillBase(b, 90, -180, -90, 175, 5, 5, 72, 37, 0, 0)
		putSmag(b, 72, int32(spLat*1e6))                // octets 73-76: southern pole lat (sign-magnitude)
		putSmag(b, 76, int32(spLon*1e6))                // octets 77-80: southern pole lon
		putU32(b, 80, math.Float32bits(float32(angle))) // octets 81-84: rotation angle (F32 degrees)
	})
	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	n := g.NumPoints()
	maxDLat, maxDLon := 0.0, 0.0
	for i := 0; i < n; i++ {
		lat, lon, err := g.Point(i)
		if err != nil {
			t.Fatalf("Point(%d): %v", i, err)
		}
		backLat, backLon := rotateFrame(lat, lon, angle, spLat, spLon)
		j, k := i/72, i%72
		wantLat := 90 - float64(j)*5
		wantLon := -180 + float64(k)*5
		if d := math.Abs(backLat - wantLat); d > maxDLat {
			maxDLat = d
		}
		// At the poles the inverse-rotated longitude is indeterminate (all
		// pole longitudes map to one point), so compare latitude only there.
		if math.Abs(wantLat) < 89.999 {
			if d := math.Abs(backLon - wantLon); d > maxDLon {
				maxDLon = d
			}
		}
	}
	t.Logf("rotated round trip: max dLat=%g dLon=%g over %d points", maxDLat, maxDLon, n)
	if maxDLat > 1e-9 || maxDLon > 1e-9 {
		t.Errorf("round trip failed (max dLat %g, dLon %g)", maxDLat, maxDLon)
	}
}

// TestDefinitionStretched checks template 3.2: the i dimension follows the
// §5.9 geometric series with r = raw/1e6; r==1 must degenerate to equal
// spacing. BOUNDARY: eccodes 2.49.0 has no stretched-latlon iterator —
// 无外部对拍 (no external cross-check); expectations derive from the §5.9
// formula dual-source reading (WMO note + contract).
func TestDefinitionStretched(t *testing.T) {
	b := makeSec3(2, func(b []byte) {
		fillBase(b, 45, 0, -45, 90, 15, 45, 7, 3, 0, 0)
		putU32(b, 72, uint32(45e6))     // pole of stretching lat (metadata)
		putU32(b, 76, uint32(0))        // pole of stretching lon (metadata)
		putU32(b, 80, uint32(1.05*1e6)) // stretching factor 1.05 (scaled by 1e6)
	})
	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r := 1.05
	for k := 0; k < 7; k++ {
		want := 0 + 15*(math.Pow(r, float64(k))-1)/(r-1) // x_k = Lo1 + Di*(r^k-1)/(r-1)
		_, lon, err := g.Point(k)
		if err != nil {
			t.Fatalf("Point(%d): %v", k, err)
		}
		if !almostEq(lon, want, 1e-9) {
			t.Errorf("Point(%d).lon = %v, want %v", k, lon, want)
		}
	}

	// r == 1 -> equal spacing
	b2 := makeSec3(2, func(b []byte) {
		fillBase(b, 45, 0, -45, 90, 15, 45, 7, 3, 0, 0)
		putU32(b, 80, uint32(1e6)) // r = 1.0
	})
	g2, err := New(b2)
	if err != nil {
		t.Fatalf("New(r=1): %v", err)
	}
	for k := 0; k < 7; k++ {
		_, lon, _ := g2.Point(k)
		if !almostEq(lon, float64(k)*15, 1e-9) {
			t.Errorf("r=1 Point(%d).lon = %v, want %v", k, lon, float64(k)*15)
		}
	}
}

// TestDefinitionStretchedRotated checks template 3.3 (stretched AND rotated
// lat/lon): coordinates must be built by applying the §5.9 geometric series
// in the BASE frame first, then the §5.8 rotation — order of operations plus
// the template-3 field offsets (rotation octets 73-84, stretch octets 85-96).
// BOUNDARY: eccodes 2.49.0 exposes no stretched-latlon iterator, so this
// combined path has 无外部对拍 (no external cross-check); the rotation
// transform itself is covered externally by the 3.1 eccodes xcheck
// (rotated_a vector) and TestDefinitionRotatedRoundTrip. The base-frame lon
// literals are hand-computed from x_k = Lo1 + Di(r^k-1)/(r-1), Lo1=0 Di=15
// r=1.05.
func TestDefinitionStretchedRotated(t *testing.T) {
	var spLat, spLon, angle = -60.0, 25.0, 17.5 // negative values need two's-complement encoding
	var stretchPoleLat = -90.0                  // runtime var: negative constant -> uint32 overflow at compile time
	r := 1.05
	b := makeSec3(3, func(b []byte) {
		fillBase(b, 45, 0, -45, 90, 15, 45, 7, 3, 0, 0) // Ni=7 Nj=3, scan0 (i-fastest, -j)
		putSmag(b, 72, int32(spLat*1e6))                // octets 73-76: southern pole of rotation
		putSmag(b, 76, int32(spLon*1e6))                // octets 77-80
		putU32(b, 80, math.Float32bits(float32(angle))) // octets 81-84: rotation angle F32 degrees
		putSmag(b, 84, int32(stretchPoleLat*1e6))       // octets 85-88: stretch pole lat (metadata)
		putU32(b, 88, uint32(0))                        // octets 89-92: stretch pole lon (metadata)
		putU32(b, 92, uint32(r*1e6))                    // octets 93-96: stretch factor scaled by 1e6
	})
	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantBaseLon := []float64{0, 15, 30.75, 47.2875, 64.651875, 82.88446875, 102.0286921875}
	wantRowLat := []float64{45, 0, -45}
	rotated := false
	for j := 0; j < 3; j++ {
		for k := 0; k < 7; k++ {
			lat, lon, err := g.Point(j*7 + k)
			if err != nil {
				t.Fatalf("Point(%d): %v", j*7+k, err)
			}
			// Undo the rotation: the pre-rotation frame must be the §5.9
			// stretch series along i x the regular row lattice along j.
			bLat, bLon := rotateFrame(lat, lon, angle, spLat, spLon)
			if !almostEq(bLat, wantRowLat[j], 1e-9) {
				t.Errorf("(%d,%d): base lat = %v, want %v", j, k, bLat, wantRowLat[j])
			}
			if !almostEq(bLon, wantBaseLon[k], 1e-9) {
				t.Errorf("(%d,%d): base lon = %v, want %v (§5.9)", j, k, bLon, wantBaseLon[k])
			}
			if math.Abs(lat-wantRowLat[j]) > 1e-6 || math.Abs(lon-wantBaseLon[k]) > 1e-6 {
				rotated = true // the rotation actually moved the point
			}
		}
	}
	if !rotated {
		t.Error("rotation had no effect: angle=17.5 should move points — order-of-operations check is vacuous")
	}
}

// TestDefinitionReducedUnverifiedGuard checks (, adopted by
// B): quasi-regular grids combined with rotation (3.1) or stretching (3.2)
// must parse metadata normally but fail loud at coordinate time — the
// uniform reduced base frame cannot represent the §5.9 series, and no
// eccodes vector verifies the rotated combination. BOUNDARY: 无外部对拍 by
// construction — this test pins the fail-loud contract itself.
func TestDefinitionReducedUnverifiedGuard(t *testing.T) {
	mk := func(tmpl uint16, fill func(b []byte)) []byte {
		b := makeSec3(tmpl, func(b []byte) {
			fillBase(b, 60, 0, -60, 90, 0, 60, missingU32, 3, 1, 0) // Ni missing -> quasi
			b[10] = 1                                               // point-list element width
			fill(b)
			b[84], b[85], b[86] = 4, 3, 2 // point list (octets 85+, Nj=3 entries)
		})
		b = b[:87] // section ends right after the 3-entry point list
		putU32(b, 0, 87)
		return b
	}
	var rotSpLat, rotSpLon = -60.0, 25.0 // runtime vars: negative constants overflow uint32
	var stretchPole = -90.0
	rotated := mk(1, func(b []byte) {
		putSmag(b, 72, int32(rotSpLat*1e6))            // octets 73-76: south pole lat
		putSmag(b, 76, int32(rotSpLon*1e6))            // octets 77-80: south pole lon
		putU32(b, 80, math.Float32bits(float32(17.5))) // octets 81-84: rotation angle
	})
	stretched := mk(2, func(b []byte) {
		putSmag(b, 72, int32(stretchPole*1e6)) // stretch pole lat (metadata)
		putU32(b, 76, uint32(0))               // stretch pole lon (metadata)
		putU32(b, 80, uint32(1.05*1e6))        // stretch factor
	})
	for _, tc := range []struct {
		name string
		b    []byte
	}{
		{"reduced+rotated (3.1)", rotated},
		{"reduced+stretched (3.2)", stretched},
	} {
		g, err := New(tc.b)
		if err != nil {
			t.Fatalf("%s: New: %v (metadata should parse)", tc.name, err)
		}
		if !g.IsQuasiRegular() {
			t.Errorf("%s: IsQuasiRegular = false, want true", tc.name)
		}
		if g.NumPoints() != 9 {
			t.Errorf("%s: NumPoints = %d, want 9 (metadata readable)", tc.name, g.NumPoints())
		}
		if _, _, err := g.Point(0); err == nil {
			t.Errorf("%s: Point: expected fail-loud, got success", tc.name)
		} else if !strings.Contains(err.Error(), "not verified in P1") {
			t.Errorf("%s: Point error = %v, want 'not verified in P1' guard", tc.name, err)
		}
	}
}

// TestDefinitionUnimplemented checks that unknown templates parse metadata
// but fail loudly on coordinates .
func TestDefinitionUnimplemented(t *testing.T) {
	b := makeSec3(10, nil) // template 3.10 Mercator: spec def exists, coords not implemented
	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if g.TemplateNumber() != 10 {
		t.Errorf("TemplateNumber = %d, want 10", g.TemplateNumber())
	}
	if g.Walk() == nil {
		t.Error("Walk = nil, want non-nil (spec has a def for 3.10)")
	}
	if _, _, err := g.Point(0); err == nil {
		t.Fatal("Point: expected ErrNotImplemented")
	} else {
		var eni *ErrNotImplemented
		if !errors.As(err, &eni) || eni.Template != 10 {
			t.Errorf("Point error = %v, want ErrNotImplemented{10}", err)
		}
	}
	if n := g.NumPoints(); n != 0 {
		t.Errorf("NumPoints = %d, want 0", n)
	}
}

// TestDefinitionGaussianFamilyNumbers checks the A-B4 ruling: Gaussian grids
// store 40-43 in octets 13-14 (40/41 implemented in P1; 42/43 metadata only);
// 340 is NOT a valid value and must not be aliased.
func TestDefinitionGaussianFamilyNumbers(t *testing.T) {
	fill := func(b []byte) {
		fillBase(b, 89.45, 0, -89.45, 360-360/128, 360.0/128, 0, 128, 64, 0, 0)
		putU32(b, 67, 32) // N=32 -> P_64
	}
	for _, tmpl := range []uint16{42, 43, 340} {
		g, err := New(makeSec3(tmpl, fill))
		if err != nil {
			t.Fatalf("New(%d): %v", tmpl, err)
		}
		if g.TemplateNumber() != int(tmpl) {
			t.Errorf("tmpl %d: TemplateNumber = %d", tmpl, g.TemplateNumber())
		}
		if n := g.NumPoints(); n != 0 {
			t.Errorf("tmpl %d: NumPoints = %d, want 0 (metadata only)", tmpl, n)
		}
		if _, _, err := g.Point(0); err == nil {
			t.Errorf("tmpl %d: Point: expected ErrNotImplemented", tmpl)
		} else {
			var eni *ErrNotImplemented
			if !errors.As(err, &eni) || eni.Template != int(tmpl) {
				t.Errorf("tmpl %d: Point error = %v, want ErrNotImplemented{%d}", tmpl, err, tmpl)
			}
		}
	}
}

// TestDefinitionDiContradiction checks the A-B5 fail-loud: a stored Di that
// contradicts the Lo1/Lo2 span (beyond 1e-6 deg, including the 360-wrap
// alternative) must be rejected.
func TestDefinitionDiContradiction(t *testing.T) {
	// Ni=72, Di=5 -> span must be 355 (or wrapped 5); Lo2=180 gives |Lo2-Lo1|=360.
	b := makeSec3(0, func(b []byte) {
		fillBase(b, 90, -180, -90, 180, 5, 5, 72, 37, 0, 0)
	})
	if _, err := New(b); err == nil {
		t.Fatal("New: expected Di/Lo2 contradiction error")
	}

	// The wrapped alternative must pass: Lo1=170 eastward with Di=5, Ni=5
	// ends at -170 (via 180); unwrapped span |Lo2-Lo1|=340, wrapped 20 = want.
	b2 := makeSec3(0, func(b []byte) {
		fillBase(b, 90, 170, -90, -170, 5, 5, 5, 37, 0, 0)
	})
	g, err := New(b2)
	if err != nil {
		t.Fatalf("New (wrap-consistent): %v", err)
	}
	if g.NumPoints() != 5*37 {
		t.Errorf("NumPoints = %d, want %d", g.NumPoints(), 5*37)
	}

	// Dj contradiction: Nj=37, Dj=5 -> span must be 180; La2=-80 gives 170.
	b3 := makeSec3(0, func(b []byte) {
		fillBase(b, 90, -180, -80, 175, 5, 5, 72, 37, 0, 0)
	})
	if _, err := New(b3); err == nil {
		t.Fatal("New: expected Dj/La2 contradiction error")
	}
}

// toFloat64 converts a JSON-decoded number (or "Inf"/"-Inf"/"NaN" string from
// the vector generator) to float64.
func toFloat64(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		switch x {
		case "Inf":
			return math.Inf(1)
		case "-Inf":
			return math.Inf(-1)
		case "NaN":
			return math.NaN()
		default:
			panic("unexpected string value")
		}
	default:
		panic("unexpected type")
	}
}

// TestDefinitionRotatedXcheck runs Definition end-to-end against the
// eccodes-generated rotated_a vector: the raw section 3 bytes captured from
// an eccodes-encoded message (spLat=-60, spLon=25, angle=17.5) are parsed and
// every point compared with eccodes' own unrotated latitude/longitude lists.
// This is the binary differential for the unrotate path (previously verified
// by round-trip only).
func TestDefinitionRotatedXcheck(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "ecc-xcheck", "rotated_a.json"))
	if err != nil {
		t.Fatalf("rotated_a.json: %v", err)
	}
	var env struct {
		Ni             int       `json:"Ni"`
		Nj             int       `json:"Nj"`
		NumberOfPoints int       `json:"numberOfPoints"`
		Section3Hex    string    `json:"section3_hex"`
		Lats           []float64 `json:"lats"`
		Lons           []float64 `json:"lons"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("rotated_a.json: %v", err)
	}
	sec3, err := hex.DecodeString(env.Section3Hex)
	if err != nil {
		t.Fatalf("rotated_a.json: bad section3_hex: %v", err)
	}
	g, err := New(sec3)
	if err != nil {
		t.Fatalf("New over eccodes section 3: %v", err)
	}
	if g.TemplateNumber() != 1 {
		t.Errorf("TemplateNumber = %d, want 1 (rotated lat/lon)", g.TemplateNumber())
	}
	n := env.Ni * env.Nj
	if n != env.NumberOfPoints || len(env.Lats) != n || len(env.Lons) != n {
		t.Fatalf("vector inconsistency: Ni*Nj=%d numberOfPoints=%d lats=%d", n, env.NumberOfPoints, len(env.Lats))
	}
	if g.NumPoints() != n {
		t.Fatalf("NumPoints = %d, want %d", g.NumPoints(), n)
	}
	// Tolerance: eccodes "rounds to 6 decimals" with roundf() (FLOAT), so the
	// effective granularity is the float32 ulp of value*1e6 — up to ~1.6e-5 deg
	// at |lon|~150-180 (ulp 16) and ~4e-6 at |lat|~90 (ulp 8). Full-precision
	// output is what we produce; 1e-5 deg covers the worst case with margin.
	const tol = 1e-5
	maxDLat, maxDLon := 0.0, 0.0
	for i := 0; i < n; i++ {
		lat, lon, err := g.Point(i)
		if err != nil {
			t.Fatalf("Point(%d): %v", i, err)
		}
		if d := math.Abs(lat - env.Lats[i]); d > maxDLat {
			maxDLat = d
		}
		// eccodes does not normalize longitudes (e.g. -197.5); compare with
		// the wrap-around distance.
		dlon := math.Mod(math.Abs(lon-env.Lons[i]), 360)
		if dlon > 180 {
			dlon = 360 - dlon
		}
		if dlon > maxDLon {
			maxDLon = dlon
		}
	}
	t.Logf("rotated_a: %d points, max |dLat|=%g deg, max wrap|dLon|=%g deg", n, maxDLat, maxDLon)
	if maxDLat > tol || maxDLon > tol {
		t.Errorf("rotated point mismatch vs eccodes (max dLat %g, dLon %g, tol %g)", maxDLat, maxDLon, tol)
	}
}
