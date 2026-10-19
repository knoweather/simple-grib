// Tests for the template 3.0 section 3 writer (gribw.EncodeGrid). The writer
// had no grid-definition constructor, so callers hand-built section 3 and
// could store Di/Dj while leaving octet 55 (flag table 3.3) at 0x00 — a
// section that contradicts itself and that eccodes then reports as having no
// iDirectionIncrement / jDirectionIncrement. These tests pin the flag
// derivation and the rest of the section 3 layout.
//
// The oracle here is eccodes' own output: testdata/p6-a-xcheck/p6a_ll_basic.grib2
// was produced by the system eccodes C API (gen_p6a.py) and is compared
// byte-for-byte. Its section 3 is 72 octets with octet 55 = 0x30.

package gribw

import (
	"encoding/binary"
	"os"
	"strings"
	"testing"

	"github.com/knoweather/simple-grib/grid"
)

// section3Of extracts one complete section 3 (length prefix included) from a
// fixture file.
func section3Of(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for p := 16; p+4 <= len(raw); {
		n := int(binary.BigEndian.Uint32(raw[p : p+4]))
		if n < 5 {
			break
		}
		if raw[p+4] == 3 {
			return raw[p : p+n]
		}
		p += n
	}
	t.Fatalf("no section 3 found in %s", path)
	return nil
}

func f64(v float64) *float64 { return &v }

// TestEncodeGridMatchesEccodes is the byte-for-byte differential against the
// eccodes-generated fixture. The parameters are the fixture's own field values
// (decoded from its section 3), so a match proves the writer stores exactly what
// eccodes stores — including the 0x30 resolution flags and the missing sentinels
// for the fixed-shape earth fields.
func TestEncodeGridMatchesEccodes(t *testing.T) {
	want := section3Of(t, "../testdata/p6-a-xcheck/p6a_ll_basic.grib2")
	got, err := EncodeGrid(GridParams{
		Source:      0,
		Shape:       6,
		RadiusScale: -1, RadiusValue: -1,
		MajorScale: -1, MajorValue: -1,
		MinorScale: -1, MinorValue: -1,
		Ni: 7, Nj: 4,
		BasicAngle:   0,
		Subdivisions: -1,
		La1:          30, Lo1: 100, La2: 0, Lo2: 160,
		Di: f64(10), Dj: f64(10),
		ScanMode: 0,
	})
	if err != nil {
		t.Fatalf("EncodeGrid: %v", err)
	}
	if len(got) != 72 {
		t.Fatalf("section 3 length = %d, want 72 (WMO template 3.0 without point list)", len(got))
	}
	if got[54] != 0x30 {
		t.Errorf("octet 55 = 0x%02X, want 0x30 (flag table 3.3: bit 3 i given, bit 4 j given)", got[54])
	}
	if string(got) != string(want) {
		t.Errorf("section 3 bytes differ from the eccodes fixture:\n got % X\nwant % X", got, want)
	}
}

// TestEncodeGridFlags pins the flag derivation: the flags must follow what is
// actually stored, not an unchecked caller byte.
func TestEncodeGridFlags(t *testing.T) {
	base := GridParams{
		Shape: 6, Ni: 7, Nj: 4,
		La1: 30, Lo1: 100, La2: 0, Lo2: 160,
	}
	cases := []struct {
		name  string
		p     GridParams
		flags byte
	}{
		{"both increments given", with(base, func(q *GridParams) { q.Di, q.Dj = f64(10), f64(10) }), 0x30},
		{"only i given", with(base, func(q *GridParams) { q.Di = f64(10) }), 0x20},
		{"only j given", with(base, func(q *GridParams) { q.Dj = f64(10) }), 0x10},
		{"neither given", base, 0x00},
		{"vector components + both", with(base, func(q *GridParams) { q.Di, q.Dj, q.VectorComponents = f64(10), f64(10), true }), 0x38},
	}
	for _, tc := range cases {
		sec, err := EncodeGrid(tc.p)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if sec[54] != tc.flags {
			t.Errorf("%s: octet 55 = 0x%02X, want 0x%02X", tc.name, sec[54], tc.flags)
		}
		// An increment that is not given must be stored as the missing sentinel,
		// not as 0 — otherwise the flag and the field disagree.
		if tc.p.Di == nil && binary.BigEndian.Uint32(sec[63:67]) != 0xFFFFFFFF {
			t.Errorf("%s: Di field is not the missing sentinel while flag bit 3 is 0", tc.name)
		}
		if tc.p.Dj == nil && binary.BigEndian.Uint32(sec[67:71]) != 0xFFFFFFFF {
			t.Errorf("%s: Dj field is not the missing sentinel while flag bit 4 is 0", tc.name)
		}
	}

	// 0xF0 is not a valid flag value: bits 1-2 of table 3.3 are Reserved, so the
	// value for "Di and Dj given" is 0x30. Table 3.3 is indexed by bit number, so
	// 0xF0 is not in it at all.
	if err := checkTableCode("3.3", "resolution flags", 0xF0); err == nil {
		t.Errorf("table 3.3 accepted 0xF0; bits 1-2 are Reserved and the correct value is 0x30")
	}
}

