package grib1

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/knoweather/simple-grib/internal/octetio"
)

// ---- Synthetic message builders -----------------------------------------

// pdsOpts carries the PDS fields the synthetic builders care about; zero values
// are the WMO defaults.
type pdsOpts struct {
	tableVersion int
	centre       int
	genProc      int
	param        int
	levelType    int
	level        int
	yearOfCent   int
	month        int
	day          int
	hour         int
	century      int
	timeUnit     int
	p1, p2       int
	tri          int
	decScale     int
	flags        uint8 // octet 8: 0x80 = GDS present, 0x40 = BMS present
}

// gdsLatLon builds a type 0 (regular latitude/longitude) GDS: Ni×Nj with
// La1/Lo1/La2/Lo2 and Di/Dj encoded in 1/1000-degree units. All signed values are
// written sign-magnitude.
func gdsLatLon(ni, nj int, la1, lo1, la2, lo2, di, dj float64, scanMode uint8) []byte {
	b := make([]byte, 32)
	b[0], b[1], b[2] = 0, 0, 32 // length
	b[3] = 0                    // NV
	b[4] = 255                  // PV
	b[5] = 0                    // data representation type 0
	putU16(b[6:], ni)
	putU16(b[8:], nj)
	putSmag24(b[10:], la1)
	putSmag24(b[13:], lo1)
	b[16] = 0x80 // resolution flags: direction increments given
	putSmag24(b[17:], la2)
	putSmag24(b[20:], lo2)
	putU16(b[23:], int(math.Round(di*1000)))
	putU16(b[25:], int(math.Round(dj*1000)))
	b[27] = scanMode
	return b
}

func putU16(b []byte, v int) {
	_ = b[1]
	b[0] = byte(v >> 8)
	b[1] = byte(v)
}

func putSmag24(b []byte, deg float64) {
	_ = b[2]
	mag := int(math.Round(math.Abs(deg) * 1000))
	v := mag & 0x7FFFFF
	if deg < 0 {
		v |= 0x800000
	}
	b[0] = byte(v >> 16)
	b[1] = byte(v >> 8)
	b[2] = byte(v)
}

// bdsSimple builds a section 4: simple packing with the given E/R/bits and a
// pre-encoded X array.
func bdsSimple(bits int, e int, ibmRef []byte, xs []uint32) []byte {
	payload := packBits(bits, xs)
	// Section length padded to even (GRIB1 rule); the low nibble declares the pad bits.
	n := 11 + len(payload)
	pad := 0
	if n%2 != 0 {
		pad = 8
		payload = append(payload, 0)
		n++
	}
	totalUnused := pad
	if bits > 0 {
		// payload bits − value bits = unused bits (including the even-length pad byte)
		totalUnused = len(payload)*8 - len(xs)*bits
	}
	b := make([]byte, 11, n)
	b[0], b[1], b[2] = byte(n>>16), byte(n>>8), byte(n)
	b[3] = byte(totalUnused & 0x0F) // low nibble = unused bits; high-nibble flags = 0 (simple packing)
	putSmag16(b[4:], e)
	copy(b[6:10], ibmRef)
	b[10] = byte(bits)
	return append(b, payload...)
}

func putSmag16(b []byte, v int) {
	_ = b[1]
	mag := v
	if mag < 0 {
		mag = -mag
	}
	w := uint16(mag & 0x7FFF)
	if v < 0 {
		w |= 0x8000
	}
	b[0] = byte(w >> 8)
	b[1] = byte(w)
}

func packBits(bits int, xs []uint32) []byte {
	if bits == 0 {
		return nil
	}
	var out []byte
	var acc uint64
	nb := 0
	for _, x := range xs {
		acc = acc<<uint(bits) | uint64(x)
		nb += bits
		for nb >= 8 {
			nb -= 8
			out = append(out, byte(acc>>uint(nb)))
		}
	}
	if nb > 0 {
		out = append(out, byte(acc<<uint(8-nb)))
	}
	return out
}

