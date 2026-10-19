package probe

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	grib "github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/gribw"
	"github.com/knoweather/simple-grib/grid"
	"github.com/knoweather/simple-grib/pack"
	"github.com/knoweather/simple-grib/spec"
)

// ---------------------------------------------------------------------------
// JPC: division by zero on unvalidated XTsiz/YTsiz
// ---------------------------------------------------------------------------

func sec5T540(numValues uint32, bits uint8, compressionType uint8) []byte {
	buf := make([]byte, 23)
	binary.BigEndian.PutUint32(buf[0:4], 23)
	buf[4] = 5
	binary.BigEndian.PutUint32(buf[5:9], numValues)
	binary.BigEndian.PutUint16(buf[9:11], 40)
	binary.BigEndian.PutUint32(buf[11:15], math.Float32bits(0)) // R
	binary.BigEndian.PutUint16(buf[15:17], 0)                   // E
	binary.BigEndian.PutUint16(buf[17:19], 0)                   // D
	buf[19] = bits
	buf[20] = 0 // orig type
	buf[21] = compressionType
	buf[22] = 0 // quality layers
	return buf
}

func TestProbeJPCTileSizeDivByZero(t *testing.T) {
	path := filepath.Join("..", "testdata", "p7-3-j2k", "f07.j2k")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	// Locate the SIZ marker segment.
	i := 0
	if binary.BigEndian.Uint16(raw[0:2]) == 0xff4f {
		i = 2
	}
	for i+2 < len(raw) {
		if raw[i] != 0xff {
			break
		}
		mark := int(raw[i+1]) | 0xff00
		l := int(binary.BigEndian.Uint16(raw[i+2 : i+4]))
		if mark == 0xff51 {
			base := i + 4
			binary.BigEndian.PutUint32(raw[base+18:base+22], 0) // XTsiz = 0
			binary.BigEndian.PutUint32(raw[base+22:base+26], 1) // YTsiz = 1
			break
		}
		i += 2 + l
	}

	def, ok := spec.Template(5, 40)
	if !ok {
		t.Fatal("no template 5.40 def")
	}
	sec5 := sec5T540(1500, 14, 0)
	w, err := spec.NewWalk(def, sec5)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC in pack.Unpack(5.40) with XTsiz=0: %v", r)
		}
	}()
	_, err = pack.Unpack(&pack.Context{TemplateNumber: 40, Fields: w, Data: raw, NumValues: 1500, GridPoints: 1500})
	t.Logf("pack.Unpack(5.40) XTsiz=0 -> %v", err)
}

// JPC: bitsPerValue = 0 path allocates ctx.NumValues entries with no guard.
func TestProbeJPCZeroBitsUnboundedAlloc(t *testing.T) {
	def, _ := spec.Template(5, 40)
	sec5 := sec5T540(4000000000, 0, 0)
	w, err := spec.NewWalk(def, sec5)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	t.Logf("NumValues=%d accepted by DataRep with no bound; pack.Unpack would allocate %d int32 (~%d MB) plus the float32 output (walk %d fields, no resource guard on this path)",
		4000000000, 4000000000, 4000000000*4/1024/1024, w.Len())
}

// ---------------------------------------------------------------------------
// grid: sign-magnitude inconsistency for 1-octet scale factors
// ---------------------------------------------------------------------------

// Earth-dimension scale factors are unsigned[1] per ecCodes
// template.3.shape_of_the_earth.def
// (`unsigned[1] scaleFactorOfRadiusOfSphericalEarth = missing() :
// can_be_missing`); 0xFF is the missing sentinel. The old int8 (two's
// complement) decode was wrong for byte values >= 128 (bug.md §3.3).
func TestProbeEarthScaleFactorUnsigned(t *testing.T) {
	build := func(scale byte, raw uint32) *grid.Definition {
		buf := make([]byte, 72)
		binary.BigEndian.PutUint32(buf[0:4], 72)
		buf[4] = 3
		binary.BigEndian.PutUint32(buf[30:34], 1)
		binary.BigEndian.PutUint32(buf[34:38], 1)
		buf[14] = 1 // shape 1 (spherical, radius given)
		buf[15] = scale
		binary.BigEndian.PutUint32(buf[16:20], raw)
		g, err := grid.New(buf)
		if err != nil {
			t.Fatalf("grid.New(scale=%#x): %v", scale, err)
		}
		return g
	}

	// scale 0: radius = raw.
	if got := build(0, 6371229).Radius(); got != 6371229 {
		t.Errorf("shape 1 scale 0: Radius() = %v, want 6371229", got)
	}
	// scale 1 (unsigned): radius = raw / 10.
	if got := build(1, 63712290).Radius(); got != 6371229 {
		t.Errorf("shape 1 scale 1: Radius() = %v, want 6371229", got)
	}
	// scale 0x81 = unsigned 129 (NOT sign-magnitude -1): raw × 10^-129 ≈ 0.
	if got := build(0x81, 6371229).Radius(); got != 6371229*math.Pow(10, -129) {
		t.Errorf("shape 1 scale 0x81: Radius() = %v, want raw×10^-129 (unsigned 129)", got)
	}
	// scale 0xFF = missing -> NaN (fail-loud by absence).
	if got := build(0xFF, 6371229).Radius(); !math.IsNaN(got) {
		t.Errorf("shape 1 scale 0xFF (missing): Radius() = %v, want NaN", got)
	}
}

