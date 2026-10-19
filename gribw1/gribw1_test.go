package gribw1

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/knoweather/simple-grib/grib1"
)

// baseParams 是一份合法的参数基线（2×2 网格、500hPa 位势、ECMWF）。
func baseParams() Params {
	return Params{
		Centre:             98,
		SubCentre:          0,
		TableVersion:       3,
		GeneratingProcess:  158,
		Parameter:          3,
		ReferenceTime:      time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC),
		LevelType:          100,
		LevelTop:           500,
		TimeUnit:           1,
		P1:                 112,
		TimeRangeIndicator: 10,
		Ni:                 2,
		Nj:                 2,
		La1:                60,
		Lo1:                0,
		La2:                40,
		Lo2:                20,
		Di:                 20,
		Dj:                 20,
		ScanMode:           0,
		Values:             []float64{1, 2, 3, 4},
	}
}

// wantClose 断言 v≈want（相对容差 1e-12：float64 的 10^-D 不精确，
// 逐位 == 会把 -12.5 判成 -12.500000000000002 —— 这是二进制浮点的固有现象，
// eccodes 侧同样存在）。
func wantClose(t *testing.T, idx int, got, want float64) {
	t.Helper()
	tol := 1e-12 * math.Max(1, math.Abs(want))
	if math.Abs(got-want) > tol {
		t.Errorf("值[%d] = %v, 想要 %v", idx, got, want)
	}
}

// roundTrip 编码后用 grib1.Parse 读回，返回消息与值。
func roundTrip(t *testing.T, p Params) (*grib1.Message, []float64) {
	t.Helper()
	data, err := Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	m, err := grib1.Parse(0, 0, data)
	if err != nil {
		t.Fatalf("grib1.Parse 读回失败: %v", err)
	}
	vals, err := m.Values64()
	if err != nil {
		t.Fatalf("Values64: %v", err)
	}
	return m, vals
}

// TestEncodeRoundTripBasics 基本往返：PDS/GDS 字段、值、居中。
func TestEncodeRoundTripBasics(t *testing.T) {
	m, vals := roundTrip(t, baseParams())

	if m.Edition() != 1 {
		t.Errorf("edition = %d", m.Edition())
	}
	id := m.Identification()
	if id.Centre != 98 || id.TableVersion != 3 || id.Century != 21 {
		t.Errorf("identification = %+v", id)
	}
	if got := id.ReferenceTime.Format("2006-01-02T15:04"); got != "2025-12-29T00:00" {
		t.Errorf("参考时间 = %s", got)
	}
	p := m.Product()
	if p.Parameter != 3 || p.LevelType != 100 || p.LevelTop != 500 {
		t.Errorf("product = %+v", p)
	}
	if p.TimeRangeIndicator != 10 || p.P1 != 112 {
		t.Errorf("时效字段 = %+v", p)
	}
	g, err := m.Grid()
	if err != nil {
		t.Fatalf("Grid: %v", err)
	}
	if g.Ni() != 2 || g.Nj() != 2 {
		t.Errorf("网格 = %dx%d", g.Ni(), g.Nj())
	}
	if math.Abs(g.La1-60) > 1e-9 || math.Abs(g.Lo2-20) > 1e-9 {
		t.Errorf("角点 = La1 %v Lo2 %v", g.La1, g.Lo2)
	}
	want := []float64{1, 2, 3, 4}
	for i := range want {
		wantClose(t, i, vals[i], want[i])
	}
}

// TestEncodeValueFidelity 自动选参下的值保真：物理量级的场
// （温度、气压、位势）往返误差必须为 0（十进制网格精确表示）。
func TestEncodeValueFidelity(t *testing.T) {
	cases := map[string][]float64{
		"2m温度(K)":   {240.62, 263.11, 288.05, 302.48},
		"海平面气压(Pa)": {99409.6, 101325.0, 104052.0, 100000.0},
		"位势(gpm)":   {5200.5, 5340.25, 5480.0, 5120.75},
		"含负值的偏差场":   {-12.5, 0.5, 12.5, -0.5},
		"小量大动态范围":   {1e-3, 5, 250, 120},
	}
	for name, values := range cases {
		t.Run(name, func(t *testing.T) {
			p := baseParams()
			p.Values = values
			_, vals := roundTrip(t, p)
			for i := range values {
				wantClose(t, i, vals[i], values[i])
			}
		})
	}
}

