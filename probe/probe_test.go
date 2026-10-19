// Package probe is an independent black-box test harness used by the test
// engineer to hunt for bugs. It does not fix anything; findings are written to
// bug.md. Run with: go test ./probe -count=1
package probe

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
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
// helpers
// ---------------------------------------------------------------------------

func sm32(v int64) uint32 {
	if v < 0 {
		return 0x80000000 | uint32(-v)
	}
	return uint32(v)
}

// sec3 builds a template 3.0 section (72 bytes) with explicit raw fields.
func sec3(ni, nj uint32, la1, lo1, la2, lo2, di, dj int64, scanMode uint8, pointList []byte) []byte {
	buf := make([]byte, 72+len(pointList))
	binary.BigEndian.PutUint32(buf[0:4], uint32(len(buf)))
	buf[4] = 3
	buf[5] = 0
	binary.BigEndian.PutUint32(buf[6:10], ni*nj)
	buf[10] = 4
	buf[11] = 0
	binary.BigEndian.PutUint16(buf[12:14], 0)
	buf[14] = 6
	binary.BigEndian.PutUint32(buf[30:34], ni)
	binary.BigEndian.PutUint32(buf[34:38], nj)
	binary.BigEndian.PutUint32(buf[38:42], 1)
	binary.BigEndian.PutUint32(buf[42:46], 1000000)
	binary.BigEndian.PutUint32(buf[46:50], sm32(la1))
	binary.BigEndian.PutUint32(buf[50:54], sm32(lo1))
	buf[54] = 0
	binary.BigEndian.PutUint32(buf[55:59], sm32(la2))
	binary.BigEndian.PutUint32(buf[59:63], sm32(lo2))
	binary.BigEndian.PutUint32(buf[63:67], sm32(di))
	binary.BigEndian.PutUint32(buf[67:71], sm32(dj))
	buf[71] = scanMode
	copy(buf[72:], pointList)
	return buf
}

func sec1() []byte {
	buf := make([]byte, 21)
	binary.BigEndian.PutUint32(buf[0:4], 21)
	buf[4] = 1
	binary.BigEndian.PutUint16(buf[5:7], 98)
	binary.BigEndian.PutUint16(buf[7:9], 0)
	buf[9] = 1
	buf[10] = 0
	binary.BigEndian.PutUint16(buf[12:14], 2026)
	buf[14] = 1
	buf[15] = 1
	buf[16] = 0
	buf[17] = 0
	buf[18] = 0
	buf[19] = 0
	buf[20] = 0
	return buf
}

func sec4() []byte {
	buf := make([]byte, 34)
	binary.BigEndian.PutUint32(buf[0:4], 34)
	buf[4] = 4
	binary.BigEndian.PutUint16(buf[5:7], 0)
	binary.BigEndian.PutUint16(buf[7:9], 0)
	buf[9] = 2  // category
	buf[10] = 1 // number
	buf[11] = 1 // generating process
	buf[17] = 1 // forecast time unit = hour
	binary.BigEndian.PutUint32(buf[18:22], 0)
	buf[22] = 1 // level type
	buf[23] = 0 // scale
	binary.BigEndian.PutUint32(buf[24:28], 850)
	buf[28] = 255
	buf[29] = 0
	binary.BigEndian.PutUint32(buf[30:34], 0xFFFFFFFF)
	return buf
}

// sec5Simple builds a template 5.0 section (21 bytes).
func sec5Simple(numValues uint32, R float32, E, D uint16, bits uint8, origType uint8) []byte {
	buf := make([]byte, 21)
	binary.BigEndian.PutUint32(buf[0:4], 21)
	buf[4] = 5
	binary.BigEndian.PutUint32(buf[5:9], numValues)
	binary.BigEndian.PutUint16(buf[9:11], 0)
	binary.BigEndian.PutUint32(buf[11:15], math.Float32bits(R))
	binary.BigEndian.PutUint16(buf[15:17], E)
	binary.BigEndian.PutUint16(buf[17:19], D)
	buf[19] = bits
	buf[20] = origType
	return buf
}