// ibmOf encodes a float64 as IBM float32, used to build known reference values in
// tests. Only values exactly representable in this format (integers and powers-of-
// two combinations such as 240.625) round-trip exactly.
func ibmOf(v float64) []byte {
	b := make([]byte, 4)
	if v == 0 {
		return b
	}
	sign := byte(0)
	if v < 0 {
		sign = 0x80
		v = -v
	}
	exp := 64
	for v >= 1 {
		v /= 16
		exp++
	}
	for v < 1.0/16 {
		v *= 16
		exp--
	}
	m := uint32(math.Round(v * (1 << 24)))
	b[0] = sign | byte(exp&0x7F)
	b[1] = byte(m >> 16)
	b[2] = byte(m >> 8)
	b[3] = byte(m)
	return b
}

// buildMessage assembles a complete GRIB1 message: IS + PDS + [GDS] + [BMS] + BDS + '7777'.
func buildMessage(t *testing.T, o pdsOpts, gds []byte, bms []byte, bds []byte) []byte {
	t.Helper()
	if o.tableVersion == 0 {
		o.tableVersion = 3
	}
	if o.centre == 0 {
		o.centre = 98
	}
	if o.yearOfCent == 0 {
		o.yearOfCent, o.month, o.day, o.hour, o.century = 25, 12, 29, 0, 21
	}
	if o.timeUnit == 0 {
		o.timeUnit = 1
	}

	pds := make([]byte, 28)
	putU24(pds[0:], 28)
	pds[3] = byte(o.tableVersion)
	pds[4] = byte(o.centre)
	pds[5] = byte(o.genProc)
	pds[6] = 255
	pds[7] = o.flags
	pds[8] = byte(o.param)
	pds[9] = byte(o.levelType)
	putU16(pds[10:], o.level)
	pds[12] = byte(o.yearOfCent)
	pds[13] = byte(o.month)
	pds[14] = byte(o.day)
	pds[15] = byte(o.hour)
	pds[16] = 0
	pds[17] = byte(o.timeUnit)
	pds[18] = byte(o.p1)
	pds[19] = byte(o.p2)
	pds[20] = byte(o.tri)
	putU16(pds[21:], 0)
	pds[23] = 0
	pds[24] = byte(o.century)
	pds[25] = 0
	putSmag16(pds[26:], o.decScale)

	body := append([]byte{}, pds...)
	if o.flags&pdsFlagGDS != 0 {
		body = append(body, gds...)
	}
	if o.flags&pdsFlagBMS != 0 {
		body = append(body, bms...)
	}
	body = append(body, bds...)
	body = append(body, '7', '7', '7', '7')

	total := 8 + len(body)
	out := make([]byte, 8, total)
	copy(out[0:4], "GRIB")
	out[4], out[5], out[6] = byte(total>>16), byte(total>>8), byte(total)
	out[7] = Edition
	return append(out, body...)
}

func putU24(b []byte, v int) {
	_ = b[2]
	b[0], b[1], b[2] = byte(v>>16), byte(v>>8), byte(v)
}

// bmsAllPresent builds an "everything present" bitmap section.
func bmsAllPresent(n int) []byte {
	bits := (n + 7) / 8
	unused := bits*8 - n
	b := make([]byte, 6+bits)
	putU24(b[0:], len(b))
	b[3] = 0 // octet 4 (unused-bit count) zeroed first
	b[4], b[5] = 0, 0
	for i := 0; i < n; i++ {
		b[6+i/8] |= 0x80 >> uint(i%8)
	}
	// Section 3 records the trailing unused-bit count in octet 4 — fill it in here.
	b[3] = byte(unused)
	return b
}

// ---- Unit tests ----------------------------------------------------------