// TestEncodeConstantField 常量场：位宽 0，段里一个比特都不占。
func TestEncodeConstantField(t *testing.T) {
	p := baseParams()
	p.Values = []float64{273.15, 273.15, 273.15, 273.15}
	data, err := Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	m, err := grib1.Parse(0, 0, data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.DataRep().BitsPerValue != 0 {
		t.Errorf("位宽 = %d, 想要 0", m.DataRep().BitsPerValue)
	}
	if len(m.DataRep().Payload) != 0 {
		t.Errorf("常量场载荷 = %d 字节, 想要 0", len(m.DataRep().Payload))
	}
	vals, _ := m.Values64()
	for i, v := range vals {
		if math.Abs(v-273.15) > 1e-6 {
			t.Errorf("值[%d] = %v", i, v)
		}
	}
}

// TestEncodeExplicitBits 显式位宽/E：X 数组逐位可控。
func TestEncodeExplicitBits(t *testing.T) {
	p := baseParams()
	p.Values = []float64{0, 1, 2, 3}
	p.Bits = 3 // X = v·2^(-E) = 2v ∈ {0,2,4,6}，要 3 位
	p.BinaryScale = -1
	_, vals := roundTrip(t, p)
	// Y = (R + X·2^-1)·10^0，R=0 => 0, 1, 2, 3
	want := []float64{0, 1, 2, 3}
	for i := range want {
		wantClose(t, i, vals[i], want[i])
	}
	// 显式参数模式**尊重调用方**：等值场给了 Bits=4 就走 4 位（不自动退化
	// 常量场）—— 显式模式的优先级是确定性，不是越小越好。值仍须正确。
	p2 := baseParams()
	p2.Values = []float64{1.5, 1.5, 1.5, 1.5}
	p2.Bits = 4
	m2, vals2 := roundTrip(t, p2)
	if m2.DataRep().BitsPerValue != 4 {
		t.Errorf("显式位宽 4 的等值场应当保持 4 位，实际 %d", m2.DataRep().BitsPerValue)
	}
	for i, v := range vals2 {
		wantClose(t, i, v, 1.5)
	}
}

// TestEncodeBitmap 位图：缺测点在段 4 里不占位，读回展开成 NaN。
func TestEncodeBitmap(t *testing.T) {
	p := baseParams()
	p.Values = []float64{10, 20, 30, 40}
	// 第 1、2 个点有值，第 0、3 个缺测
	p.Bitmap = []bool{false, true, true, false}
	_, vals := roundTrip(t, p)
	if !math.IsNaN(vals[0]) || !math.IsNaN(vals[3]) {
		t.Errorf("缺测点应当是 NaN: %v %v", vals[0], vals[3])
	}
	wantClose(t, 1, vals[1], 20)
	wantClose(t, 2, vals[2], 30)

	// 全 true 位图：不写段 3（更小），语义等价
	p2 := baseParams()
	p2.Bitmap = []bool{true, true, true, true}
	m2, vals2 := roundTrip(t, p2)
	if m2.Bitmap() != nil {
		t.Error("全 true 位图不该写出段 3")
	}
	for i, v := range vals2 {
		wantClose(t, i, v, float64(i+1))
	}
}

// TestEncodeGaussian 高斯网格（类型 4）：N 写进 octets 26-27，Nj=2N。
func TestEncodeGaussian(t *testing.T) {
	p := baseParams()
	p.GaussianN = 2
	p.Ni, p.Nj = 8, 4
	p.Di, p.Dj = 45, 0
	p.La1, p.La2 = 59.44440828916676, -59.44440828916676
	p.Values = make([]float64, 32)
	for i := range p.Values {
		p.Values[i] = float64(i)
	}
	m, vals := roundTrip(t, p)
	g, err := m.Grid()
	if err != nil {
		t.Fatalf("Grid: %v", err)
	}
	if g.TemplateNumber() != 4 {
		t.Errorf("网格类型 = %d, 想要 4（高斯）", g.TemplateNumber())
	}
	lat, _, err := g.Point(0)
	if err != nil {
		t.Fatalf("Point(0): %v", err)
	}
	if math.Abs(lat-59.44440828916676) > 1e-6 {
		t.Errorf("首行纬度 = %v", lat)
	}
	for i := range vals {
		wantClose(t, i, vals[i], float64(i))
	}
}

// TestEncodeLevelGroup 层组类型（101）：octets 11/12 两个 1 字节。
func TestEncodeLevelGroup(t *testing.T) {
	p := baseParams()
	p.LevelType = 101
	p.LevelTop = 100
	p.LevelBottom = 92
	_, _ = roundTrip(t, p)
	m, _ := grib1.Parse(0, 0, mustEncode(t, p))
	if !m.Product().LayerRange || m.Product().LevelTop != 100 || m.Product().LevelBottom != 92 {
		t.Errorf("层组 = %+v", m.Product())
	}
}

func mustEncode(t *testing.T, p Params) []byte {
	t.Helper()
	data, err := Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return data
}

// TestEncodeFailLoud 坏输入必须报错而不是写出结构非法的字节。
func TestEncodeFailLoud(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Params)
	}{
		{"NaN 值", func(p *Params) { p.Values = []float64{1, math.NaN(), 3, 4} }},
		{"Inf 值", func(p *Params) { p.Values = []float64{1, math.Inf(1), 3, 4} }},
		{"参考时间是零值", func(p *Params) { p.ReferenceTime = time.Time{} }},
		{"Centre 超界", func(p *Params) { p.Centre = 0x100 }},
		{"扫描模式保留位", func(p *Params) { p.ScanMode = 0x10 }},
		{"纬度超界", func(p *Params) { p.La1 = 9000 }},
		{"位宽超界", func(p *Params) { p.Bits = 33 }},
		{"显式位宽装不下", func(p *Params) { p.Values = []float64{0, 0, 0, 100}; p.Bits = 2 }},
		{"层组缺下界", func(p *Params) { p.LevelType = 101; p.LevelTop = 100; p.LevelBottom = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := baseParams()
			tc.mut(&p)
			if _, err := Encode(p); err == nil {
				t.Error("坏输入却没有报错")
			}
		})
	}
	// 参数字节超界单独写（上面的空 mut 是占位）
	p := baseParams()
	p.Parameter = 0x100
	if _, err := Encode(p); err == nil {
		t.Error("Parameter > 255 应当报错")
	}
}