// sec5T53 builds a template 5.3 section (49 bytes).
func sec5T53(numValues uint32, ng, refLen, trueLast uint32, lenInc, widthBits, scaledLenBits, osd, noed uint8) []byte {
	buf := make([]byte, 49)
	binary.BigEndian.PutUint32(buf[0:4], 49)
	buf[4] = 5
	binary.BigEndian.PutUint32(buf[5:9], numValues)
	binary.BigEndian.PutUint16(buf[9:11], 3)
	binary.BigEndian.PutUint32(buf[11:15], math.Float32bits(0))
	binary.BigEndian.PutUint16(buf[15:17], 0) // B
	binary.BigEndian.PutUint16(buf[17:19], 0) // D
	buf[19] = 8                               // bits per value
	buf[20] = 0                               // orig type
	buf[21] = 0                               // group split method
	buf[22] = 0                               // missing value management
	binary.BigEndian.PutUint32(buf[23:27], 0)
	binary.BigEndian.PutUint32(buf[27:31], 0)
	binary.BigEndian.PutUint32(buf[31:35], ng)
	buf[35] = 0 // reference group width
	buf[36] = widthBits
	binary.BigEndian.PutUint32(buf[37:41], refLen)
	buf[41] = lenInc
	binary.BigEndian.PutUint32(buf[42:46], trueLast)
	buf[46] = scaledLenBits
	buf[47] = osd
	buf[48] = noed
	return buf
}

func sec6(indicator uint8, bitmap []byte) []byte {
	buf := make([]byte, 6+len(bitmap))
	binary.BigEndian.PutUint32(buf[0:4], uint32(len(buf)))
	buf[4] = 6
	buf[5] = indicator
	copy(buf[6:], bitmap)
	return buf
}

func sec7(payload []byte) []byte {
	buf := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(buf[0:4], uint32(len(buf)))
	buf[4] = 7
	copy(buf[5:], payload)
	return buf
}

// assemble builds a complete message from section bytes (each already has its
// own 4-octet length prefix), plus the "7777" tail.
func assemble(discipline int, secs [][]byte) []byte {
	total := 16 + 4
	for _, s := range secs {
		total += len(s)
	}
	out := make([]byte, 16)
	copy(out[0:4], "GRIB")
	out[4], out[5] = 0xFF, 0xFF
	out[6] = byte(discipline)
	out[7] = 2
	binary.BigEndian.PutUint64(out[8:16], uint64(total))
	for _, s := range secs {
		out = append(out, s...)
	}
	out = append(out, 0x37, 0x37, 0x37, 0x37)
	return out
}

func openBytes(b []byte) (*grib.File, error) {
	return grib.OpenReader(byteReaderAt(b), int64(len(b)))
}

type byteReaderAt []byte

func (b byteReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(b)) {
		return 0, fmt.Errorf("read at %d out of range", off)
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, fmt.Errorf("short read")
	}
	return n, nil
}

func guard(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC in %s: %v", name, r)
		}
	}()
	fn()
}

// ---------------------------------------------------------------------------
// 1. panic hunt: complex packing with zero values
// ---------------------------------------------------------------------------

func TestProbeComplexZeroValuesPanic(t *testing.T) {
	def, ok := spec.Template(5, 3)
	if !ok {
		t.Fatal("no template 5.3 def")
	}

	for _, osd := range []uint8{1, 2} {
		sec5 := sec5T53(0, 1, 0, 0, 0, 0, 0, osd, 1)
		w, err := spec.NewWalk(def, sec5)
		if err != nil {
			t.Fatalf("walk: %v", err)
		}
		// header: osd extra descriptors + bias = (1+osd) bytes, then 1 ref byte
		payload := make([]byte, 1+int(osd)+1)
		ctx := &pack.Context{
			TemplateNumber: 3,
			Fields:         w,
			Data:           payload,
			NumValues:      0,
			Bitmap:         nil,
			GridPoints:     2,
		}
		guard(t, fmt.Sprintf("complexDecode order=%d numValues=0", osd), func() {
			_, err := pack.Unpack(ctx)
			t.Logf("order=%d numValues=0 -> err=%v", osd, err)
		})
	}
}

