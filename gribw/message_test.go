// Tests for the message framework: round-trip field assertions, golden byte
// stability, determinism, multi-message self-delimitation, and the fail-loud
// matrix. The data sections are hand-built here; their layouts follow the
// vendored spec tables (§5.12/§5.13) exactly as the reader walks them:
// 5.0 = R F32 @octet12 (stored PRE-scaled by 10^D), E U16 @16, D U16 @18,
// bits U8 @20, origType U8 @21; 5.4 = precision U8 @12.

package gribw

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	grib "github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/grid"
)

const goldenSimple = "../testdata/gribw-xcheck/p31_frame_simple.grib2"

// fixtureGrid loads the 3×3 lat/lon grid from the committed poscorners.grib2
// fixture (all-positive corners, decoded with eccodes) and returns it for
// SetGrid.
func fixtureGrid(t *testing.T) *grid.Definition {
	t.Helper()
	b, err := os.ReadFile("../testdata/ecc-xcheck/poscorners.grib2")
	if err != nil {
		t.Fatalf("read poscorners.grib2: %v", err)
	}
	f, err := grib.OpenReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()
	m, err := f.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}
	g, err := m.Grid()
	if err != nil {
		t.Fatalf("Grid: %v", err)
	}
	return g
}

// canonicalID is the fixed identification used by all golden/round-trip tests.
func canonicalID() *grib.Identification {
	return &grib.Identification{
		Centre:              98,
		SubCentre:           0,
		MasterTableVersion:  3,
		LocalTableVersion:   0,
		ReferenceTime:       time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		ProductionStatus:    0,
		TypeOfProcessedData: 255,
		Calendar:            &grib.Calendar{TemplateNumber: 0, TypeOfCalendar: 0},
	}
}

func canonicalProduct() *grib.Product {
	return &grib.Product{
		TemplateNumber:          0,
		ParameterCategory:       2,
		ParameterNumber:         2,
		TypeOfGeneratingProcess: 0,
		ForecastTime:            &grib.ForecastTime{Unit: 1, Value: 0},
		Level1:                  &grib.Level{Type: 10, Scale: 850.0, HasValue: true},
		NV:                      0,
	}
}

// simpleSec5 builds a template 5.0 section 5 header (21 bytes, minimum) with
// the given R (stored pre-scaled by 10^D), E, D, bits. NV is fixed at 9 (3×3).
func simpleSec5(t *testing.T, R float32, E, D int16, bits uint8) []byte {
	t.Helper()
	b := make([]byte, 21)
	binary.BigEndian.PutUint32(b[0:4], 21)
	b[4] = 5
	binary.BigEndian.PutUint32(b[5:9], 9) // numberOfValues (octets 6-9)
	// template number 0 at octets 10-11 stays zero
	binary.BigEndian.PutUint32(b[11:15], math.Float32bits(R)) // R @ octet 12
	// E/D are GRIB2 sign-magnitude int16 (bit 15 = sign, low 15 bits =
	// magnitude), NOT two's complement — eccodes' grib_set writes D=-2 as
	// 0x8002, and real ECMWF files store E=-6 as 0x8006. Callers in this file
	// pass only non-negative E/D, where both conventions coincide.
	binary.BigEndian.PutUint16(b[15:17], smu16(int32(E))) // E @ octet 16 (sign-magnitude)
	binary.BigEndian.PutUint16(b[17:19], smu16(int32(D))) // D @ octet 18 (sign-magnitude)
	b[19] = bits                                          // bits @ octet 20
	b[20] = 0                                             // origType 0 (float) @ octet 21
	return b
}

// packMSB packs raw values MSB-first into a bitstream of `bits` bits each.
func packMSB(vals []uint32, bits int) []byte {
	out := make([]byte, (len(vals)*bits+7)/8)
	for i, v := range vals {
		off := i * bits
		for b := 0; b < bits; b++ {
			if v&(uint32(1)<<uint(bits-1-b)) != 0 {
				out[(off+b)/8] |= 0x80 >> uint((off+b)%8)
			}
		}
	}
	return out
}

func ieeeSec5() []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint32(b[0:4], 12)
	b[4] = 5
	binary.BigEndian.PutUint32(b[5:9], 9)  // NV
	binary.BigEndian.PutUint16(b[9:11], 4) // template 5.4
	b[11] = 1                              // precision 1 (binary32), octet 12
	return b
}

