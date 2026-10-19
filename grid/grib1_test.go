package grid

import (
	"math"
	"testing"

	"github.com/knoweather/simple-grib/internal/octetio"
)

// 造一个 GRIB1 GDS：公共头 + 按类型的字段。数值一律按符号-幅度 / 1e-3 度写入。
type gdsBuilder struct {
	b []byte
}

func newGDS(reprType int) *gdsBuilder {
	g := &gdsBuilder{b: make([]byte, 32)}
	g.b[3] = 0   // NV
	g.b[4] = 255 // PV
	g.b[5] = byte(reprType)
	return g
}

func (g *gdsBuilder) u16(off, v int) *gdsBuilder {
	g.b[off] = byte(v >> 8)
	g.b[off+1] = byte(v)
	return g
}

func (g *gdsBuilder) u24(off, v int) *gdsBuilder {
	g.b[off], g.b[off+1], g.b[off+2] = byte(v>>16), byte(v>>8), byte(v)
	return g
}

func (g *gdsBuilder) smag24(off int, deg float64) *gdsBuilder {
	mag := int(math.Round(math.Abs(deg) * 1000))
	v := mag & 0x7FFFFF
	if deg < 0 {
		v |= 0x800000
	}
	return g.u24(off, v)
}

func (g *gdsBuilder) at(off int, v byte) *gdsBuilder { g.b[off] = v; return g }

func (g *gdsBuilder) done() []byte {
	n := len(g.b)
	g.b[0], g.b[1], g.b[2] = byte(n>>16), byte(n>>8), byte(n)
	return g.b
}

// latLonGDS 是类型 0/10 的公共字段布局（octets 7-28）。
func latLonGDS(reprType, ni, nj int, la1, lo1, la2, lo2, di, dj float64, scan uint8) *gdsBuilder {
	g := newGDS(reprType)
	g.u16(6, ni).u16(8, nj)
	g.smag24(10, la1).smag24(13, lo1).at(16, 0x80)
	g.smag24(17, la2).smag24(20, lo2)
	g.u16(23, int(math.Round(di*1000))).u16(25, int(math.Round(dj*1000)))
	g.at(27, scan)
	return g
}

func TestNewGRIB1LatLon(t *testing.T) {
	gds := latLonGDS(GRIB1LatLon, 4, 3, 10, 20, 0, 30, 2.5, 5, 0x40).done()
	d, err := NewGRIB1(gds)
	if err != nil {
		t.Fatalf("NewGRIB1: %v", err)
	}
	if d.edition != 1 || d.TemplateNumber() != GRIB1LatLon {
		t.Errorf("edition/template = %d/%d", d.edition, d.TemplateNumber())
	}
	if d.Ni() != 4 || d.Nj() != 3 || d.NumPoints() != 12 {
		t.Fatalf("网格 = %dx%d/%d", d.Ni(), d.Nj(), d.NumPoints())
	}
	if !d.IsQuasiRegular() == false {
		t.Error("不该被判定为准规则")
	}
	// scanMode = 0x40：j 正向扫描（纬度递增），i 正向
	lat, lon, err := d.Point(0)
	if err != nil {
		t.Fatalf("Point(0): %v", err)
	}
	if math.Abs(lat-10) > 1e-9 || math.Abs(lon-20) > 1e-9 {
		t.Errorf("首点 = (%.4f, %.4f)，想要 (10, 20)", lat, lon)
	}
	// 末点 = 第 3 行第 4 列：(10 + 2*5, 20 + 3*2.5) = (20, 27.5)
	lat, lon, err = d.Point(11)
	if err != nil {
		t.Fatalf("Point(11): %v", err)
	}
	if math.Abs(lat-20) > 1e-9 || math.Abs(lon-27.5) > 1e-9 {
		t.Errorf("末点 = (%.4f, %.4f)，想要 (20, 27.5)", lat, lon)
	}
	// GRIB1 默认是半径 6367.47 km 的球
	if r := d.Radius(); r != 6367470 {
		t.Errorf("半径 = %v, 想要 6367470", r)
	}
	if d.ShapeOfEarth() != 0 {
		t.Errorf("地球形状 = %d, 想要 0", d.ShapeOfEarth())
	}
}