// end-to-end through the public API: bitmap popcount 0 -> NumValues 0
func TestProbeComplexZeroValuesViaValues(t *testing.T) {
	g := sec3(2, 1, 45e6, 10e6, 45e6, 11e6, 1e6, 0, 64, nil)
	msg := assemble(0, [][]byte{sec1(), g, sec4(), sec5T53(0, 1, 0, 0, 0, 0, 0, 2, 1), sec6(0, []byte{0x00}), sec7([]byte{3, 0, 0, 0})})
	f, err := openBytes(msg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	guard(t, "Message.Values() complex order=2 zero values", func() {
		m, err := f.Message(0)
		if err != nil {
			t.Fatalf("message: %v", err)
		}
		_, err = m.Values()
		t.Logf("Values() -> %v", err)
	})
}

// ---------------------------------------------------------------------------
// 2. unbounded allocation / integer overflow in grid point counts
// ---------------------------------------------------------------------------

func TestProbeGridPointCountOverflow(t *testing.T) {
	// Ni = Nj = 4e9 with Di/Dj missing: the product 1.6e19 would overflow int.
	// grid.New must reject it fail-loud rather than return a negative count or
	// drive a makeslice panic (bug.md §1.3).
	g := sec3(4000000000, 4000000000, 0, 0, 0, 0, 4294967295, 4294967295, 64, nil)
	if _, err := grid.New(g); err == nil {
		t.Errorf("grid.New accepted Ni=Nj=4e9; want a fail-loud point-count error")
	} else {
		t.Logf("grid.New rejected Ni=Nj=4e9: %v", err)
	}

	// A large-but-supported grid still works, and Point() is bounded.
	ok := sec3(1000, 1000, 0, 0, 0, 0, 4294967295, 4294967295, 64, nil)
	def, err := grid.New(ok)
	if err != nil {
		t.Fatalf("grid.New(1000x1000): %v", err)
	}
	if n := def.NumPoints(); n != 1_000_000 {
		t.Errorf("NumPoints() = %d, want 1000000", n)
	}
	if _, _, err := def.Point(0); err != nil {
		t.Errorf("Point(0) on a supported grid: %v", err)
	}
}

func TestProbeReducedPointListUnbounded(t *testing.T) {
	// Ni missing, Nj = 3, point list width 4, entries = 0xFFFFFFFF each: the
	// sum (~1.3e10) must be rejected fail-loud (bug.md §1.4).
	list := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	g := sec3(0xFFFFFFFF, 3, 0, 0, 0, 0, 4294967295, 4294967295, 64, list)
	if _, err := grid.New(g); err == nil {
		t.Errorf("grid.New accepted a reduced point list summing to ~1.3e10 points; want a fail-loud error")
	} else {
		t.Logf("grid.New rejected the unbounded reduced point list: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 3. gribw writer: short section 5 -> index out of range
// ---------------------------------------------------------------------------

func TestProbeGribwShortSection5(t *testing.T) {
	// A section 5 that is exactly the minimum accepted by SetDataSection (11 bytes).
	sec5 := make([]byte, 11)
	binary.BigEndian.PutUint32(sec5[0:4], 11)
	sec5[4] = 5
	binary.BigEndian.PutUint32(sec5[5:9], 1)
	binary.BigEndian.PutUint16(sec5[9:11], 0) // template 5.0

	m := gribw.New()
	if err := m.SetIdentification(&grib.Identification{Centre: 98, ReferenceTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("id: %v", err)
	}
	if err := m.SetGridBytes(sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil)); err != nil {
		t.Fatalf("grid: %v", err)
	}
	if err := m.SetProduct(&grib.Product{TemplateNumber: 0, ForecastTime: &grib.ForecastTime{Unit: 1, Value: 0}}); err != nil {
		t.Fatalf("product: %v", err)
	}
	if err := m.SetDataSection(sec5, nil); err != nil {
		t.Fatalf("data section: %v", err)
	}
	guard(t, "gribw.Build with 11-byte section 5 (template 5.0)", func() {
		_, err := m.Build()
		t.Logf("Build() -> %v", err)
	})

	// same for template 5.4 (reads sec5[11])
	binary.BigEndian.PutUint16(sec5[9:11], 4)
	guard(t, "gribw.Build with 11-byte section 5 (template 5.4)", func() {
		_, err := m.Build()
		t.Logf("Build() -> %v", err)
	})
}

func TestProbeGribwSection3LengthPrefixNotChecked(t *testing.T) {
	// grid.New does not verify the 4-octet length prefix against the actual
	// section size, unlike SetDataSection which does for section 5.
	bad := sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil)
	binary.BigEndian.PutUint32(bad[0:4], 999) // wrong length field
	m := gribw.New()
	_ = m.SetIdentification(&grib.Identification{Centre: 98, ReferenceTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err := m.SetGridBytes(bad); err != nil {
		t.Logf("SetGridBytes rejected (good): %v", err)
		return
	}
	_ = m.SetProduct(&grib.Product{TemplateNumber: 0, ForecastTime: &grib.ForecastTime{Unit: 1, Value: 0}})
	sec5, payload, err := gribw.EncodeSimple([]float32{1}, gribw.SimpleParams{Bits: 1})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	_ = m.SetDataSection(sec5, payload)
	out, err := m.Build()
	if err != nil {
		t.Logf("Build rejected: %v", err)
		return
	}
	t.Errorf("Build accepted a section 3 whose length prefix (999) != actual size (%d); round-trip read: %v", len(bad), roundTripErr(out))
}

func roundTripErr(b []byte) error {
	f, err := openBytes(b)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Message(0)
	return err
}

// ---------------------------------------------------------------------------
// 4. JSON output cannot encode NaN / Inf
// ---------------------------------------------------------------------------

// Non-finite IEEE values (template 5.4) must survive decoding as real NaN/Inf
// so callers can decide what to do with them. encoding/json cannot represent
// them at all, which is why the gribdump CLI wraps float32 values in a
// JSON-safe type that emits "NaN"/"+Inf"/"-Inf" (bug.md §2.6;
// cmd/gribdump/main.go float32Val).
func TestProbeNonFiniteValues(t *testing.T) {
	path := filepath.Join("..", "testdata", "p6-b-xcheck", "d4_ieee_extremes.grib2")
	f, err := grib.Open(path)
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	defer f.Close()
	m, err := f.Message(0)
	if err != nil {
		t.Fatalf("message: %v", err)
	}
	vals, err := m.Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	var nan, inf bool
	for _, v := range vals {
		if math.IsNaN(float64(v)) {
			nan = true
		}
		if math.IsInf(float64(v), 0) {
			inf = true
		}
	}
	if !nan || !inf {
		t.Errorf("IEEE-extremes fixture decoded without NaN/Inf: nan=%v inf=%v (%v)", nan, inf, vals)
	}
	// The stdlib marshaler rejects these — the reason the CLI needs a wrapper.
	if _, err := json.Marshal(vals); err == nil {
		t.Errorf("json.Marshal of non-finite float32 unexpectedly succeeded")
	}
}

// ---------------------------------------------------------------------------
// 5. forecast time overflow
// ---------------------------------------------------------------------------

func TestProbeForecastTimeOverflow(t *testing.T) {
	// raw = 4e9 hours -> 4e9 * time.Hour overflows int64 nanoseconds.
	def, ok := spec.Template(4, 0)
	if !ok {
		t.Fatal("no template 4.0")
	}
	sec := sec4()
	binary.BigEndian.PutUint32(sec[18:22], 4000000000)
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
	if p.ForecastTime == nil {
		t.Fatal("no forecast time")
	}
	t.Logf("forecast time raw=4e9 hours -> %v (int64 ns overflow; 4e9 h = 4.5e17 ns fits, so check 4e9*3600e9)", p.ForecastTime.Value)

	// bigger: raw = 2.5e9 days -> 2.5e9 * 24h = 6e16 ns, fits.
	// Use unit=0 (minute) with raw = MaxUint32: 4.29e9 * 60e9 = 2.57e20 ns > MaxInt64.
	sec[17] = 0
	binary.BigEndian.PutUint32(sec[18:22], 0xFFFFFFFF)
	p2, err := m.Product()
	if err != nil {
		t.Fatalf("product2: %v", err)
	}
	t.Logf("forecast time raw=MaxUint32 minutes -> %v (want ~2.57e20 ns, MaxInt64 = 9.22e18)", p2.ForecastTime.Value)
	if p2.ForecastTime.Value < 0 {
		t.Errorf("forecast time overflowed to negative duration: %v", p2.ForecastTime.Value)
	}
	_ = def
}

// ---------------------------------------------------------------------------
// 6. spec.Scale silent clamp
// ---------------------------------------------------------------------------

// spec.Scale honours the full signed 1-octet range: a scale factor of 127
// yields 10^-127, not a silent clamp to 10^-9 (bug.md §2.1).
func TestProbeScaleFullRange(t *testing.T) {
	if got, want := spec.Scale(1, 127), 1/math.Pow(10, 127); got != want {
		t.Errorf("spec.Scale(1,127) = %v, want %v (must not clamp)", got, want)
	}
	if got, want := spec.Scale(1, -127), math.Pow(10, 127); got != want {
		t.Errorf("spec.Scale(1,-127) = %v, want %v (must not clamp)", got, want)
	}
	// The 1-octet sign-magnitude decode helpers (ecCodes convention).
	if got := spec.Signed8(0x81); got != -1 {
		t.Errorf("spec.Signed8(0x81) = %d, want -1", got)
	}
}

// ---------------------------------------------------------------------------
// 7. code table lookups used by gribdump
// ---------------------------------------------------------------------------

func TestProbeGribdumpTableReferences(t *testing.T) {
	// gribdump resolves typeOfGeneratingProcess with table 4.2 instead of 4.3.
	def, ok := spec.Template(4, 0)
	if !ok {
		t.Fatal("no template 4.0")
	}
	var genTable string
	for _, f := range def.Fields {
		if f.Start+1 == 12 {
			genTable = fmt.Sprintf("%d.%d", f.TableSec, f.TableFlag)
		}
	}
	t.Logf("template 4.0 octet 12 table reference = %s", genTable)
	if genTable != "4.3" {
		t.Errorf("unexpected table ref for octet 12: %s", genTable)
	}
	if t43, ok := spec.TableByID("4.3"); ok {
		if e, ok := t43.Lookup(0); ok {
			t.Logf("table 4.3 code 0 = %q", e.Name)
		}
	}
	if t42, ok := spec.TableByID("4.2"); ok {
		if e, ok := t42.Lookup(0); ok {
			t.Logf("table 4.2 code 0 = %q (this is what gribdump prints for typeOfGeneratingProcess)", e.Name)
		} else {
			t.Logf("table 4.2 has no code 0 -> gribdump prints no name for typeOfGeneratingProcess")
		}
	}

	// section 1: which table does WMO assign to octet 20 / 21?
	t13, ok13 := spec.TableByID("1.3")
	t14, ok14 := spec.TableByID("1.4")
	if ok13 {
		t.Logf("table 1.3 title = %q", t13.Title)
	}
	if ok14 {
		t.Logf("table 1.4 title = %q", t14.Title)
	}
}

// ---------------------------------------------------------------------------
// 8. fuzz: no panic on malformed input
// ---------------------------------------------------------------------------

func TestProbeFuzzNoPanic(t *testing.T) {
	base := assemble(0, [][]byte{
		sec1(),
		sec3(2, 2, 45e6, 10e6, 44e6, 11e6, 1e6, -1e6, 64, nil),
		sec4(),
		sec5Simple(4, 0, 0, 0, 8, 0),
		sec7([]byte{1, 2, 3, 4}),
	})
	rng := rand.New(rand.NewSource(1))
	panics := 0
	for iter := 0; iter < 20000; iter++ {
		b := make([]byte, len(base))
		copy(b, base)
		nmut := 1 + rng.Intn(6)
		for k := 0; k < nmut; k++ {
			b[rng.Intn(len(b))] = byte(rng.Intn(256))
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					panics++
					t.Errorf("PANIC (iter %d): %v", iter, r)
				}
			}()
			f, err := openBytes(b)
			if err != nil {
				return
			}
			n, _ := f.NumMessages()
			for i := 0; i < n && i < 4; i++ {
				m, err := f.Message(i)
				if err != nil {
					continue
				}
				_, _ = m.Identification()
				_, _ = m.Product()
				_, _ = m.DataRep()
				g, err := m.Grid()
				if err == nil && g != nil {
					_, _, _ = g.Point(0)
					_, _ = g.Lats()
					_, _ = g.Lons()
				}
			}
			f.Close()
		}()
		if panics > 5 {
			break
		}
	}
	if panics == 0 {
		t.Log("fuzz: no panics in 20000 mutations")
	}
}

func TestProbeTruncationSweep(t *testing.T) {
	base := assemble(0, [][]byte{
		sec1(),
		sec3(2, 2, 45e6, 10e6, 44e6, 11e6, 1e6, -1e6, 64, nil),
		sec4(),
		sec5Simple(4, 0, 0, 0, 8, 0),
		sec7([]byte{1, 2, 3, 4}),
	})
	for cut := 0; cut < len(base); cut++ {
		b := base[:cut]
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("PANIC at cut %d: %v", cut, r)
				}
			}()
			f, err := openBytes(b)
			if err != nil {
				return
			}
			n, _ := f.NumMessages()
			for i := 0; i < n; i++ {
				_, _ = f.Message(i)
			}
			f.Close()
		}()
	}
	t.Log("truncation sweep complete")
}