// TestEncodeGridRoundTrip drives the writer output back through the reader.
func TestEncodeGridRoundTrip(t *testing.T) {
	sec, err := EncodeGrid(GridParams{
		Source: 0, Shape: 6, Ni: 7, Nj: 4,
		La1: 30, Lo1: 100, La2: 0, Lo2: 160,
		Di: f64(10), Dj: f64(10), ScanMode: 0,
	})
	if err != nil {
		t.Fatalf("EncodeGrid: %v", err)
	}
	g, err := grid.New(sec)
	if err != nil {
		t.Fatalf("grid.New on the writer's own output: %v", err)
	}
	if g.TemplateNumber() != 0 || g.SourceOfGridDefinition() != 0 || g.ShapeOfEarth() != 6 {
		t.Errorf("header: template %d source %d shape %d, want 0/0/6", g.TemplateNumber(), g.SourceOfGridDefinition(), g.ShapeOfEarth())
	}
	if g.Ni() != 7 || g.Nj() != 4 || g.NumPoints() != 28 || g.NumPointsDeclared() != 28 {
		t.Errorf("counts: Ni %d Nj %d points %d declared %d, want 7/4/28/28", g.Ni(), g.Nj(), g.NumPoints(), g.NumPointsDeclared())
	}
	for _, e := range []struct {
		what string
		got  float64
		want float64
	}{
		{"La1", g.La1, 30}, {"Lo1", g.Lo1, 100}, {"La2", g.La2, 0}, {"Lo2", g.Lo2, 160},
		{"Di", g.Di, 10}, {"Dj", g.Dj, 10},
	} {
		if e.got != e.want {
			t.Errorf("%s = %v, want %v", e.what, e.got, e.want)
		}
	}
	// Scan mode 0: i fastest, j scanned in the -j direction, so data order runs
	// from (La1, Lo1) to (La2, Lo2).
	checkPoint(t, g, 0, 30, 100)
	checkPoint(t, g, 27, 0, 160)
}

// TestEncodeGridScanMode pins octet 72 and the reader's reading of it: bit 2
// (0x40) = j scanned in the +j direction, so data order runs from La1 upwards.
func TestEncodeGridScanMode(t *testing.T) {
	sec, err := EncodeGrid(GridParams{
		Shape: 6, Ni: 7, Nj: 4,
		La1: 0, Lo1: 100, La2: 30, Lo2: 160,
		Di: f64(10), Dj: f64(10), ScanMode: 0x40,
	})
	if err != nil {
		t.Fatalf("EncodeGrid: %v", err)
	}
	if sec[71] != 0x40 {
		t.Errorf("octet 72 = 0x%02X, want 0x40", sec[71])
	}
	g, err := grid.New(sec)
	if err != nil {
		t.Fatalf("grid.New: %v", err)
	}
	checkPoint(t, g, 0, 0, 100)
	checkPoint(t, g, 27, 30, 160)
}

func checkPoint(t *testing.T, g *grid.Definition, i int, wantLat, wantLon float64) {
	t.Helper()
	lat, lon, err := g.Point(i)
	if err != nil {
		t.Errorf("Point(%d): %v", i, err)
		return
	}
	if lat != wantLat || lon != wantLon {
		t.Errorf("Point(%d) = (%v, %v), want (%v, %v)", i, lat, lon, wantLat, wantLon)
	}
}

// TestEncodeGridQuasiRegular covers the point list: the section grows past octet
// 72 and the declared count is the sum of the list. Per §5.6/Note 15 the
// increment of the varying dimension (here Ni, so Di) must stay missing, so only
// flag bit 4 is set.
func TestEncodeGridQuasiRegular(t *testing.T) {
	sec, err := EncodeGrid(GridParams{
		Shape: 6, Ni: -1, Nj: 4,
		La1: 30, Lo1: 100, La2: 0, Lo2: 160,
		Dj:                      f64(10),
		PointListInterpretation: 2, PointListWidth: 1,
		PointList: []int{3, 4, 4, 5},
	})
	if err != nil {
		t.Fatalf("EncodeGrid: %v", err)
	}
	if len(sec) != 76 {
		t.Fatalf("section 3 length = %d, want 72 + 4×1 = 76", len(sec))
	}
	if binary.BigEndian.Uint32(sec[0:4]) != 76 {
		t.Errorf("length prefix = %d, want 76", binary.BigEndian.Uint32(sec[0:4]))
	}
	if sec[54] != 0x10 {
		t.Errorf("octet 55 = 0x%02X, want 0x10 (only j increment given; Ni is missing so Di must stay missing)", sec[54])
	}
	if binary.BigEndian.Uint32(sec[63:67]) != 0xFFFFFFFF {
		t.Errorf("Di field = %d, want the missing sentinel", binary.BigEndian.Uint32(sec[63:67]))
	}
	g, err := grid.New(sec)
	if err != nil {
		t.Fatalf("grid.New: %v", err)
	}
	if !g.IsQuasiRegular() || g.NumPoints() != 16 {
		t.Errorf("quasi-regular = %v, points = %d, want true/16", g.IsQuasiRegular(), g.NumPoints())
	}
}