func f32s(vals ...float32) []byte {
	out := make([]byte, 4*len(vals))
	for i, v := range vals {
		binary.BigEndian.PutUint32(out[4*i:], math.Float32bits(v))
	}
	return out
}

// buildCanonical assembles the golden message: canonical id/product/grid +
// simple packing (D=0, E=0, R=0, bits=16) with values 0..8.
func buildCanonical(t *testing.T) []byte {
	t.Helper()
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
	var raws []uint32
	for i := 0; i < 9; i++ {
		raws = append(raws, uint32(i))
	}
	if err := m.SetDataSection(simpleSec5(t, 0, 0, 0, 16), packMSB(raws, 16)); err != nil {
		t.Fatalf("SetDataSection: %v", err)
	}
	b, err := m.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return b
}

func readBack(t *testing.T, b []byte) *grib.Message {
	t.Helper()
	f, err := grib.OpenReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("reader OpenReader: %v", err)
	}
	defer f.Close()
	n, err := f.NumMessages()
	if err != nil || n != 1 {
		t.Fatalf("NumMessages = %d, %v; want 1", n, err)
	}
	m, err := f.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}
	return m
}

func TestBuildRoundTripSimpleTrivial(t *testing.T) {
	b := buildCanonical(t)

	// Structural invariants on the raw bytes first.
	if !bytes.Equal(b[len(b)-4:], []byte("7777")) {
		t.Errorf("message does not end with bare 7777 tail: % x", b[len(b)-8:])
	}
	if got := binary.BigEndian.Uint64(b[8:16]); got != uint64(len(b)) {
		t.Errorf("section 0 totalLen U64 = %d, want %d (whole message incl. tail)", got, len(b))
	}
	if string(b[0:4]) != "GRIB" || b[7] != 2 {
		t.Errorf("section 0 magic/edition = % x / %d, want GRIB / 2", b[0:4], b[7])
	}

	m := readBack(t, b)
	if got := m.Discipline(); got != 10 {
		t.Errorf("Discipline = %d, want 10", got)
	}

	id, err := m.Identification()
	if err != nil {
		t.Fatalf("Identification: %v", err)
	}
	wantID := canonicalID()
	if id.Centre != wantID.Centre || id.SubCentre != wantID.SubCentre ||
		id.MasterTableVersion != wantID.MasterTableVersion ||
		id.LocalTableVersion != wantID.LocalTableVersion ||
		id.ProductionStatus != wantID.ProductionStatus ||
		id.TypeOfProcessedData != wantID.TypeOfProcessedData {
		t.Errorf("identification scalars = %+v, want %+v", id, wantID)
	}
	if !id.ReferenceTime.Equal(wantID.ReferenceTime) {
		t.Errorf("ReferenceTime = %v, want %v", id.ReferenceTime, wantID.ReferenceTime)
	}
	if id.Calendar == nil || id.Calendar.TemplateNumber != 0 || id.Calendar.TypeOfCalendar != 0 {
		t.Errorf("Calendar = %+v, want {TemplateNumber:0 TypeOfCalendar:0}", id.Calendar)
	}

	p, err := m.Product()
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	wantP := canonicalProduct()
	if p.TemplateNumber != 0 || p.ParameterCategory != wantP.ParameterCategory ||
		p.ParameterNumber != wantP.ParameterNumber ||
		p.TypeOfGeneratingProcess != wantP.TypeOfGeneratingProcess || p.NV != 0 {
		t.Errorf("product scalars = %+v, want %+v", p, wantP)
	}
	if p.ForecastTime == nil || p.ForecastTime.Unit != 1 || p.ForecastTime.Value != 0 {
		t.Errorf("ForecastTime = %+v, want {Unit:1 Value:0}", p.ForecastTime)
	}
	if p.Level1 == nil || !p.Level1.HasValue || p.Level1.Type != 10 || p.Level1.Scale != 850.0 {
		t.Errorf("Level1 = %+v, want {Type:10 Scale:850 HasValue:true}", p.Level1)
	}
	// Writer nil Level2 → type byte 255 + all-ones value → reader maps to a
	// valueless level, never to a usable value.
	if p.Level2 == nil || p.Level2.Type != 255 || p.Level2.HasValue {
		t.Errorf("Level2 = %+v, want valueless type-255 level", p.Level2)
	}

	g, err := m.Grid()
	if err != nil {
		t.Fatalf("Grid: %v", err)
	}
	src := fixtureGrid(t)
	if g.TemplateNumber() != src.TemplateNumber() || g.Ni() != 3 || g.Nj() != 3 || g.NumPoints() != 9 {
		t.Errorf("grid shape = tmpl %d Ni %d Nj %d pts %d, want tmpl %d 3 3 9",
			g.TemplateNumber(), g.Ni(), g.Nj(), g.NumPoints(), src.TemplateNumber())
	}
	for i := 0; i < 9; i++ {
		la1, lo1, err1 := src.Point(i)
		la2, lo2, err2 := g.Point(i)
		if err1 != nil || err2 != nil {
			t.Fatalf("Point(%d): %v / %v", i, err1, err2)
		}
		if la1 != la2 || lo1 != lo2 {
			t.Errorf("point %d = (%v,%v), want (%v,%v)", i, la2, lo2, la1, lo1)
		}
	}

	vals, err := m.Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	for i, v := range vals {
		if v != float32(i) {
			t.Errorf("value[%d] = %v, want %d (bit-exact)", i, v, i)
		}
	}
}