// ---------------------------------------------------------------------------
// 9. round-trip symmetry
// ---------------------------------------------------------------------------

func TestProbeRoundTripSimple(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 2000; iter++ {
		bits := rng.Intn(33)
		E := rng.Intn(20) - 10
		D := rng.Intn(10) - 5
		R := float32(rng.NormFloat64() * 1000)
		n := 1 + rng.Intn(20)
		vals := make([]float32, n)
		for i := range vals {
			vals[i] = float32(rng.Intn(1<<20) - (1 << 19))
		}
		sec5, payload, err := gribw.EncodeSimple(vals, gribw.SimpleParams{R: R, E: E, D: D, Bits: bits})
		if err != nil {
			continue
		}
		msg := assemble(0, [][]byte{
			sec1(),
			sec3(uint32(n), 1, 0, 0, 0, 0, 0, 0, 64, nil),
			sec4(),
			sec5,
			sec7(payload),
		})
		f, err := openBytes(msg)
		if err != nil {
			t.Errorf("round-trip open failed: %v", err)
			continue
		}
		m, err := f.Message(0)
		if err != nil {
			f.Close()
			t.Errorf("round-trip message failed: %v", err)
			continue
		}
		got, err := m.Values()
		f.Close()
		if err != nil {
			t.Errorf("round-trip decode failed (bits=%d E=%d D=%d): %v", bits, E, D, err)
			continue
		}
		if len(got) != n {
			t.Errorf("round-trip length %d != %d", len(got), n)
		}
	}
	t.Log("round-trip sweep complete")
}