// TestEncodeGridMissingBasicAngle pins the §5.3 default: a missing basic angle
// reads as 1, so the unit stays microdegrees and the writer and the reader agree.
func TestEncodeGridMissingBasicAngle(t *testing.T) {
	sec, err := EncodeGrid(GridParams{
		Shape: 6, Ni: 3, Nj: 2,
		BasicAngle: -1, Subdivisions: 1000000,
		La1: 45, Lo1: 10, La2: 46, Lo2: 12,
		Di: f64(1), Dj: f64(1),
	})
	if err != nil {
		t.Fatalf("EncodeGrid: %v", err)
	}
	if binary.BigEndian.Uint32(sec[38:42]) != 0xFFFFFFFF {
		t.Errorf("basic angle field = %d, want the missing sentinel", binary.BigEndian.Uint32(sec[38:42]))
	}
	if binary.BigEndian.Uint32(sec[46:50]) != 45000000 {
		t.Errorf("La1 stored as %d, want 45000000 (45 degrees in microdegrees)", binary.BigEndian.Uint32(sec[46:50]))
	}
	g, err := grid.New(sec)
	if err != nil {
		t.Fatalf("grid.New: %v", err)
	}
	lat, lon, err := g.Point(0)
	if err != nil {
		t.Fatalf("Point(0): %v", err)
	}
	if lat != 45 || lon != 10 {
		t.Errorf("Point(0) = (%v, %v), want (45, 10)", lat, lon)
	}
}

// TestEncodeGridFailLoud pins the checks that hand-built bytes had none of.
func TestEncodeGridFailLoud(t *testing.T) {
	base := GridParams{Shape: 6, Ni: 7, Nj: 4, La1: 30, Lo1: 100, La2: 0, Lo2: 160, Di: f64(10), Dj: f64(10)}
	cases := []struct {
		name string
		p    GridParams
		want string
	}{
		{"reserved shape code", with(base, func(q *GridParams) { q.Shape = 12 }), "Reserved in table 3.2"},
		{"missing shape", with(base, func(q *GridParams) { q.Shape = 255 }), "missing"},
		{"source 255", with(base, func(q *GridParams) { q.Source = 255 }), "no grid definition applies"},
		{"point list without width", with(base, func(q *GridParams) {
			q.Ni = -1
			q.PointListInterpretation, q.PointListWidth, q.PointList = 2, 0, []int{3}
		}), "width"},
		{"interpretation 3", with(base, func(q *GridParams) {
			q.Ni = -1
			q.PointListInterpretation, q.PointListWidth, q.PointList = 3, 1, []int{3}
		}), "interpretation 3"},
		{"quasi-regular with both counts", with(base, func(q *GridParams) {
			q.PointListInterpretation, q.PointListWidth, q.PointList = 2, 1, []int{3, 4, 4, 5}
		}), "exactly one of Ni/Nj"},
		{"quasi-regular with stored Di (Ni missing)", with(base, func(q *GridParams) {
			q.Ni = -1
			q.PointListInterpretation, q.PointListWidth, q.PointList = 2, 1, []int{3, 4, 4, 5}
		}), "cannot carry a stored i increment"},
		{"quasi-regular with stored Dj (Nj missing)", with(base, func(q *GridParams) {
			q.Nj = -1
			q.PointListInterpretation, q.PointListWidth, q.PointList = 2, 1, []int{3, 4, 4, 5}
		}), "cannot carry a stored j increment"},
		{"angle not a multiple of the unit", with(base, func(q *GridParams) { q.La1 = 30.0000001 }), "not an exact multiple"},
		{"producer shape without value", with(base, func(q *GridParams) { q.Shape = 1; q.RadiusValue = -1 }), "needs the value"},
		{"stored Di contradicts the span (A-B5)", with(base, func(q *GridParams) { q.Di = f64(11) }), "contradicts"},
		{"scale factor out of range", with(base, func(q *GridParams) { q.Shape = 1; q.RadiusValue = 6371000; q.RadiusScale = 10 }), "outside [0,9]"},
	}
	for _, tc := range cases {
		_, err := EncodeGrid(tc.p)
		if err == nil {
			t.Errorf("%s: no error", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not mention %q", tc.name, err.Error(), tc.want)
		}
	}
}

func with(p GridParams, f func(*GridParams)) GridParams {
	q := p
	f(&q)
	return q
}
