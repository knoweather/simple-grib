package grid

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- synthetic reduced Gaussian sections ----------------------------------
//
// Field values follow what ecCodes reports for the real ECMWF regional
// reduced Gaussian products that motivated this work
// (D3O09240000092401001: gridType=reduced_gg, template 40, Ni=2147483647
// missing, Nj=626, N=1280, numberOfPoints=62644, corners
// 24.991213/110.0 -> -18.945519/118.0) and for D8D09240000092400011
// (Nj=782, numberOfPoints=707641).

// reducedGaussSection builds a synthetic GRIB2 section 3 for templates
// 3.40/3.41 with Ni or Nj encoded as missing. diRaw is written to octets
// 64-67: real reduced_gg files leave the i increment missing once Ni is
// missing, so missingU32 is the realistic choice.
func reducedGaussSection(tmpl uint16, ni, nj, n uint32, la1, lo1, la2, lo2 float64, diRaw, declared uint32, scanMode byte) []byte {
	return makeSec3(tmpl, func(b []byte) {
		putU32(b, 6, declared)    // octets 7-10: numberOfDataPoints
		b[14] = 6                 // octet 15: shape = sphere R=6371229
		putU32(b, 30, ni)         // octets 31-34
		putU32(b, 34, nj)         // octets 35-38
		putU32(b, 42, missingU32) // subdivisions missing -> microdegrees
		putSmag(b, 46, int32(la1*1e6))
		putSmag(b, 50, int32(lo1*1e6))
		b[54] = 0
		putSmag(b, 55, int32(la2*1e6))
		putSmag(b, 59, int32(lo2*1e6))
		putU32(b, 63, diRaw) // octets 64-67: Di
		putU32(b, 67, n)     // octets 68-71: N on 3.40/3.41 (never Dj)
		b[71] = scanMode
	})
}

// wantNotImplemented asserts that err is ErrNotImplemented{Template: tmpl}.
func wantNotImplemented(t *testing.T, what string, err error, tmpl int) {
	t.Helper()
	var eni *ErrNotImplemented
	if !errors.As(err, &eni) {
		t.Errorf("%s: err = %v, want ErrNotImplemented{%d}", what, err, tmpl)
		return
	}
	if eni.Template != tmpl {
		t.Errorf("%s: ErrNotImplemented.Template = %d, want %d", what, eni.Template, tmpl)
	}
}

// TestReducedGaussianMetadataOnly pins the plan-B contract for a regional
// reduced Gaussian grid (template 3.40, Ni missing). Such a definition must
// parse — otherwise grib.Open/M.Grid() lose every message of a real ECMWF
// reduced_gg file — while the coordinates stay withheld (kindNone) and the
// metadata (corners, N, declared point count) stays readable.
func TestReducedGaussianMetadataOnly(t *testing.T) {
	const declared = 62644
	b := reducedGaussSection(40, missingU32, 626, 1280,
		24.991213, 110.0, -18.945519, 118.0, missingU32, declared, 0)
	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v (metadata-only reduced Gaussian must not fail)", err)
	}
	if g.TemplateNumber() != 40 {
		t.Errorf("TemplateNumber = %d, want 40", g.TemplateNumber())
	}
	// kindNone reads octets 31-34/35-38 verbatim: a missing Ni decodes to 0,
	// Nj keeps its encoded value.
	if g.Ni() != 0 || g.Nj() != 626 {
		t.Errorf("Ni/Nj = %d/%d, want 0/626", g.Ni(), g.Nj())
	}
	// Convention for metadata-only grids: NumPoints() is 0 (no table can be
	// built) while NumPointsDeclared() is the encoded count — what callers
	// must use for the grid size.
	if n := g.NumPoints(); n != 0 {
		t.Errorf("NumPoints = %d, want 0 (coordinates withheld)", n)
	}
	if n := g.NumPointsDeclared(); n != declared {
		t.Errorf("NumPointsDeclared = %d, want %d", n, declared)
	}
	if !g.IsReducedGaussian() {
		t.Error("IsReducedGaussian = false, want true")
	}
	if g.IsQuasiRegular() {
		t.Error("IsQuasiRegular = true, want false (that flag is the enumerated point-list path)")
	}
	if n := g.GaussianN(); n != 1280 {
		t.Errorf("GaussianN = %d, want 1280 (octets 68-71)", n)
	}

	// Bounds is the four encoded corners, exactly as decoded at the file's
	// angle resolution (microdegrees here); ok must be true.
	lat1, lon1, lat2, lon2, ok := g.Bounds()
	if !ok {
		t.Fatal("Bounds ok = false, want true")
	}
	if lat1 != 24.991213 || lon1 != 110.0 || lat2 != -18.945519 || lon2 != 118.0 {
		t.Errorf("Bounds = (%v, %v, %v, %v), want (24.991213, 110, -18.945519, 118)", lat1, lon1, lat2, lon2)
	}

	// Coordinates: ErrNotImplemented for Point, Lats and Lons alike.
	if _, _, err := g.Point(0); err != nil {
		wantNotImplemented(t, "Point(0)", err, 40)
	} else {
		t.Error("Point(0): expected ErrNotImplemented, got success")
	}
	if _, err := g.Lats(); err != nil {
		wantNotImplemented(t, "Lats()", err, 40)
	} else {
		t.Error("Lats(): expected ErrNotImplemented, got success")
	}
	if _, err := g.Lons(); err != nil {
		wantNotImplemented(t, "Lons()", err, 40)
	} else {
		t.Error("Lons(): expected ErrNotImplemented, got success")
	}
}