// ---------------------------------------------------------------------------
// 10. bitmap edge cases
// ---------------------------------------------------------------------------

func TestProbeBitmapEdges(t *testing.T) {
	cases := []struct {
		name      string
		indicator uint8
		bitmap    []byte
		numValues uint32
	}{
		{"indicator 0, bitmap shorter than grid", 0, []byte{0xFF}, 3},
		{"indicator 0, bitmap longer than grid", 0, []byte{0xFF, 0xFF}, 8},
		{"indicator 0, bitmap empty", 0, nil, 0},
		{"indicator 254", 254, nil, 0},
		{"indicator 1", 1, nil, 0},
	}
	for _, c := range cases {
		msg := assemble(0, [][]byte{
			sec1(),
			sec3(3, 1, 0, 0, 0, 0, 0, 0, 64, nil),
			sec4(),
			sec5Simple(c.numValues, 0, 0, 0, 1, 0),
			sec6(c.indicator, c.bitmap),
			sec7(nil),
		})
		f, err := openBytes(msg)
		if err != nil {
			t.Logf("%s: open error %v", c.name, err)
			continue
		}
		m, err := f.Message(0)
		if err != nil {
			t.Logf("%s: message error %v", c.name, err)
			f.Close()
			continue
		}
		_, err = m.Values()
		t.Logf("%s -> %v", c.name, err)
		f.Close()
	}
}