func TestNewGRIB1NegativeLatLon(t *testing.T) {
	// 南半球的负纬度：signed[3] 符号-幅度，-10 度 = 0x802710
	gds := latLonGDS(GRIB1LatLon, 2, 2, 10, 20, -10, 30, 20, 20, 0).done()
	d, err := NewGRIB1(gds)
	if err != nil {
		t.Fatalf("NewGRIB1: %v", err)
	}
	if math.Abs(d.La2-(-10)) > 1e-9 {
		t.Errorf("La2 = %v, 想要 -10（符号-幅度解码）", d.La2)
	}
	lat, _, err := d.Point(2)
	if err != nil {
		t.Fatalf("Point(2): %v", err)
	}
	if math.Abs(lat-(-10)) > 1e-9 {
		t.Errorf("第 2 行纬度 = %v, 想要 -10", lat)
	}
}

func TestNewGRIB1Gaussian(t *testing.T) {
	// 类型 4：octets 26-27 是高斯的 N（不是 Dj）。N=2 时全球有 4 条平行圈。
	//
	// 注意高斯平行圈的纬度由 N 的 Legendre 根决定（N=2 时是 ±59.4444…、±19.875…），
	// **不是** La1/La2 —— 所以这里把 La1/La2 写成真实的第一/最后一条平行圈。
	const gaussLatN2 = 59.44440828916676
	g := newGDS(GRIB1Gaussian)
	g.u16(6, 8).u16(8, 4)
	g.smag24(10, gaussLatN2).smag24(13, 0).at(16, 0x80)
	g.smag24(17, -gaussLatN2).smag24(20, 300)
	g.u16(23, 0) // Di（类型 4 里 ecCodes 允许缺失，这里给 0）
	g.u16(25, 2) // N
	g.at(27, 0)
	gds := g.done()

	d, err := NewGRIB1(gds)
	if err != nil {
		t.Fatalf("NewGRIB1: %v", err)
	}
	if d.gaussN != 2 {
		t.Fatalf("N = %d, 想要 2", d.gaussN)
	}
	if d.Ni() != 8 || d.Nj() != 4 {
		t.Fatalf("网格 = %dx%d", d.Ni(), d.Nj())
	}
	// Nj == 2N，是全球网格：首末行纬度应当正好等于 N 决定的平行圈
	lat, _, err := d.Point(0)
	if err != nil {
		t.Fatalf("Point(0): %v", err)
	}
	if math.Abs(lat-gaussLatN2) > 1e-6 {
		t.Errorf("首行纬度 = %v, 想要 %v", lat, gaussLatN2)
	}
	lat, _, err = d.Point(3 * 8)
	if err != nil {
		t.Fatalf("Point(24): %v", err)
	}
	if math.Abs(lat+gaussLatN2) > 1e-6 {
		t.Errorf("末行纬度 = %v, 想要 %v", lat, -gaussLatN2)
	}
}

func TestNewGRIB1Projected(t *testing.T) {
	t.Run("极射赤面", func(t *testing.T) {
		g := newGDS(GRIB1PolarStereo)
		g.u16(6, 3).u16(8, 2)
		g.smag24(10, 60).smag24(13, 0).at(16, 0x80)
		g.smag24(17, 0)   // LoV
		g.u24(20, 100000) // Dx = 100 km
		g.u24(23, 100000) // Dy
		g.at(26, 0)       // 投影中心标志
		g.at(27, 0)       // 扫描模式
		d, err := NewGRIB1(g.done())
		if err != nil {
			t.Fatalf("NewGRIB1: %v", err)
		}
		if d.kind != kindProjected || d.proj.tmpl != 20 {
			t.Fatalf("kind/tmpl = %v/%d", d.kind, d.proj.tmpl)
		}
		if d.proj.LaD != grib1PolarStereographicLaD {
			t.Errorf("LaD = %v, 想要常量 60", d.proj.LaD)
		}
		if d.proj.Dx != 100000 || d.proj.Dy != 100000 {
			t.Errorf("Dx/Dy = %v/%v", d.proj.Dx, d.proj.Dy)
		}
		if _, _, err := d.Point(0); err != nil {
			t.Errorf("Point(0): %v", err)
		}
	})

	t.Run("兰勃特", func(t *testing.T) {
		g := newGDS(GRIB1Lambert)
		g.b = append(g.b, make([]byte, 8)...) // 补到 octet 40
		g.u16(6, 3).u16(8, 2)
		g.smag24(10, 45).smag24(13, 0).at(16, 0x80)
		g.smag24(17, 262.5) // LoV
		g.u24(20, 3000)     // Dx
		g.u24(23, 3000)     // Dy
		g.at(26, 0).at(27, 0)
		g.smag24(28, 38.5).smag24(31, 38.5) // Latin1 = Latin2（退化单标准纬线分支）
		d, err := NewGRIB1(g.done())
		if err != nil {
			t.Fatalf("NewGRIB1: %v", err)
		}
		if d.proj.tmpl != 30 {
			t.Fatalf("tmpl = %d, 想要 30", d.proj.tmpl)
		}
		// GRIB1 没有独立 LaD 字段，ecCodes 用 Latin1 顶替
		if d.proj.LaD != d.proj.Latin1 {
			t.Errorf("LaD = %v 应当等于 Latin1 = %v", d.proj.LaD, d.proj.Latin1)
		}
		if _, _, err := d.Point(0); err != nil {
			t.Errorf("Point(0): %v", err)
		}
	})
}