// TestReducedGaussianNMissing covers a malformed / minimal file where the N
// field itself is missing: it must still parse (metadata fallback is
// unconditional) and GaussianN must report 0 ("not usable") rather than a
// sentinel-derived value.
func TestReducedGaussianNMissing(t *testing.T) {
	b := reducedGaussSection(40, missingU32, 626, missingU32,
		24.991213, 110.0, -18.945519, 118.0, missingU32, 62644, 0)
	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v (N missing must not fail the metadata path)", err)
	}
	if n := g.GaussianN(); n != 0 {
		t.Errorf("GaussianN = %d, want 0 (octets 68-71 missing)", n)
	}
	if !g.IsReducedGaussian() {
		t.Error("IsReducedGaussian = false, want true")
	}
	if g.NumPoints() != 0 || g.NumPointsDeclared() != 62644 || g.Nj() != 626 {
		t.Errorf("counts = (%d, %d, Nj=%d), want (0, 62644, 626)", g.NumPoints(), g.NumPointsDeclared(), g.Nj())
	}
	if _, _, _, _, ok := g.Bounds(); !ok {
		t.Error("Bounds ok = false, want true (corners are independent of N)")
	}
	if _, _, err := g.Point(0); err != nil {
		wantNotImplemented(t, "Point(0)", err, 40)
	} else {
		t.Error("Point(0): expected ErrNotImplemented")
	}
}

// TestReducedGaussianLa1OffParallel pins the deliberate asymmetry that makes
// the metadata fallback unconditional: a reduced Gaussian grid whose La1 does
// not sit exactly on a Gaussian parallel still parses. Real producers store
// the corners rounded to the file's angle unit, so a window lookup with
// resolveGaussianWindow's 1e-6 deg tolerance can fail on a perfectly readable
// file — and geometry this package never enumerates must not gate metadata.
// The same corner on an enumerated Gaussian grid (Ni present) stays
// fail-loud, pinned at the end of this test and by
// TestDefinitionGaussianSubAreaMissingLa1.
func TestReducedGaussianLa1OffParallel(t *testing.T) {
	const offParallel = 42.123456 // not a root of P_32

	b := reducedGaussSection(40, missingU32, 4, 16,
		offParallel, 0, 30, 360-15, missingU32, 9, 0)
	g, err := New(b)
	if err != nil {
		t.Fatalf("New (metadata-only): %v, want success (no window check on the metadata path)", err)
	}
	if g.GaussianN() != 16 || !g.IsReducedGaussian() || g.NumPoints() != 0 {
		t.Errorf("metadata = (N=%d, reduced=%v, points=%d), want (16, true, 0)",
			g.GaussianN(), g.IsReducedGaussian(), g.NumPoints())
	}
	lat1, _, _, _, ok := g.Bounds()
	if !ok || lat1 != offParallel {
		t.Errorf("Bounds = (%v, ok=%v), want (%v, true)", lat1, ok, offParallel)
	}
	if _, _, err := g.Point(0); err != nil {
		wantNotImplemented(t, "Point(0)", err, 40)
	} else {
		t.Error("Point(0): expected ErrNotImplemented")
	}

	// Ni present -> kindGaussian: the Gaussian window check must still reject
	// a La1 that is not a parallel (unchanged fail-loud behavior).
	b2 := reducedGaussSection(40, 24, 4, 16, offParallel, 0, 30, 360-15, 15_000_000, 9, 0)
	_, err = New(b2)
	if err == nil {
		t.Fatal("New (Ni present): expected the La1-not-a-parallel rejection to survive")
	}
	if !strings.Contains(err.Error(), "not found among") {
		t.Errorf("New (Ni present): err = %v, want the Gaussian sub-area window rejection", err)
	}
}