// ---------------------------------------------------------------------------
// 11. simple packing boundary
// ---------------------------------------------------------------------------

func TestProbeSimplePackingBoundary(t *testing.T) {
	// bits = 33 with zero values: no error (legacy path only fails on read)
	sec5 := sec5Simple(0, 0, 0, 0, 33, 0)
	w, _ := spec.NewWalk(mustTemplate(5, 0), sec5)
	_, err := pack.Unpack(&pack.Context{TemplateNumber: 0, Fields: w, Data: nil, NumValues: 0, GridPoints: 0})
	t.Logf("bits=33 numValues=0 -> %v", err)

	_, err = pack.Unpack(&pack.Context{TemplateNumber: 0, Fields: w, Data: nil, NumValues: 1, GridPoints: 1})
	t.Logf("bits=33 numValues=1 -> %v", err)

	// origType 2
	sec5b := sec5Simple(1, 0, 0, 0, 1, 2)
	wb, _ := spec.NewWalk(mustTemplate(5, 0), sec5b)
	_, err = pack.Unpack(&pack.Context{TemplateNumber: 0, Fields: wb, Data: []byte{0}, NumValues: 1, GridPoints: 1})
	t.Logf("origType=2 -> %v", err)

	// E sign-magnitude vs two's complement
	sec5c := sec5Simple(1, 0, 0x8006, 0x8002, 8, 0)
	wc, _ := spec.NewWalk(mustTemplate(5, 0), sec5c)
	v, err := pack.Unpack(&pack.Context{TemplateNumber: 0, Fields: wc, Data: []byte{1}, NumValues: 1, GridPoints: 1})
	t.Logf("E=0x8006 D=0x8002 raw=1 -> %v err=%v", v, err)
}

