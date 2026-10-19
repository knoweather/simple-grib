//
// Thorough completeness tests for the GRIB1 reader: harder cases than the basic
// paths in grib1_test.go. Coverage: the decode core (all bit widths), robustness
// (arbitrary truncation / single-byte flips must not panic), coordinate semantics
// (scan modes / negative angles), PDS corner cases (layer pairs / climatological
// time) and root package integration (multi-message iteration / float32 view).

package grib1

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestBitWidthSweep builds one message per bit width 1..32 with a known X array and
// requires bit-exact decoded values — the most direct completeness proof of the
// decode core (bit-stream reading plus the value formula).
func TestBitWidthSweep(t *testing.T) {
	for bits := 1; bits <= 32; bits++ {
		bits := bits
		t.Run(fmt_Sprint(bits), func(t *testing.T) {
			// Five representative codes per width: 0, 1, mid, max-1, max.
			max := uint32(1)<<uint(bits) - 1
			xs := []uint32{0, 1, max / 2, max - 1, max}
			msg := buildMessage(t, pdsOpts{param: 1, levelType: 100, level: 500, flags: pdsFlagGDS},
				gdsLatLon(5, 1, 10, 0, 10, 40, 10, 1, 0), nil,
				bdsSimple(bits, 0, ibmOf(0), xs))
			m, err := Parse(0, 0, msg)
			if err != nil {
				t.Fatalf("位宽 %d: Parse: %v", bits, err)
			}
			vals, err := m.Values64()
			if err != nil {
				t.Fatalf("位宽 %d: Values64: %v", bits, err)
			}
			if len(vals) != len(xs) {
				t.Fatalf("位宽 %d: 值数 %d, 想要 %d", bits, len(vals), len(xs))
			}
			for i, x := range xs {
				// E=0, D=0, R=0 => value == X (exact in float64 for <= 2^32)
				if vals[i] != float64(x) {
					t.Errorf("位宽 %d: 值[%d] = %v, 想要 %d", bits, i, vals[i], x)
				}
			}
		})
	}
}

// TestNegativeReferenceAndScale combines a negative reference value (IBM sign bit)
// with a negative binary scale factor E and a decimal scale factor D — a combination
// common in meteorological fields (temperature-dewpoint spread, anomaly fields).
func TestNegativeReferenceAndScale(t *testing.T) {
	// R = -50 (IBM sign bit), E = -1, D = 0, X = 0..3
	// value = -50 + X·0.5 = -50, -49.5, -49, -48.5
	msg := buildMessage(t, pdsOpts{param: 1, levelType: 100, level: 500, flags: pdsFlagGDS},
		gdsLatLon(4, 1, 10, 0, 10, 30, 10, 1, 0), nil,
		bdsSimple(8, -1, ibmOf(-50), []uint32{0, 1, 2, 3}))
	m, err := Parse(0, 0, msg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	vals, err := m.Values64()
	if err != nil {
		t.Fatalf("Values64: %v", err)
	}
	want := []float64{-50, -49.5, -49, -48.5}
	for i := range want {
		if math.Abs(vals[i]-want[i]) > 1e-9 {
			t.Errorf("值[%d] = %v, 想要 %v", i, vals[i], want[i])
		}
	}
}

// TestTruncationsNeverPanic truncates a structurally valid message at every possible
// length; Parse must return an error, never panic. The floor of fail-loud is that
// bad input never panics.
func TestTruncationsNeverPanic(t *testing.T) {
	good := buildMessage(t, pdsOpts{param: 11, levelType: 100, level: 500, flags: pdsFlagGDS | pdsFlagBMS},
		gdsLatLon(2, 2, -30, -60, -40, -50, 10, 10, 0),
		bmsAllPresent(4),
		bdsSimple(8, 0, ibmOf(100), []uint32{0, 1, 2, 3}))

	for n := 0; n < len(good); n++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("截断到 %d 字节时 panic: %v", n, r)
				}
			}()
			_, _ = Parse(0, 0, good[:n]) // an error is fine, a panic is not
		}()
	}
	// The complete message itself must parse.
	if _, err := Parse(0, 0, good); err != nil {
		t.Fatalf("完整消息解析失败: %v", err)
	}
}