// TestReducedGaussianRotatedTemplate41 checks the rotated variant: 3.41 with
// Ni missing is metadata only exactly like 3.40 (the ecCodes Gaussian
// iterator never unrotates Gaussian coordinates, so the rotation fields are
// metadata on this path too).
func TestReducedGaussianRotatedTemplate41(t *testing.T) {
	b := reducedGaussSection(41, missingU32, 626, 1280,
		24.991213, 110.0, -18.945519, 118.0, missingU32, 707641, 0)
	// Rotation suffix (octets 73-84): identity pole/angle.
	putSmag(b, 72, 0)
	putSmag(b, 76, 0)
	putU32(b, 80, 0)

	g, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if g.TemplateNumber() != 41 {
		t.Errorf("TemplateNumber = %d, want 41", g.TemplateNumber())
	}
	if !g.IsReducedGaussian() || g.IsQuasiRegular() {
		t.Errorf("reduced/quasi = %v/%v, want true/false", g.IsReducedGaussian(), g.IsQuasiRegular())
	}
	if g.GaussianN() != 1280 {
		t.Errorf("GaussianN = %d, want 1280", g.GaussianN())
	}
	if g.NumPoints() != 0 || g.NumPointsDeclared() != 707641 {
		t.Errorf("counts = (%d, %d), want (0, 707641)", g.NumPoints(), g.NumPointsDeclared())
	}
	lat1, lon1, lat2, lon2, ok := g.Bounds()
	if !ok || lat1 != 24.991213 || lon1 != 110.0 || lat2 != -18.945519 || lon2 != 118.0 {
		t.Errorf("Bounds = (%v, %v, %v, %v, ok=%v), want the encoded corners", lat1, lon1, lat2, lon2, ok)
	}
	if _, _, err := g.Point(0); err != nil {
		wantNotImplemented(t, "Point(0)", err, 41)
	} else {
		t.Error("Point(0): expected ErrNotImplemented")
	}
}

// TestReducedGaussianNonGaussianQuasiUnchanged makes sure the new
// reduced-Gaussian handling captures only templates 3.40/3.41: the
// quasi-regular latitude/longitude templates 3.0/3.1 keep their enumerated
// point-list path (kindReduced) and never report IsReducedGaussian.
func TestReducedGaussianNonGaussianQuasiUnchanged(t *testing.T) {
	mk := func(tmpl uint16, fill func(b []byte)) []byte {
		b := makeSec3(tmpl, func(b []byte) {
			fillBase(b, 60, 0, -60, 90, 0, 60, missingU32, 3, 1, 0) // Ni missing -> quasi
			b[10] = 1                                               // point-list element width
			if fill != nil {
				fill(b)
			}
		})
		if tmpl == 0 {
			b[72], b[73], b[74] = 4, 3, 2 // point list (octets 73+), Nj=3 entries
			b = b[:75]
		} else {
			putSmag(b, 72, 0) // rotation suffix (octets 73-84)
			putSmag(b, 76, 0)
			putU32(b, 80, 0)
			b[84], b[85], b[86] = 4, 3, 2 // point list (octets 85+)
			b = b[:87]
		}
		putU32(b, 0, uint32(len(b)))
		return b
	}

	t.Run("3.0", func(t *testing.T) {
		g, err := New(mk(0, nil))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if g.kind != kindReduced || !g.IsQuasiRegular() {
			t.Errorf("kind/quasi = %v/%v, want kindReduced/true", g.kind, g.IsQuasiRegular())
		}
		if g.IsReducedGaussian() {
			t.Error("IsReducedGaussian = true, want false for template 3.0")
		}
		if g.GaussianN() != 0 || g.NumPoints() != 9 {
			t.Errorf("N/points = %d/%d, want 0/9", g.GaussianN(), g.NumPoints())
		}
		if _, _, _, _, ok := g.Bounds(); !ok {
			t.Error("Bounds ok = false, want true (3.0 encodes corners)")
		}
		if lat, _, err := g.Point(0); err != nil || !almostEq(lat, 60, 1e-12) {
			t.Errorf("Point(0) = (%v, %v), want (60, nil)", lat, err)
		}
	})

	t.Run("3.1", func(t *testing.T) {
		g, err := New(mk(1, nil))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if g.kind != kindReduced || !g.IsQuasiRegular() || g.IsReducedGaussian() {
			t.Errorf("kind/quasi/reduced = %v/%v/%v, want kindReduced/true/false", g.kind, g.IsQuasiRegular(), g.IsReducedGaussian())
		}
		if g.NumPoints() != 9 {
			t.Errorf("NumPoints = %d, want 9", g.NumPoints())
		}
		// The pre-existing fail-loud guard for reduced+rotated coordinates.
		if _, _, err := g.Point(0); err == nil || !strings.Contains(err.Error(), "coordinates not implemented") {
			t.Errorf("Point(0) err = %v, want the 'coordinates not implemented' guard", err)
		}
	})
}