func mustTemplate(sec, num int) *spec.TemplateDef {
	t, ok := spec.Template(sec, num)
	if !ok {
		panic("no template")
	}
	return t
}

// ---------------------------------------------------------------------------
// 12. ValuesAll concurrency
// ---------------------------------------------------------------------------

func TestProbeValuesAll(t *testing.T) {
	msgs := assemble(0, [][]byte{sec1(), sec3(2, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(2, 0, 0, 0, 8, 0), sec7([]byte{1, 2})})
	one := msgs[:len(msgs)-4] // drop tail? keep as is
	_ = one
	data := append([]byte{}, msgs...)
	f, err := openBytes(data)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	got, err := f.ValuesAll(8)
	if err != nil {
		t.Fatalf("ValuesAll: %v", err)
	}
	t.Logf("ValuesAll(8) -> %v", got)
	if _, err := f.ValuesAll(1, 2); err == nil {
		t.Errorf("ValuesAll with 2 args should fail")
	} else {
		t.Logf("ValuesAll(1,2) -> %v", err)
	}
	if _, err := f.ValuesAll(-3); err != nil {
		t.Errorf("ValuesAll(-3) unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 13. misc API behaviour
// ---------------------------------------------------------------------------

func TestProbeMisc(t *testing.T) {
	// Open on a directory
	_, err := grib.Open("testdata")
	t.Logf("Open(directory) -> %v", err)

	// OpenReader with size 0
	_, err = openBytes(nil)
	t.Logf("OpenReader(nil,0) -> %v", err)

	// message index out of range
	f, err := openBytes(assemble(0, [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0), sec7([]byte{0})}))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	_, err = f.Message(5)
	t.Logf("Message(5) -> %v", err)

	// Walk.At out of range returns zero Value (no error)
	w, err := spec.NewWalk(mustTemplate(5, 0), sec5Simple(1, 0, 0, 0, 1, 0))
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	v := w.At(999)
	t.Logf("Walk.At(999) -> %+v (silent zero value, no error)", v)

	// Walk.Tail out of range
	t.Logf("Walk.Tail(1000) -> %v", w.Tail(1000))

	// section 5 shorter than template requires
	short := make([]byte, 11)
	binary.BigEndian.PutUint32(short[0:4], 11)
	short[4] = 5
	binary.BigEndian.PutUint32(short[5:9], 1)
	binary.BigEndian.PutUint16(short[9:11], 0)
	f2, err := openBytes(assemble(0, [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), short, sec7(nil)}))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f2.Close()
	m, err := f2.Message(0)
	if err != nil {
		t.Fatalf("message: %v", err)
	}
	dr, err := m.DataRep()
	t.Logf("short section 5 DataRep -> %+v err=%v", dr, err)
	_, err = m.Values()
	t.Logf("short section 5 Values -> %v", err)
}