func TestIBMFloat32(t *testing.T) {
	// IBM float32 definition: value = (-1)^s × (m/2^24) × 16^(e-64).
	// 0x426F1400 => s=0, e=0x42=66, m=0x6F1400=7279616
	//   = 7279616/16777216 × 16^2 = 0.433887 × 256 = 111.0756…
	got := octetio.IBMFloat32([]byte{0x42, 0x6F, 0x14, 0x00})
	want := float64(0x6F1400) / (1 << 24) * 256
	if math.Abs(got-want) > 1e-12 {
		t.Errorf("ibmFloat32 = %v, want %v", got, want)
	}
	if v := octetio.IBMFloat32([]byte{0x00, 0x00, 0x00, 0x00}); v != 0 {
		t.Errorf("全零应当是 0，得到 %v", v)
	}
	// A zero mantissa decodes to 0 regardless of the exponent (IBM float has no
	// NaN/Inf concept).
	if v := octetio.IBMFloat32([]byte{0x7F, 0x00, 0x00, 0x00}); v != 0 {
		t.Errorf("尾数 0 应当是 0，得到 %v", v)
	}
	// Sign bit
	if v := octetio.IBMFloat32([]byte{0xC2, 0x6F, 0x14, 0x00}); math.Abs(v+want) > 1e-12 {
		t.Errorf("负号没生效：%v", v)
	}
	// Round trip: values this helper encodes must decode back unchanged.
	for _, x := range []float64{1, -1, 0.5, 240.625, 101540, 1e-7, 6367.47} {
		if got := octetio.IBMFloat32(ibmOf(x)); math.Abs(got-x) > math.Abs(x)*1e-6 {
			t.Errorf("ibmOf/ibmFloat32 往返 %v -> %v", x, got)
		}
	}
}

func TestSplitSectionsRejects(t *testing.T) {
	good := buildMessage(t, pdsOpts{param: 11, levelType: 100, level: 500, flags: pdsFlagGDS},
		gdsLatLon(2, 2, 10, 20, 5, 25, 5, 5, 0),
		nil,
		bdsSimple(8, 0, ibmOf(100), []uint32{0, 1, 2, 3}))

	cases := []struct {
		name string
		mut  func([]byte) []byte
	}{
		{"空", func([]byte) []byte { return nil }},
		{"magic 不对", func(b []byte) []byte { c := append([]byte{}, b...); copy(c[0:4], "XRIB"); return c }},
		{"版本号不是 1", func(b []byte) []byte { c := append([]byte{}, b...); c[7] = 2; return c }},
		{"总长度与实长不符", func(b []byte) []byte { c := append([]byte{}, b...); c[4], c[5], c[6] = 0, 0, 8; return c }},
		{"结尾不是 7777", func(b []byte) []byte { c := append([]byte{}, b...); copy(c[len(c)-4:], "8888"); return c }},
		{"PDS 长度过小", func(b []byte) []byte { c := append([]byte{}, b...); putU24(c[8:], 4); return c }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := splitSections(tc.mut(good)); err == nil {
				t.Fatal("坏输入却没有报错")
			}
		})
	}
	if _, err := splitSections(good); err != nil {
		t.Fatalf("好输入被拒: %v", err)
	}
}