// TestEncodeMultiMessageFile 多消息文件 + 与 GRIB2（gribw）混装。
func TestEncodeMultiMessageFile(t *testing.T) {
	p := baseParams()
	a := mustEncode(t, p)
	p.Values = []float64{9, 8, 7, 6}
	b := mustEncode(t, p)
	multi := File(a, b)

	// 用 gribw1 自己的计数走一遍
	if n := countMessages(multi); n != 2 {
		t.Fatalf("消息数 = %d, 想要 2", n)
	}

	// 用 GRIB2 也放一条进来：混装文件 edition 分派
	// 塞一条 GRIB2（ecc-xcheck 小样本）做真混装：不同 edition 必须被分开解析
	g2, err := os.ReadFile(filepath.Join("..", "testdata", "ecc-xcheck", "poscorners.grib2"))
	if err != nil {
		t.Skipf("缺 GRIB2 样本: %v", err)
	}
	mixed := File(a, g2)
	if n := countMessages(mixed); n != 2 {
		t.Fatalf("混装消息数 = %d, 想要 2", n)
	}
}

// countMessages 数一下文件里有几条消息（edition 感知的只读切片逻辑，测试用）：
// GRIB1 总长在 octets 5-7，GRIB2 在 octets 9-16（8 字节）。
func countMessages(data []byte) int {
	n, off := 0, 0
	for off+8 <= len(data) {
		var total int64
		switch data[off+7] {
		case 1:
			total = int64(data[off+4])<<16 | int64(data[off+5])<<8 | int64(data[off+6])
		case 2:
			if off+16 > len(data) {
				return n
			}
			for _, b := range data[off+8 : off+16] {
				total = total<<8 | int64(b)
			}
		default:
			return n
		}
		if total <= 0 || off+int(total) > len(data) {
			break
		}
		off += int(total)
		n++
	}
	return n
}