func TestNewGRIB1Reduced(t *testing.T) {
	// 准规则：Ni 缺失（全 1），Nj=3，点数列表 3 项写在 octet 33 起（NV=0、PV=255）。
	g := latLonGDS(GRIB1LatLon, 0, 3, 10, 0, 0, 20, 10, 10, 0)
	g.b[6], g.b[7] = 0xFF, 0xFF // Ni 缺失
	g.b = append(g.b, make([]byte, 6)...)
	// 列表从 octet 33 起（0 基 32）：3 个 u16
	g.u16(32, 2).u16(34, 4).u16(36, 3)
	gds := g.done()

	d, err := NewGRIB1(gds)
	if err != nil {
		t.Fatalf("NewGRIB1: %v", err)
	}
	if d.kind != kindReduced {
		t.Fatalf("kind = %v, 想要 kindReduced", d.kind)
	}
	if !d.IsQuasiRegular() {
		t.Error("IsQuasiRegular 应当为 true")
	}
	if d.NumPoints() != 9 {
		t.Errorf("点数 = %d, 想要 2+4+3=9", d.NumPoints())
	}
	if _, _, err := d.Point(0); err != nil {
		t.Errorf("Point(0): %v", err)
	}
}

func TestNewGRIB1Rejects(t *testing.T) {
	cases := []struct {
		name string
		gds  []byte
	}{
		{"太短", []byte{0, 0, 4, 0, 255, 0}},
		{"长度前缀不符", func() []byte {
			g := latLonGDS(GRIB1LatLon, 2, 2, 10, 20, 0, 30, 10, 10, 0).done()
			g[2] = 31
			return g
		}()},
		{"Nj 缺失", func() []byte {
			g := latLonGDS(GRIB1LatLon, 2, 2, 10, 20, 0, 30, 10, 10, 0)
			g.b[8], g.b[9] = 0xFF, 0xFF
			return g.done()
		}()},
		{"扫描模式保留位", latLonGDS(GRIB1LatLon, 2, 2, 10, 20, 0, 30, 10, 10, 0x08).done()},
		{"投影网格 Nx 为 0", func() []byte {
			g := newGDS(GRIB1PolarStereo)
			g.u16(6, 0).u16(8, 2)
			return g.done()
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewGRIB1(tc.gds); err == nil {
				t.Fatal("应当报错")
			}
		})
	}
}

func TestNewGRIB1UnimplementedIsMetadataOnly(t *testing.T) {
	// 类型 1（Mercator）本包没实现坐标：应当成功返回元数据，Point 报 ErrNotImplemented。
	g := newGDS(1)
	g.u16(6, 4).u16(8, 4)
	d, err := NewGRIB1(g.done())
	if err != nil {
		t.Fatalf("未实现的类型不该在构造时报错: %v", err)
	}
	if d.Ni() != 4 || d.Nj() != 4 {
		t.Errorf("元数据 Ni/Nj = %d/%d，想要 4/4", d.Ni(), d.Nj())
	}
	if _, _, err := d.Point(0); err == nil {
		t.Fatal("未实现的类型取坐标应当报错")
	}
	if d.NumPoints() != 0 {
		t.Errorf("未实现类型的 NumPoints = %d, 想要 0", d.NumPoints())
	}
}

func TestU24AndSmag(t *testing.T) {
	// 直接钉住 octetio 里两个 GRIB1 原语的边界行为
	if got := octetio.U24([]byte{0x01, 0x02, 0x03}); got != 0x010203 {
		t.Errorf("U24 = %#x", got)
	}
	if got := octetio.Smag24([]byte{0x80, 0x27, 0x10}); got != -10000 {
		t.Errorf("Smag24(0x802710) = %d, 想要 -10000（符号-幅度）", got)
	}
	if got := octetio.Smag24([]byte{0x7F, 0xFF, 0xFF}); got != 0x7FFFFF {
		t.Errorf("Smag24 最大正值 = %d", got)
	}
	if got := octetio.Smag16([]byte{0x80, 0x26}); got != -38 {
		t.Errorf("Smag16(0x8026) = %d, 想要 -38", got)
	}
}