// TestReducedGaussianRegularGaussianUnchanged pins the enumerated Gaussian
// path (Ni and Nj present) against the change: N validation, the Nj <= 2N
// check and GaussianN must behave exactly as before.
func TestReducedGaussianRegularGaussianUnchanged(t *testing.T) {
	// N=2 -> 4 parallels; the outermost are +/-59.44440828916676 (the same
	// values grib1_test uses for the GRIB1 type-4 case).
	const gaussLatN2 = 59.44440828916676
	mk := func(ni, nj, n uint32) []byte {
		return reducedGaussSection(40, ni, nj, n, gaussLatN2, 0, -gaussLatN2, 315, 45_000_000, 8*4, 0)
	}

	g, err := New(mk(8, 4, 2))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if g.GaussianN() != 2 || g.NumPoints() != 32 || g.IsReducedGaussian() {
		t.Errorf("(N, points, reduced) = (%d, %d, %v), want (2, 32, false)", g.GaussianN(), g.NumPoints(), g.IsReducedGaussian())
	}
	if lat1, lon1, lat2, lon2, ok := g.Bounds(); !ok || !almostEq(lat1, gaussLatN2, 1e-6) || lon1 != 0 || !almostEq(lat2, -gaussLatN2, 1e-6) || lon2 != 315 {
		// The corners are compared at the file's microdegree resolution: the
		// synthetic section encodes 59.444408 while the parallel's exact value
		// is 59.44440828916676. Bounds reports what is encoded.
		t.Errorf("Bounds = (%v, %v, %v, %v, %v)", lat1, lon1, lat2, lon2, ok)
	}
	if lat, _, err := g.Point(0); err != nil || !almostEq(lat, gaussLatN2, 1e-9) {
		t.Errorf("Point(0) = (%v, %v), want lat %v", lat, err, gaussLatN2)
	}

	// N missing or zero -> still fail loud (Note 87).
	if _, err := New(mk(8, 4, missingU32)); err == nil {
		t.Error("New (N missing): expected the Note-87 rejection to survive")
	}
	// Nj > 2N -> still fail loud.
	if _, err := New(mk(8, 8, 2)); err == nil {
		t.Error("New (Nj>2N): expected the total-parallels rejection to survive")
	}
}

// TestBoundsAvailability pins the ok=false contract of Bounds for templates
// that carry no corner fields, and ok=true for the latitude/longitude family
// in both editions.
func TestBoundsAvailability(t *testing.T) {
	// 3.10 Mercator and the stretched Gaussian family 3.42/3.43 are
	// metadata-only templates with no La1/Lo1/La2/Lo2.
	for _, tmpl := range []uint16{10, 42, 43} {
		g, err := New(makeSec3(tmpl, nil))
		if err != nil {
			t.Fatalf("New(%d): %v", tmpl, err)
		}
		if _, _, _, _, ok := g.Bounds(); ok {
			t.Errorf("template %d: Bounds ok = true, want false (no corner fields)", tmpl)
		}
	}

	// 3.0 (and the reduced Gaussian family) do carry corners.
	g0, err := New(makeSec3(0, func(b []byte) {
		fillBase(b, 90, -180, -90, 175, 5, 5, 72, 37, 0, 0)
	}))
	if err != nil {
		t.Fatalf("New(0): %v", err)
	}
	if lat1, lon1, lat2, lon2, ok := g0.Bounds(); !ok || lat1 != 90 || lon1 != -180 || lat2 != -90 || lon2 != 175 {
		t.Errorf("template 0: Bounds = (%v, %v, %v, %v, %v), want the encoded corners", lat1, lon1, lat2, lon2, ok)
	}

	// GRIB1 types 0/4/10 carry the same four fields; a projection does not.
	g1, err := NewGRIB1(latLonGDS(GRIB1LatLon, 2, 2, 10, 20, 0, 30, 20, 20, 0).done())
	if err != nil {
		t.Fatalf("NewGRIB1(type 0): %v", err)
	}
	if lat1, lon1, lat2, lon2, ok := g1.Bounds(); !ok || lat1 != 10 || lon1 != 20 || lat2 != 0 || lon2 != 30 {
		t.Errorf("GRIB1 type 0: Bounds = (%v, %v, %v, %v, %v), want (10, 20, 0, 30, true)", lat1, lon1, lat2, lon2, ok)
	}
	g5 := newGDS(GRIB1PolarStereo)
	g5.u16(6, 3).u16(8, 2)
	g5.smag24(10, 60).smag24(13, 0).at(16, 0x80)
	g5.smag24(17, 0)
	g5.u24(20, 100000).u24(23, 100000)
	g5.at(26, 0).at(27, 0)
	d5, err := NewGRIB1(g5.done())
	if err != nil {
		t.Fatalf("NewGRIB1(type 5): %v", err)
	}
	if _, _, _, _, ok := d5.Bounds(); ok {
		t.Error("GRIB1 type 5 (polar stereographic): Bounds ok = true, want false")
	}
}