// TestBuildRoundTripSimpleScaled exercises the non-trivial WMO scaling path:
// D=1, E=0, stored R=-5 (pre-scaled; true reference -0.5), bits=8. raw = 10v+5
// is integral for v in {0.1..0.9}, and the reader's float64-then-float32 decode
// lands exactly on the correctly-rounded float32 constants.
func TestBuildRoundTripSimpleScaled(t *testing.T) {
	m := New()
	if err := m.SetIdentification(canonicalID()); err != nil {
		t.Fatal(err)
	}
	if err := m.SetGrid(fixtureGrid(t)); err != nil {
		t.Fatal(err)
	}
	if err := m.SetProduct(canonicalProduct()); err != nil {
		t.Fatal(err)
	}
	var raws []uint32
	for k := 1; k <= 9; k++ {
		raws = append(raws, uint32(k+5)) // V=k*0.1 → raw = V*10^D - R_stored = k + 5
	}
	if err := m.SetDataSection(simpleSec5(t, -5.0, 0, 1, 8), packMSB(raws, 8)); err != nil {
		t.Fatalf("SetDataSection: %v", err)
	}
	b, err := m.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	vals, err := readBack(t, b).Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	for k := 1; k <= 9; k++ {
		want := float32(float64(k) / 10.0) // correctly-rounded literal, e.g. float32(0.3)
		if vals[k-1] != want || math.Float32bits(vals[k-1]) != math.Float32bits(want) {
			t.Errorf("value[%d] = %v (bits %08x), want %v (bits %08x)", k-1, vals[k-1],
				math.Float32bits(vals[k-1]), want, math.Float32bits(want))
		}
	}
}

// TestBuildRoundTripIEEE pins bit-exact fidelity through the 5.4 path,
// including ±Inf, NaN and subnormals.
func TestBuildRoundTripIEEE(t *testing.T) {
	want := []float32{
		1.5, -0.5, 0,
		float32(math.Inf(1)), float32(math.Inf(-1)), math.Float32frombits(0x7fc00000),
		math.SmallestNonzeroFloat32, -math.SmallestNonzeroFloat32, math.MaxFloat32,
	}
	m := New()
	if err := m.SetIdentification(canonicalID()); err != nil {
		t.Fatal(err)
	}
	if err := m.SetGrid(fixtureGrid(t)); err != nil {
		t.Fatal(err)
	}
	if err := m.SetProduct(canonicalProduct()); err != nil {
		t.Fatal(err)
	}
	if err := m.SetDataSection(ieeeSec5(), f32s(want...)); err != nil {
		t.Fatalf("SetDataSection: %v", err)
	}
	b, err := m.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	got, err := readBack(t, b).Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	for i := range want {
		if math.IsNaN(float64(want[i])) {
			if !math.IsNaN(float64(got[i])) {
				t.Errorf("value[%d] = %v, want NaN", i, got[i])
			}
			continue
		}
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Errorf("value[%d] bits = %08x, want %08x (%v)", i,
				math.Float32bits(got[i]), math.Float32bits(want[i]), want[i])
		}
	}
}