func TestProbeBasicAngleMissing(t *testing.T) {
	buf := make([]byte, 72)
	binary.BigEndian.PutUint32(buf[0:4], 72)
	buf[4] = 3
	binary.BigEndian.PutUint32(buf[30:34], 1)
	binary.BigEndian.PutUint32(buf[34:38], 1)
	buf[14] = 6
	binary.BigEndian.PutUint32(buf[38:42], 0xFFFFFFFF) // basic angle = missing
	binary.BigEndian.PutUint32(buf[42:46], 1000000)    // subdivisions
	binary.BigEndian.PutUint32(buf[46:50], 45000000)
	binary.BigEndian.PutUint32(buf[50:54], 10000000)
	binary.BigEndian.PutUint32(buf[55:59], 45000000)
	binary.BigEndian.PutUint32(buf[59:63], 11000000)
	binary.BigEndian.PutUint32(buf[63:67], 1000000)
	binary.BigEndian.PutUint32(buf[67:71], 0)
	g, err := grid.New(buf)
	if err != nil {
		t.Fatalf("grid.New: %v", err)
	}
	lat, lon, err := g.Point(0)
	t.Logf("basicAngle missing (all ones): Point(0) = (%v, %v) err=%v (expected 45, 10)", lat, lon, err)
	if math.Abs(lat-45) > 1e-9 || math.Abs(lon-10) > 1e-9 {
		t.Errorf("missing basic angle not treated as default 1/1e6: got (%v, %v)", lat, lon)
	}
}

// Di/Dj are unsigned[4]: WMO template 3.0 Note 5 says "It is recommended to
// use unsigned direction increments", and ecCodes declares them unsigned[4]
// (template.3.latlon.def). The scan direction lives in the scanning mode.
// A two's-complement/sign-magnitude "negative" increment therefore decodes to
// a huge positive value and is rejected fail-loud by the span check rather
// than silently misplacing the grid (bug.md §3.1 — the report's proposal to
// treat Di as signed contradicts both sources).
func TestProbeDiUnsignedIncrement(t *testing.T) {
	build := func(diRaw uint32) (*grid.Definition, error) {
		buf := make([]byte, 72)
		binary.BigEndian.PutUint32(buf[0:4], 72)
		buf[4] = 3
		binary.BigEndian.PutUint32(buf[30:34], 3) // Ni = 3
		binary.BigEndian.PutUint32(buf[34:38], 1) // Nj = 1
		buf[14] = 6
		binary.BigEndian.PutUint32(buf[38:42], 1)
		binary.BigEndian.PutUint32(buf[42:46], 1000000)
		binary.BigEndian.PutUint32(buf[46:50], 0)         // La1 = 0
		binary.BigEndian.PutUint32(buf[50:54], 0)         // Lo1 = 0
		binary.BigEndian.PutUint32(buf[55:59], 0)         // La2 = 0
		binary.BigEndian.PutUint32(buf[59:63], 2_000_000) // Lo2 = +2
		binary.BigEndian.PutUint32(buf[63:67], diRaw)
		binary.BigEndian.PutUint32(buf[67:71], 0)
		buf[71] = 64 // scanMode: +i, -j
		return grid.New(buf)
	}

	// Unsigned Di = +1°, consistent with the Lo1..Lo2 span -> accepted.
	g, err := build(1_000_000)
	if err != nil {
		t.Fatalf("grid.New(unsigned Di=+1): %v", err)
	}
	lons, err := g.Lons()
	if err != nil {
		t.Fatalf("Lons(): %v", err)
	}
	if len(lons) != 3 || math.Abs(lons[1]-1) > 1e-9 || math.Abs(lons[2]-2) > 1e-9 {
		t.Errorf("Lons() = %v, want [0 1 2]", lons)
	}

	// A sign-magnitude "negative" Di (0x80000001) decodes to ~2147° under the
	// unsigned rule and must be rejected, not silently accepted.
	if _, err := build(0x80000001); err == nil {
		t.Errorf("grid.New accepted a sign-magnitude negative Di; want a fail-loud span error")
	} else {
		t.Logf("sign-magnitude Di rejected fail-loud: %v", err)
	}
}