// TestReducedGaussianRealFixture runs the fix over the real eccodes-produced
// fixture the team lead staged in .fixtures-verify (a regional reduced_gg
// grid: template 40, Ni missing, Nj=8, N=1280, 8505906 declared points,
// corners 89.736143/0 -> 0/359.929687). The directory is a temporary
// verification artifact outside the repository, so the test skips when it is
// absent rather than failing on a clean checkout.
func TestReducedGaussianRealFixture(t *testing.T) {
	sec3 := section3OfFixture(t, "reduced_gg_regional.grib2")
	g, err := New(sec3)
	if err != nil {
		t.Fatalf("New over the eccodes fixture: %v", err)
	}
	if g.TemplateNumber() != 40 {
		t.Errorf("TemplateNumber = %d, want 40", g.TemplateNumber())
	}
	if g.Ni() != 0 || g.Nj() != 8 {
		t.Errorf("Ni/Nj = %d/%d, want 0/8", g.Ni(), g.Nj())
	}
	if g.GaussianN() != 1280 {
		t.Errorf("GaussianN = %d, want 1280", g.GaussianN())
	}
	if n := g.NumPoints(); n != 0 {
		t.Errorf("NumPoints = %d, want 0", n)
	}
	if n := g.NumPointsDeclared(); n != 8505906 {
		t.Errorf("NumPointsDeclared = %d, want 8505906 (ecCodes numberOfPoints)", n)
	}
	if !g.IsReducedGaussian() {
		t.Error("IsReducedGaussian = false, want true")
	}
	lat1, lon1, lat2, lon2, ok := g.Bounds()
	if !ok {
		t.Fatal("Bounds ok = false, want true")
	}
	if lat1 != 89.736143 || lon1 != 0.0 || lat2 != 0.0 || lon2 != 359.929687 {
		t.Errorf("Bounds = (%v, %v, %v, %v), want (89.736143, 0, 0, 359.929687) as ecCodes reports", lat1, lon1, lat2, lon2)
	}
	if _, _, err := g.Point(0); err != nil {
		wantNotImplemented(t, "Point(0)", err, 40)
	} else {
		t.Error("Point(0): expected ErrNotImplemented")
	}
}

// section3OfFixture reads a single-message GRIB2 file and returns its section
// 3 bytes. It is a test helper, not a reader: the library's own Open lives in
// the root package, which imports this one.
func section3OfFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", ".fixtures-verify", name)
	if _, err := os.Stat(path); err != nil {
		t.Skipf("external verification fixture %s not staged: %v", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if len(raw) < 16 || string(raw[0:4]) != "GRIB" || raw[7] != 2 {
		t.Fatalf("%s: not a GRIB2 message", name)
	}
	total := int(binary.BigEndian.Uint64(raw[8:16]))
	if total > len(raw) {
		total = len(raw)
	}
	for off := 16; off+5 <= total; {
		slen := int(binary.BigEndian.Uint32(raw[off : off+4]))
		num := raw[off+4]
		if slen < 5 || off+slen > total {
			t.Fatalf("%s: malformed section %d at offset %d (length %d)", name, num, off, slen)
		}
		if num == 3 {
			return raw[off : off+slen]
		}
		if num == 8 {
			break
		}
		off += slen
	}
	// Every GRIB2 message carries a section 3, so reaching here means the
	// fixture is not a well-formed single message.
	t.Fatalf("%s: section 3 not found", name)
	return nil
}