// TestRoundTripRealFile 真实 ECMWF GRIB1 文件：解码 → 再编码 → 再解码，
// 值与网格必须等价（这是对写入器最硬的对拍 —— 语料自己当裁判）。
func TestRoundTripRealFile(t *testing.T) {
	path := os.Getenv("GRIB1_XCHECK_FILE")
	if path == "" {
		path = filepath.Join("..", ".m-realworld", "D6F122900000119____1")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("真实 GRIB1 语料不在位（%s）", path)
	}

	off, n := 0, 0
	for off < len(data) {
		size := int(data[off+4])<<16 | int(data[off+5])<<8 | int(data[off+6])
		orig, err := grib1.Parse(n, int64(off), data[off:off+size])
		if err != nil {
			t.Fatalf("消息 %d 解析: %v", n, err)
		}
		vals, err := orig.Values64()
		if err != nil {
			t.Fatalf("消息 %d 取值: %v", n, err)
		}
		g, err := orig.Grid()
		if err != nil {
			t.Fatalf("消息 %d 网格: %v", n, err)
		}
		id := orig.Identification()
		pr := orig.Product()

		// 用同一个编码器重写这条消息。
		p := Params{
			Centre:             id.Centre,
			SubCentre:          id.SubCentre,
			TableVersion:       id.TableVersion,
			GeneratingProcess:  id.GeneratingProcess,
			Parameter:          pr.Parameter,
			ReferenceTime:      id.ReferenceTime,
			LevelType:          pr.LevelType,
			LevelTop:           pr.LevelTop,
			LevelBottom:        pr.LevelBottom,
			TimeUnit:           pr.TimeRangeUnit,
			P1:                 pr.P1,
			P2:                 pr.P2,
			TimeRangeIndicator: pr.TimeRangeIndicator,
			Ni:                 g.Ni(),
			Nj:                 g.Nj(),
			La1:                g.La1, Lo1: g.Lo1, La2: g.La2, Lo2: g.Lo2,
			Di: g.Di, Dj: g.Dj,
			ScanMode: g.ScanMode(),
			Values:   vals,
			// 继承原始打包参数（D/E/位宽）做逐位忠实的 round-trip：原文件的选择
			// 是中心自定义的精度政策（这条消息是 D=0、E=-38），复用它才能零误差回放。
			DecimalScaleFactor: pr.DecimalScaleFactor,
			BinaryScale:        orig.DataRep().BinaryScale,
			Bits:               orig.DataRep().BitsPerValue,
		}
		reenc, err := Encode(p)
		if err != nil {
			t.Fatalf("消息 %d 重编码: %v", n, err)
		}
		back, err := grib1.Parse(n, int64(off), reenc)
		if err != nil {
			t.Fatalf("消息 %d 重编码后再解析: %v", n, err)
		}
		backVals, err := back.Values64()
		if err != nil {
			t.Fatalf("消息 %d 重编码取值: %v", n, err)
		}
		// 容差 = 编码自身的一个量化步长 ×2（解码一次 + 再编码一次各一次取整）。
		// 原文件的 D/E 就是该中心的精度政策，我们的回放不应当比它更粗。
		quantum := math.Pow(2, float64(orig.DataRep().BinaryScale)) *
			math.Pow(10, -float64(pr.DecimalScaleFactor))
		tol := 2 * quantum
		if len(backVals) != len(vals) {
			t.Fatalf("消息 %d: 值数 %d != %d", n, len(backVals), len(vals))
		}
		for i := range vals {
			if math.Abs(backVals[i]-vals[i]) > tol {
				t.Errorf("消息 %d 值[%d] = %v，原值 %v（容差 %g）", n, i, backVals[i], vals[i], tol)
			}
		}
		// 网格与元数据
		bg, _ := back.Grid()
		if bg.Ni() != g.Ni() || bg.Nj() != g.Nj() {
			t.Errorf("消息 %d: 网格 %dx%d != %dx%d", n, bg.Ni(), bg.Nj(), g.Ni(), g.Nj())
		}
		if lat, _, _ := bg.Point(0); math.Abs(lat-54.8) > 1e-9 {
			t.Errorf("消息 %d: 首点纬度 %v", n, lat)
		}
		off += size
		n++
	}
	if n != 18 {
		t.Fatalf("消息数 = %d, 想要 18", n)
	}
}