// TestBuildDeterministicAndGolden: same input → identical bytes (twice in one
// process), and the canonical message equals the committed golden file byte
// for byte (cross-process determinism, pinned by md5 in the file header note).
func TestBuildDeterministicAndGolden(t *testing.T) {
	b1 := buildCanonical(t)
	b2 := buildCanonical(t)
	if !bytes.Equal(b1, b2) {
		t.Fatalf("Build is not deterministic: %d vs %d bytes differ", len(b1), len(b2))
	}
	golden, err := os.ReadFile(goldenSimple)
	if err != nil {
		t.Fatalf("read golden %s: %v (regenerate with GRIBW_GOLDEN_EMIT=1 go test ./gribw/ -run TestGoldenEmit)", goldenSimple, err)
	}
	if !bytes.Equal(b1, golden) {
		t.Errorf("canonical message diverged from golden %s: got %d bytes md5 %s, golden %d bytes",
			goldenSimple, len(b1), md5hex(b1), len(golden))
	}
}

// TestGoldenEmit is the regeneration tool for the golden file (env-gated so
// normal runs never write). Run it locally with:
//
//	GRIBW_GOLDEN_EMIT=1 go test ./gribw/ -run TestGoldenEmit
func TestGoldenEmit(t *testing.T) {
	if os.Getenv("GRIBW_GOLDEN_EMIT") == "" {
		t.Skip("set GRIBW_GOLDEN_EMIT=1 to regenerate the golden file")
	}
	b := buildCanonical(t)
	if err := os.MkdirAll(filepath.Dir(goldenSimple), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goldenSimple, b, 0o644); err != nil {
		t.Fatalf("write golden: %v", err)
	}
	t.Logf("wrote %s (%d bytes, md5 %s)", goldenSimple, len(b), md5hex(b))
}

// TestFileMultiMessage: two concatenated messages stay self-delimiting through
// the section 0 total length; each round-trips independently.
func TestFileMultiMessage(t *testing.T) {
	mk := func(raws ...uint32) *Message {
		m := New()
		if err := m.SetIdentification(canonicalID()); err != nil {
			t.Fatal(err)
		}
		if err := m.SetGrid(fixtureGrid(t)); err != nil {
			t.Fatal(err)
		}
		if err := m.SetProduct(canonicalProduct()); err != nil {
			t.Fatal(err)
		}
		if err := m.SetDataSection(simpleSec5(t, 0, 0, 0, 16), packMSB(raws, 16)); err != nil {
			t.Fatal(err)
		}
		return m
	}
	b, err := File(mk(1, 2, 3, 4, 5, 6, 7, 8, 9), mk(100, 200, 300, 400, 500, 600, 700, 800, 900))
	if err != nil {
		t.Fatalf("File: %v", err)
	}

	f, err := grib.OpenReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()
	n, err := f.NumMessages()
	if err != nil || n != 2 {
		t.Fatalf("NumMessages = %d, %v; want 2", n, err)
	}
	first, second := []float32{1, 2, 3, 4, 5, 6, 7, 8, 9}, []float32{100, 200, 300, 400, 500, 600, 700, 800, 900}
	for i, want := range [][]float32{first, second} {
		mi, err := f.Message(i)
		if err != nil {
			t.Fatalf("Message(%d): %v", i, err)
		}
		// Self-delimitation: each message's section 0 totalLen covers exactly
		// its own span (the reader splits on it).
		if uint64(mi.Size()) != binary.BigEndian.Uint64(b[mi.Offset()+8:mi.Offset()+16]) {
			t.Errorf("message %d: Size() = %d, section 0 totalLen disagrees", i, mi.Size())
		}
		vals, err := mi.Values()
		if err != nil {
			t.Fatalf("message %d Values: %v", i, err)
		}
		for j := range want {
			if vals[j] != want[j] {
				t.Errorf("message %d value[%d] = %v, want %v", i, j, vals[j], want[j])
			}
		}
	}
}