// ---------------------------------------------------------------------------
// product level scale sign
// ---------------------------------------------------------------------------

func TestProbeLevelScaleSign(t *testing.T) {
	sec := make([]byte, 34)
	binary.BigEndian.PutUint32(sec[0:4], 34)
	sec[4] = 4
	binary.BigEndian.PutUint16(sec[5:7], 0)
	binary.BigEndian.PutUint16(sec[7:9], 0)
	sec[9] = 2
	sec[10] = 1
	sec[11] = 1
	sec[17] = 1
	binary.BigEndian.PutUint32(sec[18:22], 0)
	sec[22] = 1    // level type = isobaric surface
	sec[23] = 0x81 // scale = sign-magnitude -1 -> value * 10
	binary.BigEndian.PutUint32(sec[24:28], 850)
	msg := assemble(0, [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec, sec5Simple(1, 0, 0, 0, 1, 0), sec7(nil)})
	f, err := openBytes(msg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	m, err := f.Message(0)
	if err != nil {
		t.Fatalf("message: %v", err)
	}
	p, err := m.Product()
	if err != nil {
		t.Fatalf("product: %v", err)
	}
	t.Logf("level scale byte 0x81, raw 850 -> Scale=%v (expected 8500 if sign-magnitude)", p.Level1.Scale)
	if math.Abs(p.Level1.Scale-8500) > 1e-6 {
		t.Errorf("level scale sign convention mismatch: got %v", p.Level1.Scale)
	}
}

// ---------------------------------------------------------------------------
// multi-field envelope
// ---------------------------------------------------------------------------

func TestProbeMultiFieldEnvelope(t *testing.T) {
	// s0 | s1 s3 | [s4 s5 s7] x 2  (section 6 omitted is allowed)
	g := sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil)
	s4a := sec4()
	s5a := sec5Simple(1, 0, 0, 0, 1, 0)
	s7a := sec7([]byte{1})
	s4b := sec4()
	s5b := sec5Simple(1, 0, 0, 0, 1, 0)
	s7b := sec7([]byte{2})

	secs := [][]byte{sec1(), g, s4a, s5a, s7a, s4b, s5b, s7b}
	total := 16 + 4
	for _, s := range secs {
		total += len(s)
	}
	out := make([]byte, 16)
	copy(out[0:4], "GRIB")
	out[4], out[5] = 0xFF, 0xFF
	out[6] = 0
	out[7] = 2
	binary.BigEndian.PutUint64(out[8:16], uint64(total))
	for _, s := range secs {
		out = append(out, s...)
	}
	out = append(out, 0x37, 0x37, 0x37, 0x37)

	f, err := openBytes(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	n, _ := f.NumMessages()
	t.Logf("multi-field envelope -> %d logical messages", n)
	for i := 0; i < n; i++ {
		m, err := f.Message(i)
		if err != nil {
			t.Logf("message %d: %v", i, err)
			continue
		}
		v, err := m.Values()
		t.Logf("message %d sections=%v size=%d values=%v err=%v", i, m.Sections(), m.Size(), v, err)
	}
}

// ---------------------------------------------------------------------------
// section structure edge cases
// ---------------------------------------------------------------------------

func TestProbeSectionEdges(t *testing.T) {
	cases := []struct {
		name string
		secs [][]byte
	}{
		{"missing section 7", [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0)}},
		{"missing section 3", [][]byte{sec1(), sec4(), sec5Simple(1, 0, 0, 0, 1, 0), sec7(nil)}},
		{"section 8 before 7", [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0), secLocal(8), sec7(nil)}},
		{"section 0 inside", [][]byte{sec1(), secLocal(0), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0), sec7(nil)}},
		{"duplicate section 4", [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), secLocal(4), sec5Simple(1, 0, 0, 0, 1, 0), sec7(nil)}},
		{"5 trailing bytes", nil},
	}
	for _, c := range cases {
		if c.secs == nil {
			continue
		}
		_, err := openBytes(assemble(0, c.secs))
		t.Logf("%-22s -> %v", c.name, err)
	}

	// 5 trailing bytes after section 7
	msg := assemble(0, [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0), sec7(nil)})
	msg = append(msg, 1, 2, 3, 4, 5)
	binary.BigEndian.PutUint64(msg[8:16], uint64(len(msg)))
	_, err := openBytes(msg)
	t.Logf("5 trailing bytes            -> %v", err)
}

func secLocal(num int) []byte {
	buf := make([]byte, 5)
	binary.BigEndian.PutUint32(buf[0:4], 5)
	buf[4] = byte(num)
	return buf
}

// ---------------------------------------------------------------------------
// spec local registration mutates the caller's definition
// ---------------------------------------------------------------------------