// TestByteFlipsNeverPanic flips single bytes across the message (0x00↔0xFF and
// nibble flips); the only hard requirement is no panic — an error or a clean parse
// are both acceptable.
func TestByteFlipsNeverPanic(t *testing.T) {
	good := buildMessage(t, pdsOpts{param: 11, levelType: 100, level: 500, flags: pdsFlagGDS},
		gdsLatLon(2, 2, 10, 20, 5, 25, 5, 5, 0), nil,
		bdsSimple(8, 0, ibmOf(100), []uint32{0, 1, 2, 3}))

	for i := 0; i < len(good); i++ {
		for _, mask := range []byte{0xFF, 0x0F, 0x80} {
			b := append([]byte{}, good...)
			b[i] ^= mask
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("翻转 offset %d (mask %#02x) 时 panic: %v", i, mask, r)
					}
				}()
				if m, err := Parse(0, 0, b); err == nil {
					// Still parses after the flip: value decoding must not panic either.
					_, _ = m.Values64()
					_, _ = m.Grid()
				}
			}()
		}
	}
}

// TestScanModeCoordinateOrder checks all four scan-mode bit combinations against
// the point ordering: bit 1 = -i direction (west→east becomes east→west), bit 2 = -j
// direction (north→south becomes south→north), bit 3 = consecutive points along j.
// Point(i) must return geometry consistent with the declared scan direction.
func TestScanModeCoordinateOrder(t *testing.T) {
	// 2×2 grid: La1=10N (northward), La2=0N, Lo1=0E, Lo2=10E, Di=Dj=10 deg.
	// Default scan (WE:SN, consecutive along i): points = (10N,0E) (10N,10E) (0N,0E) (0N,10E)
	cases := []struct {
		name    string
		scan    uint8
		wantLat [4]float64 // latitudes of the 4 points
		wantLon [4]float64
	}{
		{"WE:SN i连续", 0x00, [4]float64{10, 10, 0, 0}, [4]float64{0, 10, 0, 10}},
		// -i direction (east→west): Lo2 lies west of Lo1; first point stays (La1, Lo1)
		{"i负向", 0x80, [4]float64{10, 10, 0, 0}, [4]float64{0, -10, 0, -10}},
		// -j direction (south→north): La2 lies north of La1
		{"j负向(NS)", 0x40, [4]float64{0, 0, 10, 10}, [4]float64{0, 10, 0, 10}},
		{"j连续", 0x20, [4]float64{10, 0, 10, 0}, [4]float64{0, 0, 10, 10}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Keep the grid geometry self-consistent with the scan: Lo2 west of
			// Lo1 for -i, La2 north of La1 for -j.
			la1, lo1, la2, lo2 := 10.0, 0.0, 0.0, 10.0
			if tc.scan&0x80 != 0 {
				lo2 = -10
			}
			if tc.scan&0x40 != 0 {
				la1, la2 = 0, 10
			}
			msg := buildMessage(t, pdsOpts{param: 1, levelType: 100, level: 500, flags: pdsFlagGDS},
				gdsLatLon(2, 2, la1, lo1, la2, lo2, 10, 10, tc.scan), nil,
				bdsSimple(8, 0, ibmOf(0), []uint32{0, 1, 2, 3}))
			m, err := Parse(0, 0, msg)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			g, err := m.Grid()
			if err != nil {
				t.Fatalf("Grid: %v", err)
			}
			for i := 0; i < 4; i++ {
				lat, lon, err := g.Point(i)
				if err != nil {
					t.Fatalf("Point(%d): %v", i, err)
				}
				if math.Abs(lat-tc.wantLat[i]) > 1e-9 || math.Abs(lon-tc.wantLon[i]) > 1e-9 {
					t.Errorf("点 %d = (%.4f, %.4f), 想要 (%.4f, %.4f)",
						i, lat, lon, tc.wantLat[i], tc.wantLon[i])
				}
			}
		})
	}
}