func TestParseMetadataAndValues(t *testing.T) {
	// 2×2 grid, 4 points, 8 bits per value, E=0, R=100: expect 100,101,102,103.
	msg := buildMessage(t, pdsOpts{
		param: 11, levelType: 100, level: 500,
		timeUnit: 1, p1: 24, tri: 0, flags: pdsFlagGDS,
	}, gdsLatLon(2, 2, 10, 20, 5, 25, 5, 5, 0), nil,
		bdsSimple(8, 0, ibmOf(100), []uint32{0, 1, 2, 3}))

	m, err := Parse(0, 0, msg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Edition() != 1 {
		t.Errorf("Edition = %d", m.Edition())
	}
	id := m.Identification()
	if id.Centre != 98 || id.TableVersion != 3 {
		t.Errorf("identification = %+v", id)
	}
	if got := id.ReferenceTime.Format("2006-01-02T15:04"); got != "2025-12-29T00:00" {
		t.Errorf("参考时间 = %s（世纪换算应当是 (21-1)*100+25 = 2025）", got)
	}
	p := m.Product()
	if p.Parameter != 11 || p.LevelType != 100 || p.LevelTop != 500 {
		t.Errorf("product = %+v", p)
	}
	if d, ok := p.ForecastTime(); !ok || d.Hours() != 24 {
		t.Errorf("预报时效 = %v ok=%v，想要 24h", d, ok)
	}
	if p.LevelDescription() != "500 hPa" {
		t.Errorf("层描述 = %q", p.LevelDescription())
	}
	g, err := m.Grid()
	if err != nil {
		t.Fatalf("Grid: %v", err)
	}
	if g.Ni() != 2 || g.Nj() != 2 || g.NumPoints() != 4 {
		t.Errorf("网格 = %dx%d/%d", g.Ni(), g.Nj(), g.NumPoints())
	}
	vals, err := m.Values64()
	if err != nil {
		t.Fatalf("Values64: %v", err)
	}
	want := []float64{100, 101, 102, 103}
	if len(vals) != len(want) {
		t.Fatalf("值个数 = %d, 想要 %d", len(vals), len(want))
	}
	for i := range want {
		if math.Abs(vals[i]-want[i]) > 1e-9 {
			t.Errorf("值[%d] = %v, 想要 %v", i, vals[i], want[i])
		}
	}
}

func TestParseConstantField(t *testing.T) {
	// bitsPerValue 0 = constant field: every point equals R, and no bits of data.
	msg := buildMessage(t, pdsOpts{param: 11, levelType: 100, level: 500, flags: pdsFlagGDS},
		gdsLatLon(3, 2, 10, 20, 5, 25, 5, 5, 0), nil,
		bdsSimple(0, 0, ibmOf(273.15), nil))
	m, err := Parse(0, 0, msg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	vals, err := m.Values64()
	if err != nil {
		t.Fatalf("Values64: %v", err)
	}
	if len(vals) != 6 {
		t.Fatalf("常量场长度 = %d, 想要 6", len(vals))
	}
	for i, v := range vals {
		if math.Abs(v-273.15) > 1e-3 {
			t.Errorf("值[%d] = %v, 想要 273.15", i, v)
		}
	}
}

func TestParseDecimalScaleAndBinaryScale(t *testing.T) {
	// E=-1, D=1, R=0, X=0..3: value = (0 + X·2^-1)·10^-1 = 0, 0.05, 0.1, 0.15
	msg := buildMessage(t, pdsOpts{param: 11, levelType: 100, level: 500, decScale: 1, flags: pdsFlagGDS},
		gdsLatLon(2, 2, 10, 20, 5, 25, 5, 5, 0), nil,
		bdsSimple(8, -1, ibmOf(0), []uint32{0, 1, 2, 3}))
	m, err := Parse(0, 0, msg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	vals, err := m.Values64()
	if err != nil {
		t.Fatalf("Values64: %v", err)
	}
	want := []float64{0, 0.05, 0.1, 0.15}
	for i := range want {
		if math.Abs(vals[i]-want[i]) > 1e-12 {
			t.Errorf("值[%d] = %v, 想要 %v", i, vals[i], want[i])
		}
	}
}

func TestBitmapExpansion(t *testing.T) {
	// 2×2 grid with the bitmap keeping only points 0 and 2: section 4 carries just
	// 2 values and the expansion fills the missing points with NaN.
	bms := bmsAllPresent(4)
	bms[6] = 0xA0 // 1010 0000 => points 0 and 2 present (MSB first)
	msg := buildMessage(t, pdsOpts{param: 11, levelType: 100, level: 500, flags: pdsFlagGDS | pdsFlagBMS},
		gdsLatLon(2, 2, 10, 20, 5, 25, 5, 5, 0), bms,
		bdsSimple(8, 0, ibmOf(0), []uint32{7, 9}))
	m, err := Parse(0, 0, msg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Bitmap() == nil || m.Bitmap().Count() != 2 {
		t.Fatalf("位图 = %+v", m.Bitmap())
	}
	vals, err := m.Values64()
	if err != nil {
		t.Fatalf("Values64: %v", err)
	}
	if len(vals) != 4 {
		t.Fatalf("展开后长度 = %d, 想要 4", len(vals))
	}
	if vals[0] != 7 || vals[2] != 9 {
		t.Errorf("有值的点 = %v / %v, 想要 7 / 9", vals[0], vals[2])
	}
	if !math.IsNaN(vals[1]) || !math.IsNaN(vals[3]) {
		t.Errorf("缺测点应当是 NaN：%v %v", vals[1], vals[3])
	}
}

func TestFailLoud(t *testing.T) {
	gds := gdsLatLon(2, 2, 10, 20, 5, 25, 5, 5, 0)

	t.Run("二阶打包标志", func(t *testing.T) {
		bds := bdsSimple(8, 0, ibmOf(0), []uint32{0, 1, 2, 3})
		bds[3] |= 0x40 // high nibble bit 2 = complex packing
		msg := buildMessage(t, pdsOpts{param: 11, levelType: 100, level: 500, flags: pdsFlagGDS}, gds, nil, bds)
		if _, err := Parse(0, 0, msg); err == nil {
			t.Fatal("二阶打包应当 fail-loud")
		}
	})

	t.Run("位宽越界", func(t *testing.T) {
		bds := bdsSimple(8, 0, ibmOf(0), []uint32{0, 1, 2, 3})
		bds[10] = 33
		msg := buildMessage(t, pdsOpts{param: 11, levelType: 100, level: 500, flags: pdsFlagGDS}, gds, nil, bds)
		if _, err := Parse(0, 0, msg); err == nil {
			t.Fatal("位宽 33 应当 fail-loud")
		}
	})

	t.Run("值个数与网格不符", func(t *testing.T) {
		msg := buildMessage(t, pdsOpts{param: 11, levelType: 100, level: 500, flags: pdsFlagGDS}, gds, nil,
			bdsSimple(8, 0, ibmOf(0), []uint32{0, 1, 2})) // 3 coded values, but the grid wants 4
		m, err := Parse(0, 0, msg)
		if err != nil {
			t.Fatalf("Parse 应当成功（结构本身合法）: %v", err)
		}
		if _, err := m.Values64(); err == nil {
			t.Fatal("值个数不符应当 fail-loud")
		}
	})

	t.Run("参考日期不存在", func(t *testing.T) {
		msg := buildMessage(t, pdsOpts{param: 11, levelType: 100, level: 500, month: 2, day: 30,
			yearOfCent: 25, century: 21, flags: pdsFlagGDS}, gds, nil,
			bdsSimple(8, 0, ibmOf(0), []uint32{0, 1, 2, 3}))
		if _, err := Parse(0, 0, msg); err == nil {
			t.Fatal("2 月 30 日应当 fail-loud")
		}
	})

	t.Run("扫描模式保留位", func(t *testing.T) {
		bad := gdsLatLon(2, 2, 10, 20, 5, 25, 5, 5, 0x10) // bit 4 is undefined
		msg := buildMessage(t, pdsOpts{param: 11, levelType: 100, level: 500, flags: pdsFlagGDS}, bad, nil,
			bdsSimple(8, 0, ibmOf(0), []uint32{0, 1, 2, 3}))
		if _, err := Parse(0, 0, msg); err == nil {
			t.Fatal("扫描模式保留位非零应当 fail-loud")
		}
	})

	t.Run("缺 GDS 时网格不可用", func(t *testing.T) {
		msg := buildMessage(t, pdsOpts{param: 11, levelType: 100, level: 500, flags: 0}, nil, nil,
			bdsSimple(8, 0, ibmOf(0), []uint32{0, 1, 2, 3}))
		m, err := Parse(0, 0, msg)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if _, err := m.Grid(); err == nil {
			t.Fatal("没有 GDS 时 Grid() 应当 fail-loud")
		}
	})
}

func TestRegisterTable2(t *testing.T) {
	// Built-in table: ecCodes 2.50's ECMWF table 128 names parameter 167
	// "2 metre temperature" (verified against the oracle, see table2.go).
	msg := buildMessage(t, pdsOpts{param: 167, tableVersion: 128, levelType: 100, level: 500, flags: pdsFlagGDS},
		gdsLatLon(2, 2, 10, 20, 5, 25, 5, 5, 0), nil,
		bdsSimple(8, 0, ibmOf(0), []uint32{0, 1, 2, 3}))
	m, err := Parse(0, 0, msg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := m.Product().ParameterName; got != "2 metre temperature" {
		t.Errorf("内置 ECMWF 表 128 的 167 = %q，想要 \"2 metre temperature\"（ecCodes 2.50 parameterName）", got)
	}

	// A caller registration replaces the built-in table for that pair.
	if err := RegisterTable2(98, 128, map[int]string{167: "overridden"}); err != nil {
		t.Fatalf("RegisterTable2: %v", err)
	}
	m2, _ := Parse(0, 0, msg)
	if got := m2.Product().ParameterName; got != "overridden" {
		t.Errorf("注册后参数名 = %q，想要 overridden", got)
	}
	// Restore the built-in rows (RegisterTable2 has no unregister) so the rest of
	// the package's tests keep seeing the vendored table.
	restore := make(map[int]string, len(genTable2[[2]uint8{98, 128}]))
	for code, name := range genTable2[[2]uint8{98, 128}] {
		restore[int(code)] = name
	}
	if err := RegisterTable2(98, 128, restore); err != nil {
		t.Fatalf("还原 RegisterTable2: %v", err)
	}

	// A different table version keeps its own code space; it never borrows the
	// other version's names — and now it has its own real ones.
	other := buildMessage(t, pdsOpts{param: 167, tableVersion: 171, levelType: 100, level: 500, flags: pdsFlagGDS},
		gdsLatLon(2, 2, 10, 20, 5, 25, 5, 5, 0), nil,
		bdsSimple(8, 0, ibmOf(0), []uint32{0, 1, 2, 3}))
	m3, _ := Parse(0, 0, other)
	if got := m3.Product().ParameterName; got != "2 metre temperature anomaly" {
		t.Errorf("表版本 171 的 167 = %q，想要 \"2 metre temperature anomaly\"（表 171 自己的名字，不借用表 128）", got)
	}

	// No such table and no registration: ecCodes' codetable_title degrades to the
	// decimal code, and so do we.
	unknown := buildMessage(t, pdsOpts{param: 167, tableVersion: 99, levelType: 100, level: 500, flags: pdsFlagGDS},
		gdsLatLon(2, 2, 10, 20, 5, 25, 5, 5, 0), nil,
		bdsSimple(8, 0, ibmOf(0), []uint32{0, 1, 2, 3}))
	m4, _ := Parse(0, 0, unknown)
	if got := m4.Product().ParameterName; got != "167" {
		t.Errorf("没有这张表时 = %q，想要 \"167\"（ecCodes 退化到十进制码）", got)
	}

	if err := RegisterTable2(300, 1, map[int]string{1: "x"}); err == nil {
		t.Error("越界的中心号应当报错")
	}
	if err := RegisterTable2(1, 1, nil); err == nil {
		t.Error("空表应当报错")
	}
}

func TestGridTypesAndLevelNames(t *testing.T) {
	if got := GridTypeName(0); got != "Latitude/Longitude Grid" {
		t.Errorf("表 6 码 0 = %q", got)
	}
	if got := GridTypeName(5); got == "" {
		t.Error("表 6 码 5（极射赤面）应当有名字")
	}
	if got := LevelTypeName(100); got == "" {
		t.Error("表 3 码 100（等压面）应当有名字")
	}
	if got := CentreName(98); got == "" {
		t.Error("C-1 码 98（ECMWF）应当有名字")
	}
	if got := TimeUnitName(1); got == "" {
		t.Error("表 4 码 1（小时）应当有名字")
	}
	if got := TimeRangeName(0); got == "" {
		t.Error("表 5 码 0 应当有名字")
	}
	if got := ScanModeName(0); got == "" {
		t.Error("扫描模式 0 应当有描述")
	}
	// Uncatalogued codes return "" rather than an invented name.
	if got := GridTypeName(200); got != "" {
		t.Errorf("未收录的网格码应当返回空串，得到 %q", got)
	}
}

// ---- Real-corpus cross checks (external anchors) --------------------------

// TestRealECMWFFile cross checks against a real ECMWF GRIB1 file. The anchor is the
// "18 messages / 736,434 values" tuple read with eccodes.
//
// The file is not in the repo, so the test skips by default; set GRIB1_XCHECK_FILE
// or place the file at .m-realworld/D6F122900000119____1 to enable it.
func TestRealECMWFFile(t *testing.T) {
	path := os.Getenv("GRIB1_XCHECK_FILE")
	if path == "" {
		path = filepath.Join("..", ".m-realworld", "D6F122900000119____1")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("真实 GRIB1 语料不在位（%s）：设 GRIB1_XCHECK_FILE 或放到 .m-realworld/ 下", path)
	}

	const wantMessages, wantValues = 18, 736434
	var off, n, total int
	for off < len(data) {
		if len(data)-off < 8 || string(data[off:off+4]) != "GRIB" {
			t.Fatalf("offset %d: 不是 GRIB 头", off)
		}
		size := int(data[off+4])<<16 | int(data[off+5])<<8 | int(data[off+6])
		if off+size > len(data) {
			t.Fatalf("消息 %d 越过文件末尾", n)
		}
		m, err := Parse(n, int64(off), data[off:off+size])
		if err != nil {
			t.Fatalf("消息 %d: %v", n, err)
		}
		if m.Edition() != 1 {
			t.Fatalf("消息 %d: edition = %d", n, m.Edition())
		}
		g, err := m.Grid()
		if err != nil {
			t.Fatalf("消息 %d 网格: %v", n, err)
		}
		if g.Ni() != 251 || g.Nj() != 163 {
			t.Fatalf("消息 %d: 网格 = %dx%d，想要 251x163", n, g.Ni(), g.Nj())
		}
		// First point must be the (54.8N, 60.0E) declared in section 2.
		lat, lon, err := g.Point(0)
		if err != nil {
			t.Fatalf("消息 %d Point(0): %v", n, err)
		}
		if math.Abs(lat-54.8) > 1e-9 || math.Abs(lon-60) > 1e-9 {
			t.Fatalf("消息 %d 首点 = (%.4f, %.4f)，想要 (54.8, 60)", n, lat, lon)
		}
		vals, err := m.Values64()
		if err != nil {
			t.Fatalf("消息 %d 取值: %v", n, err)
		}
		if len(vals) != g.NumPoints() {
			t.Fatalf("消息 %d: 值数 %d != 网格点数 %d", n, len(vals), g.NumPoints())
		}
		total += len(vals)
		off += size
		n++
	}
	if n != wantMessages || total != wantValues {
		t.Fatalf("消息数/总点数 = %d/%d，eccodes 锚是 %d/%d", n, total, wantMessages, wantValues)
	}
}