// RegisterLocalTable must validate and then store a COPY of the caller's
// definition: the caller's *TemplateDef is never mutated and never shared, so
// two goroutines can register different templates safely (bug.md §4.3).
func TestProbeRegisterLocalTableCopy(t *testing.T) {
	def := &spec.TemplateDef{Name: "local", Fields: []spec.Field{{Start: 10, Count: 1, Kind: spec.KindU8}}}
	if err := spec.RegisterLocalTable(5, 253, def); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, ok := spec.Template(5, 253)
	if !ok {
		t.Fatal("5.253 not registered")
	}
	if got == def {
		t.Errorf("registry kept the caller's pointer; want a copy")
	}
	if def.ID != "" {
		t.Errorf("caller's def was mutated: ID = %q, want empty", def.ID)
	}
	if got.ID != "5.253" {
		t.Errorf("registered copy ID = %q, want 5.253", got.ID)
	}
	// Mutating the caller's struct after registration must not corrupt the
	// registry.
	def.Name = "mutated"
	if again, _ := spec.Template(5, 253); again.Name != "local" {
		t.Errorf("registry reflects caller mutation: Name = %q", again.Name)
	}

	// The registered def is validated at registration: an incoherent field
	// range is rejected up front rather than surfacing later from NewWalk.
	if err := spec.RegisterLocalTable(5, 252, &spec.TemplateDef{Fields: []spec.Field{{Start: 10, Count: 3, Kind: spec.KindU8}}}); err == nil {
		t.Errorf("RegisterLocalTable accepted a width-mismatched field")
	}
	// A well-formed registered def parses; a section too short still fails loud.
	w, err := spec.NewWalk(got, []byte{0, 0, 0, 11, 5, 1})
	if err != nil {
		t.Logf("NewWalk on a section shorter than the field -> %v", err)
	}
	if w != nil {
		t.Errorf("NewWalk accepted an out-of-range field: %+v", w.At(0))
	}
}

// ---------------------------------------------------------------------------
// Table.Lookup with out-of-range codes
// ---------------------------------------------------------------------------

func TestProbeTableLookupNegative(t *testing.T) {
	tbl, ok := spec.TableByID("4.1")
	if !ok {
		t.Fatal("no table 4.1")
	}
	e, ok := tbl.Lookup(-1)
	t.Logf("Lookup(-1) -> %v ok=%v", e, ok)
	if ok {
		t.Errorf("negative code matched an entry")
	}
}

// ---------------------------------------------------------------------------
// concurrency: race detector over ValuesAll
// ---------------------------------------------------------------------------

func TestProbeValuesAllRace(t *testing.T) {
	msg := assemble(0, [][]byte{sec1(), sec3(4, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(4, 0, 0, 0, 8, 0), sec7([]byte{1, 2, 3, 4})})
	f, err := openBytes(msg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	// Two concurrent walks over the same File.
	done := make(chan int, 8)
	for k := 0; k < 8; k++ {
		go func(k int) {
			n, _ := f.NumMessages()
			for i := 0; i < n; i++ {
				m, err := f.Message(i)
				if err != nil {
					continue
				}
				v, err := m.Values()
				if err != nil {
					continue
				}
				_ = v
			}
			done <- k
		}(k)
	}
	for k := 0; k < 8; k++ {
		<-done
	}
	t.Log("concurrent walk complete")
}

// ---------------------------------------------------------------------------
// writer round-trip with a grid the reader cannot compute
// ---------------------------------------------------------------------------

func TestProbeWriterUnimplementedGrid(t *testing.T) {
	// grid template 3.10 (unimplemented) -> NumPoints() == 0, so Build falls
	// back to the declared count.
	buf := make([]byte, 72)
	binary.BigEndian.PutUint32(buf[0:4], 72)
	buf[4] = 3
	binary.BigEndian.PutUint32(buf[6:10], 10)
	binary.BigEndian.PutUint32(buf[30:34], 5)
	binary.BigEndian.PutUint32(buf[34:38], 2)
	binary.BigEndian.PutUint16(buf[12:14], 10)
	m := gribw.New()
	if err := m.SetIdentification(&grib.Identification{Centre: 98, ReferenceTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("id: %v", err)
	}
	if err := m.SetProduct(&grib.Product{TemplateNumber: 0, ForecastTime: &grib.ForecastTime{Unit: 1, Value: 0}}); err != nil {
		t.Fatalf("product: %v", err)
	}
	if err := m.SetGridBytes(buf); err != nil {
		t.Fatalf("SetGridBytes: %v", err)
	}
	sec5, payload, err := gribw.EncodeSimple([]float32{0, 1, 0, 1, 0, 1, 0, 1, 0, 1}, gribw.SimpleParams{Bits: 1})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	_ = m.SetDataSection(sec5, payload)
	_, err = m.Build()
	t.Logf("Build with unimplemented grid template -> %v", err)
}