// TestSouthernHemisphereEndToEnd: southern-hemisphere negative corners
// (sign-magnitude angles) plus a real value stream — the whole chain (negative GDS
// latitudes → coordinates → values) must work over the negative domain.
func TestSouthernHemisphereEndToEnd(t *testing.T) {
	// 2×2: La1=-10 (northward), La2=-30 (southward), Lo1=-60, Lo2=-40
	msg := buildMessage(t, pdsOpts{param: 1, levelType: 100, level: 500, flags: pdsFlagGDS},
		gdsLatLon(2, 2, -10, -60, -30, -40, 20, 20, 0), nil,
		bdsSimple(8, 0, ibmOf(250), []uint32{0, 5, 10, 15}))
	m, err := Parse(0, 0, msg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	g, _ := m.Grid()
	lat, lon, err := g.Point(3) // last point must be (La2, Lo2), independent of scan direction
	if err != nil {
		t.Fatalf("Point(3): %v", err)
	}
	if math.Abs(lat-(-30)) > 1e-9 || math.Abs(lon-(-40)) > 1e-9 {
		t.Errorf("末点 = (%.4f, %.4f), 想要 (-30, -40) = (La2, Lo2)", lat, lon)
	}
	// First point must be (La1, Lo1).
	lat, lon, err = g.Point(0)
	if err != nil {
		t.Fatalf("Point(0): %v", err)
	}
	if math.Abs(lat-(-10)) > 1e-9 || math.Abs(lon-(-60)) > 1e-9 {
		t.Errorf("首点 = (%.4f, %.4f), 想要 (-10, -60) = (La1, Lo1)", lat, lon)
	}
	vals, _ := m.Values64()
	if vals[3] != 265 {
		t.Errorf("末点值 = %v, 想要 265", vals[3])
	}
}

// TestLayerRangeAndClimatological: the two PDS corner cases — layer-pair level
// types (two 1-byte level values) and the climatological reference time
// (yearOfCentury = 255, no real year).
func TestLayerRangeAndClimatological(t *testing.T) {
	// Hand-built PDS: level type 101 (layer between two isobaric surfaces, octets
	// 11/12 = 1-byte top/bottom bounds). Note 104 is *not* isobaric — that is
	// "layer between two altitudes above MSL" (in hm); swapping table 3 codes like
	// that is the classic mistake this test guards against.
	pds := make([]byte, 28)
	putU24(pds[0:], 28)
	pds[3], pds[4] = 3, 98
	pds[6] = 255
	pds[7] = pdsFlagGDS
	pds[8] = 3 // parameter 3 (geopotential)
	pds[9] = 101
	pds[10], pds[11] = 100, 92 // layer pair: top 100, bottom 92
	pds[12] = 0xFF             // climatological sentinel
	pds[13], pds[14], pds[15] = 7, 15, 12
	pds[17] = 1
	pds[24] = 21
	putSmag16(pds[26:], 0)

	gds := gdsLatLon(2, 2, 10, 0, 0, 10, 10, 10, 0)
	bds := bdsSimple(8, 0, ibmOf(0), []uint32{0, 1, 2, 3})
	total := 8 + 28 + len(gds) + len(bds) + 4
	msg := make([]byte, 8, total)
	copy(msg[0:4], "GRIB")
	msg[4], msg[5], msg[6] = byte(total>>16), byte(total>>8), byte(total)
	msg[7] = Edition
	msg = append(msg, pds...)
	msg = append(msg, gds...)
	msg = append(msg, bds...)
	msg = append(msg, '7', '7', '7', '7')

	m, err := Parse(0, 0, msg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	p := m.Product()
	if !p.LayerRange || p.LevelTop != 100 || p.LevelBottom != 92 {
		t.Errorf("层组 = %+v", p)
	}
	if !m.Identification().Climatological {
		t.Error("yearOfCentury=255 应当标为气候态")
	}
	if !m.Identification().ReferenceTime.IsZero() {
		t.Error("气候态不应当有参考时间")
	}
	// Layer-pair description shows just the range (no unit suffix; the unit varies
	// with the level type).
	if got := p.LevelDescription(); got != "100-92" {
		t.Errorf("层组描述 = %q, 想要 100-92", got)
	}
}

// TestQuasiRegularEndToEnd: quasi-regular grid (Ni missing + per-row point counts)
// plus bitmap plus values. Rows differ in width, so the total point count is the sum
// of the list and the values expand in row order.
func TestQuasiRegularEndToEnd(t *testing.T) {
	// 3 rows of 4, 2 and 3 points (9 total). GDS type 0 with Ni = 0xFFFF (missing).
	gds := make([]byte, 32+6) // 32-byte base + three 2-byte point counts
	copy(gds[:32], gdsLatLon(0, 3, 60, 0, 40, 60, 0, 10, 0))
	gds[0], gds[1], gds[2] = 0, 0, byte(len(gds))
	putU16(gds[6:], 0xFFFF) // Ni missing
	putU16(gds[32:], 4)
	putU16(gds[34:], 2)
	putU16(gds[36:], 3)

	xs := make([]uint32, 9)
	for i := range xs {
		xs[i] = uint32(i)
	}
	msg := buildMessage(t, pdsOpts{param: 1, levelType: 100, level: 500, flags: pdsFlagGDS},
		gds, nil, bdsSimple(8, 0, ibmOf(0), xs))
	m, err := Parse(0, 0, msg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	g, err := m.Grid()
	if err != nil {
		t.Fatalf("Grid: %v", err)
	}
	if !g.IsQuasiRegular() {
		t.Fatal("应当判定为准规则网格")
	}
	if g.NumPoints() != 9 {
		t.Errorf("总点数 = %d, 想要 9", g.NumPoints())
	}
	vals, err := m.Values64()
	if err != nil {
		t.Fatalf("Values64: %v", err)
	}
	if len(vals) != 9 {
		t.Fatalf("值数 = %d, 想要 9", len(vals))
	}
	for i := range vals {
		if vals[i] != float64(i) {
			t.Errorf("值[%d] = %v, 想要 %d", i, vals[i], i)
		}
	}
}

// fmt_Sprint avoids importing fmt (the test only needs the bit width as a subtest
// name).
func fmt_Sprint(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

// TestRealFileValueAnchors anchors on numeric statistics of a real ECMWF file
// (stronger than only counting points): the expected values come from a local
// Python reference decoder (in agreement with eccodes); a 0.01 float tolerance applies.
func TestRealFileValueAnchors(t *testing.T) {
	path := os.Getenv("GRIB1_XCHECK_FILE")
	if path == "" {
		path = filepath.Join("..", ".m-realworld", "D6F122900000119____1")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("真实 GRIB1 语料不在位（%s）", path)
	}

	// Slice out all messages in order (each has a different length: the bit width
	// changes the BDS size).
	var msgs []*Message
	for off := 0; off < len(data); {
		if string(data[off:off+4]) != "GRIB" {
			t.Fatalf("offset %d: 不是 GRIB 头", off)
		}
		size := int(data[off+4])<<16 | int(data[off+5])<<8 | int(data[off+6])
		m, err := Parse(len(msgs), int64(off), data[off:off+size])
		if err != nil {
			t.Fatalf("消息 %d: %v", len(msgs), err)
		}
		msgs = append(msgs, m)
		off += size
	}
	if len(msgs) != 18 {
		t.Fatalf("消息数 = %d, 想要 18", len(msgs))
	}

	stats := func(m *Message) (min, max, mean float64, n int) {
		vals, err := m.Values64()
		if err != nil {
			t.Fatalf("Values64: %v", err)
		}
		min, max = math.Inf(1), math.Inf(-1)
		var sum float64
		for _, v := range vals {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
			sum += v
		}
		return min, max, sum / float64(len(vals)), len(vals)
	}

	// Message 0 (parameter 228, a surface quantity): statistics from the Python
	// reference decoder.
	mn, mx, mean, n := stats(msgs[0])
	if n != 40913 {
		t.Fatalf("消息 0 值数 = %d, 想要 40913", n)
	}
	if math.Abs(mn-(-7.669888e-8)) > 1e-10 || math.Abs(mx-1.221021e-7) > 1e-10 {
		t.Errorf("消息 0 min/max = %.8e/%.8e，想要 -7.669888e-8/1.221021e-7", mn, mx)
	}
	if math.Abs(mean-1.677948e-9) > 1e-11 {
		t.Errorf("消息 0 mean = %.8e, 想要 1.677948e-9", mean)
	}

	// Message 3 (parameter 167 = 2 metre temperature): physical range anchor 240-303 K.
	mn, mx, _, n = stats(msgs[3])
	if n != 40913 {
		t.Fatalf("消息 3 值数 = %d, 想要 40913", n)
	}
	if mn < 240 || mx > 303 {
		t.Errorf("2m 温度范围 = %.2f..%.2f K，超出 240~303 的物理锚", mn, mx)
	}
	if math.Abs(mn-240.62) > 0.1 || math.Abs(mx-302.48) > 0.1 {
		t.Errorf("2m 温度 min/max = %.2f/%.2f，参照值 240.62/302.48", mn, mx)
	}
}
