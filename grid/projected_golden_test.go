//
// golden tests: template 3.20 (polar stereographic) / 3.30 (Lambert
// conformal) coordinates against B's oracle anchor set
// (scripts/xcheck/grid-t20t30-oracle.py header, eccodes 2.41 @ the Linux bench host).
//
// Anchor convention: text digest = sha256 of the concatenated
// "%.10f,%.10f;" per-point lat/lon string (oracle convention, replayable via
// `python3 scripts/xcheck/grid-t20t30-oracle.py <file>` on the Linux bench host). The four
// synthetic fixtures are small enough that Go's libm and glibc agree at the
// %.10f level on every point — the digest is a hard gate here. (The real
// samples S1/C38 exceed that: see grid_t30_baseline_test.go header for the
// dual-column anchor convention and the ulp-level Go-vs-glibc evidence.)

package grid

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const projXcheckDir = "../testdata/gribw-xcheck"

// textDigestPts is the oracle convention: "%.10f,%.10f;" per point, sha256.
func textDigestPts(pts []Point) string {
	var s string
	for _, p := range pts {
		s += fmt.Sprintf("%.10f,%.10f;", p.Lat, p.Lon)
	}
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum)
}

func readSec3File(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(projXcheckDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	p := 16
	for p+5 <= len(b) {
		ln := int(uint32(b[p])<<24 | uint32(b[p+1])<<16 | uint32(b[p+2])<<8 | uint32(b[p+3]))
		snum := b[p+4]
		if snum == 3 {
			return b[p : p+ln]
		}
		if snum == 7 {
			break
		}
		p += ln
	}
	t.Fatalf("%s: section 3 not found", name)
	return nil
}

// TestP44_T20T30FixtureGolden replays B's four synthetic fixtures through the
// Go coordinate generator and checks the oracle text digests (bit-exact at
// %.10f on these grids), NumPoints vs declared, and Lats/Lons per-point
// consistency .
func TestP44_T20T30FixtureGolden(t *testing.T) {
	anchors := []struct {
		file       string
		tmpl, nv   int
		wantDigest string
	}{
		{"t20_grid_3x3.grib2", 20, 9, "51895186748c7104374d39f90728ff1efa33714974242aa211cee6686ea3d460"},
		{"t20_even_4x3.grib2", 20, 12, "b81357f291eb432ae758476e922a91fef692d997b5984e554bb1548418c097b0"},
		{"t30_grid_3x3.grib2", 30, 9, "0d79958624827c884fb981bb3211d6ed16969dc7074f6dd6b789b2f0550914bd"},
		{"t30_even_3x4.grib2", 30, 12, "76182bd500bffbc8644180bd47dc6c0acac3256379c71caaec462e023feb1ec4"},
	}
	for _, a := range anchors {
		t.Run(a.file, func(t *testing.T) {
			g, err := New(readSec3File(t, a.file))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if g.TemplateNumber() != a.tmpl {
				t.Fatalf("TemplateNumber=%d, want %d", g.TemplateNumber(), a.tmpl)
			}
			if n := g.NumPoints(); n != a.nv {
				t.Fatalf("NumPoints=%d, want %d (declared=%d)", n, a.nv, g.NumPointsDeclared())
			}
			pts := make([]Point, a.nv)
			for i := range pts {
				la, lo, err := g.Point(i)
				if err != nil {
					t.Fatalf("Point(%d): %v", i, err)
				}
				pts[i] = Point{Lat: la, Lon: lo}
			}
			if got := textDigestPts(pts); got != a.wantDigest {
				t.Fatalf("text digest = %s…, want %s… (oracle anchor)\nfirst3=%v",
					got[:12], a.wantDigest[:12], pts[:min(3, len(pts))])
			}
			// Lats/Lons per-point arrays ( contract revision).
			lats, err := g.Lats()
			if err != nil {
				t.Fatalf("Lats: %v", err)
			}
			lons, err := g.Lons()
			if err != nil {
				t.Fatalf("Lons: %v", err)
			}
			if len(lats) != a.nv || len(lons) != a.nv {
				t.Fatalf("Lats/Lons len = %d/%d, want %d (full Ni×Nj per-point arrays)", len(lats), len(lons), a.nv)
			}
			for i := range pts {
				if lats[i] != pts[i].Lat || lons[i] != pts[i].Lon {
					t.Fatalf("Lats/Lons[%d]=(%g,%g) != Point(%d)=(%g,%g)", i, lats[i], lons[i], i, pts[i].Lat, pts[i].Lon)
				}
			}
		})
	}
}

// TestP44_T20T30Metadata checks the parsed template fields against the known
// fixture values (B hand-crafted sec3 bytes, eccodes 2.41 parse agreement).
func TestP44_T20T30Metadata(t *testing.T) {
	t.Run("t20_grid_3x3", func(t *testing.T) {
		g, err := New(readSec3File(t, "t20_grid_3x3.grib2"))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		p := g.proj
		if p == nil {
			t.Fatal("proj params not parsed")
		}
		if g.ShapeOfEarth() != 6 || g.Radius() != 6371229 {
			t.Fatalf("shape=%d radius=%g, want 6/6371229", g.ShapeOfEarth(), g.Radius())
		}
		if p.Ni != 3 || p.Nj != 3 {
			t.Fatalf("Ni×Nj = %d×%d, want 3×3", p.Ni, p.Nj)
		}
		if p.La1 != 50.0 || p.Lo1 != 0.0 {
			t.Fatalf("La1/Lo1 = %g/%g, want 50/0", p.La1, p.Lo1)
		}
		if p.LaD != 60.0 || p.orient != 0.0 {
			t.Fatalf("LaD/orient = %g/%g, want 60/0", p.LaD, p.orient)
		}
		if p.Dx != 5e5 || p.Dy != 5e5 {
			t.Fatalf("Dx/Dy = %g/%g, want 5e5/5e5", p.Dx, p.Dy)
		}
		if p.scanMode != 192 {
			t.Fatalf("scanMode=%d, want 192", p.scanMode)
		}
	})
	t.Run("t30_grid_3x3", func(t *testing.T) {
		g, err := New(readSec3File(t, "t30_grid_3x3.grib2"))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		p := g.proj
		if p == nil {
			t.Fatal("proj params not parsed")
		}
		if p.Ni != 3 || p.Nj != 3 {
			t.Fatalf("Ni×Nj = %d×%d, want 3×3", p.Ni, p.Nj)
		}
		if p.La1 != 35.0 || p.Lo1 != 250.0 {
			t.Fatalf("La1/Lo1 = %g/%g, want 35/250", p.La1, p.Lo1)
		}
		if p.LaD != 45.0 || p.LoV != 260.0 {
			t.Fatalf("LaD/LoV = %g/%g, want 45/260", p.LaD, p.LoV)
		}
		if p.Latin1 != 30.0 || p.Latin2 != 60.0 {
			t.Fatalf("Latin1/Latin2 = %g/%g, want 30/60", p.Latin1, p.Latin2)
		}
		if p.Dx != 3e5 || p.Dy != 3e5 {
			t.Fatalf("Dx/Dy = %g/%g, want 3e5/3e5", p.Dx, p.Dy)
		}
	})
}

// TestP44_T20T30FailLoud pins the fail-loud boundaries (no silent garbage):
// oblate shapes, missing Ni/Nj, opposite-side standard parallels.
func TestP44_T20T30FailLoud(t *testing.T) {
	t.Run("t20_oblate_shape", func(t *testing.T) {
		b := makeSec3(20, func(b []byte) {
			b[14] = 7 // shape: oblate (producer-supplied) — not spherical
			putU32(b, 30, 3)
			putU32(b, 34, 3)
			putSmag(b, 38, 50_000_000)
			putU32(b, 42, 0)
			putSmag(b, 47, 60_000_000)
			putSmag(b, 51, 0)
			putU32(b, 55, 500_000) // Dx = 5e8 * 1e-3 m... any positive value
			putU32(b, 59, 500_000)
		})
		g, err := New(b)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, _, err := g.Point(0); err == nil {
			t.Fatal("Point(0): expected fail-loud for oblate shape, got nil")
		} else if want := "not spherical"; !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want mention of %q", err.Error(), want)
		}
	})
	t.Run("t30_missing_Ni", func(t *testing.T) {
		b := makeSec3(30, func(b []byte) {
			b[14] = 6
			putU32(b, 30, missingU32) // Ni missing (all ones)
			putU32(b, 34, 3)
		})
		if _, err := New(b); err == nil {
			t.Fatal("New: expected error for missing Ni")
		}
	})
	t.Run("t30_opposite_side_standards", func(t *testing.T) {
		b := makeSec3(30, func(b []byte) {
			b[14] = 6
			putU32(b, 30, 3)
			putU32(b, 34, 3)
			putSmag(b, 38, 35_000_000)
			putU32(b, 42, 250_000_000)
			putSmag(b, 47, 45_000_000)
			putU32(b, 51, 260_000_000)
			putU32(b, 55, 300_000)
			putU32(b, 59, 300_000)
			putSmag(b, 65, 30_000_000)  // Latin1 = +30
			putSmag(b, 69, -30_000_000) // Latin2 = -30: equal magnitudes, opposite sides
		})
		g, err := New(b)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, _, err := g.Point(0); err == nil {
			t.Fatal("Point(0): expected fail-loud for opposite-side standard parallels")
		} else if !strings.Contains(err.Error(), "opposite sides of equator") {
			t.Fatalf("error = %q, want mention of opposite sides of equator", err.Error())
		}
	})
}