// TestBuildFailLoud: every missing/inconsistent input must fail with a
// descriptive error (fail-loud philosophy).
func TestBuildFailLoud(t *testing.T) {
	grid := fixtureGrid(t)

	base := func() *Message {
		m := New()
		if err := m.SetIdentification(canonicalID()); err != nil {
			t.Fatal(err)
		}
		if err := m.SetGrid(grid); err != nil {
			t.Fatal(err)
		}
		return m
	}

	cases := []struct {
		name string
		fn   func(m *Message) error
		want string
	}{
		{"missing identification", func(m *Message) error { _, e := New().Build(); return e }, "SetIdentification required"},
		{"missing section 3", func(m *Message) error {
			mm := New()
			if err := mm.SetIdentification(canonicalID()); err != nil {
				return err
			}
			_, e := mm.Build()
			return e
		}, "section 3 required"},
		{"missing product", func(m *Message) error { _, e := base().Build(); return e }, "SetProduct required"},
		{"missing data section", func(m *Message) error {
			mm := base()
			if err := mm.SetProduct(canonicalProduct()); err != nil {
				return err
			}
			_, e := mm.Build()
			return e
		}, "payload required"},
		{"product NV nonzero", func(m *Message) error {
			p := canonicalProduct()
			p.NV = 9
			return m.SetProduct(p)
		}, "numberOfValues must be 0"},
		{"product template nonzero", func(m *Message) error {
			p := canonicalProduct()
			p.TemplateNumber = 3
			return m.SetProduct(p)
		}, "not writable yet"},
		{"sec5 length field mismatch", func(m *Message) error {
			s5 := simpleSec5(t, 0, 0, 0, 16)
			binary.BigEndian.PutUint32(s5[0:4], 22) // lie about the length
			return m.SetDataSection(s5, packMSB([]uint32{0}, 16))
		}, "length field"},
		{"sec5 wrong number octet", func(m *Message) error {
			s5 := simpleSec5(t, 0, 0, 0, 16)
			s5[4] = 3
			return m.SetDataSection(s5, packMSB([]uint32{0}, 16))
		}, "section 5 number octet"},
		{"IEEE precision not 1", func(m *Message) error {
			s5 := ieeeSec5()
			s5[11] = 2 // 64-bit precision is out of scope for this writer
			if err := m.SetProduct(canonicalProduct()); err != nil {
				return err
			}
			if err := m.SetDataSection(s5, f32s(0, 0, 0, 0, 0, 0, 0, 0, 0)); err != nil {
				return err
			}
			_, e := m.Build()
			return e
		}, "precision"},
		{"simple payload size mismatch", func(m *Message) error {
			if err := m.SetProduct(canonicalProduct()); err != nil {
				return err
			}
			if err := m.SetDataSection(simpleSec5(t, 0, 0, 0, 16), packMSB([]uint32{0}, 8)); err != nil { // 9×16 bits needs 18B, got 1B
				return err
			}
			_, e := m.Build()
			return e
		}, "simple packing"},
		{"value count vs grid mismatch", func(m *Message) error {
			s5 := simpleSec5(t, 0, 0, 0, 16)
			binary.BigEndian.PutUint32(s5[5:9], 4) // NV=4 ≠ 9 grid points
			if err := m.SetProduct(canonicalProduct()); err != nil {
				return err
			}
			if err := m.SetDataSection(s5, packMSB([]uint32{0, 0, 0, 0}, 16)); err != nil {
				return err
			}
			_, e := m.Build()
			return e
		}, "numberOfValues"},
		{"unsupported template", func(m *Message) error {
			s5 := simpleSec5(t, 0, 0, 0, 16)
			binary.BigEndian.PutUint16(s5[9:11], 30) // JPEG-2000: placeholder scope
			if err := m.SetProduct(canonicalProduct()); err != nil {
				return err
			}
			if err := m.SetDataSection(s5, packMSB([]uint32{0}, 16)); err != nil {
				return err
			}
			_, e := m.Build()
			return e
		}, "not implemented in "},
		{"discipline out of range", func(m *Message) error { return New().SetDiscipline(256) }, "out of range"},
		// Note: no "invalid reference time" case — a Go time.Time is always
		// normalized (time.Date(2026, 2, 30) becomes Mar 2), so encodeSec1's
		// invalid-date branch is unreachable through the public API.
	}

	m := base()
	if err := m.SetProduct(canonicalProduct()); err != nil {
		t.Fatalf("base product: %v", err)
	}
	for _, tc := range cases {
		err := tc.fn(m)
		if err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
			continue
		}
		if !contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not contain %q", tc.name, err.Error(), tc.want)
		}
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func md5hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}
