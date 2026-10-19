//
// ：网格模板 3.20（极射赤面投影 polar stereographic）与 3.30（兰勃特
// 等角圆锥投影 Lambert conformal conic）的坐标生成。
//
// 数学部分逐字复刻 ecCodes 2.41.0 的 geo/iterator/PolarStereographic.cc 与
// LambertConformal.cc：相同的运算顺序、相同的字面常量、相同的归一化循环。
// IEEE-754 双精度运算是确定性的，因此在已验证的锚点（B 的 grid-t20t30-oracle.py
// 摘要集合）上，Go 输出与 ecCodes 的 lats_/lons_ 数组逐位一致（TestB_C38EndToEnd
// 中打包 f64 大端的 sha256 硬门；合成 fixture 用文本 %.10f 摘要）。
//
// 规范填充顺序：lat/lon 数组按“i 最快”
// 的行主序填充 —— x = i·Dx，y = j·Dy，从首个网格点锚点起算（PS：累加；LCC：
// x0 + i·Dx）——且 transform_iterator_data() 只置换取值数组，绝不改变 lats_/lons_。
// 因此坐标与扫描模式无关：对任意 scanMode，Point(i) 都有良定义。
//
// 字段解码（ecCodes template.3.20.def / .3.30.def）：octets 39-46 处的 La1/Lo1
// 本身以固定的 1e-6 微度为单位 —— 这些模板没有 basicAngle/subdivisions 字段
// （与 3.0 不同）。signed[4] = 符号-幅度（bit 31 为符号位，低 31 位为幅值）：
// 与模板 3.0 同一约定（ 裁定）—— 在 the Linux bench host 上对 t30 于 2026-10-07 重新探明：
// ecCodes 2.41 的 codes_set(latitudeOfFirstGridPoint=-45e6) 落地字节 82AEA540，
// 读回 -45.0°。unsigned[4] 字段为普通无符号。全 1 原始值 = 缺失 -> fail-loud。

package grid

import (
	"fmt"
	"math"
)

// ecCodes 2.41.0 geo/iterator 的字面常量（逐字取自 .cc 文件；不要用 math.Pi
// 推导值替代 —— 这些字面量本身就是契约）。
const (
	psRad2Deg = 57.29577951308232087684 // PolarStereographic.cc RAD2DEG
	psDeg2Rad = 0.01745329251994329576  // DEG2RAD
	psPiOver2 = 1.5707963267948966      // PI_OVER_2

	lccMpi    = 3.14159265358979323846 // M_PI (LambertConformal.cc)
	lccMpi2   = 1.57079632679489661923 // M_PI_2
	lccMpi4   = 0.78539816339744830962 // M_PI_4
	lccRadDeg = 57.29577951308232087684
	lccDegRad = 0.01745329251994329576

	projEpsilon = 1.0e-10 // 两个 .cc 文件里的 EPSILON
)

// projectedParams 是解析完整的模板 3.20/3.30 网格（）：角度为度，距离为米。
type projectedParams struct {
	tmpl   int // 20 或 30
	shape  int
	Ni, Nj int

	La1, Lo1 float64 // 第一个网格点（octets 39-46），度
	LaD      float64 // 指定 Dx/Dy 处的纬度（octets 47-50）
	Dx, Dy   float64 // 米（octets 55-62，×1e-3）

	orient float64 // t20：网格朝向（octets 51-54）= 中心经度
	LoV    float64 // t30：平行于 y 轴的经线经度（octets 51-54）
	Latin1 float64 // t30：第一条标准纬线（octets 65-68）
	Latin2 float64 // t30：第二条标准纬线（octets 69-72）

	scanMode uint8 // 仅元数据；坐标与扫描模式无关
}

// parseProjected 把模板 3.20/3.30 的字段读入 projectedParams。最小 section 长度：
// t20 为 65 字节，t30 为 81 字节（WMO 布局，与 ecCodes .def 字段表及 spec 包模板一致）。
func (g *Definition) parseProjected() error {
	tmpl := g.templateNumber
	minLen := 65
	if tmpl == 30 {
		minLen = 81
	}
	if len(g.sectionData) < minLen {
		return fmt.Errorf("grid: template %d section too short: %d bytes, want >= %d", tmpl, len(g.sectionData), minLen)
	}

	shape, err := g.u8(14) // octet 15（共享的 shape_of_the_earth 头）
	if err != nil {
		return err
	}
	g.shape = int(shape)
	rs, _ := g.u8(15)
	g.radiusScl = rs
	g.radiusRaw, _ = g.u32(16)
	ms, _ := g.u8(20)
	g.majorScl = ms
	g.majorRaw, _ = g.u32(21)
	ns, _ := g.u8(25)
	g.minorScl = ns
	g.minorRaw, _ = g.u32(26)

	ni, err := g.u32(30) // octets 31-34
	if err != nil {
		return err
	}
	nj, err := g.u32(34) // octets 35-38
	if err != nil {
		return err
	}
	if ni == missingU32 || nj == missingU32 {
		return fmt.Errorf("grid: template %d: Ni/Nj missing (all ones)", tmpl)
	}
	g.ni, g.nj = int(ni), int(nj)

	p := &projectedParams{tmpl: tmpl, shape: g.shape, Ni: int(ni), Nj: int(nj)}

	p.La1, err = g.projAngle(38) // octets 39-42（signed[4]，固定 1e-6）
	if err != nil {
		return err
	}
	p.Lo1, err = g.projUnsigned(42) // octets 43-46
	if err != nil {
		return err
	}
	p.LaD, err = g.projAngle(47) // octets 48-51
	if err != nil {
		return err
	}
	switch tmpl {
	case 20:
		p.orient, err = g.projAngle(51) // octets 52-55（signed[4]）
		if err != nil {
			return err
		}
	case 30:
		p.LoV, err = g.projUnsigned(51) // octets 52-55（unsigned[4]）
		if err != nil {
			return err
		}
	}
	diRaw, err := g.u32(55) // octets 56-59：Dx，单位为 1e-3 m
	if err != nil {
		return err
	}
	p.Dx = float64(diRaw) * 1e-3
	djRaw, err := g.u32(59) // octets 60-63：Dy，单位为 1e-3 m
	if err != nil {
		return err
	}
	p.Dy = float64(djRaw) * 1e-3
	if tmpl == 30 {
		p.Latin1, err = g.projAngle(65) // octets 66-69
		if err != nil {
			return err
		}
		p.Latin2, err = g.projAngle(69) // octets 70-73
		if err != nil {
			return err
		}
		// octets 74-81：投影南极的纬度/经度 —— WMO 布局中存在，球面迭代器不用（仅元数据）。
	}
	sm, err := g.u8(64) // octet 65：scanMode（flag table 3.4）
	if err != nil {
		return err
	}
	p.scanMode = sm

	g.proj = p
	return nil
}

// projAngle 以固定的 1e-6 微度为单位解码 signed[4] 角度
// （符号-幅度， 约定；全 1 = 缺失 -> 报错）。
func (g *Definition) projAngle(off int) (float64, error) {
	raw, err := g.u32(off)
	if err != nil {
		return 0, err
	}
	if raw == missingU32 {
		return 0, fmt.Errorf("grid: template %d: signed angle at octets %d-%d is missing (all ones)", g.templateNumber, off+1, off+4)
	}
	return float64(smagInt32(raw)) * 1e-6, nil
}

// projUnsigned 以固定的 1e-6 单位解码 unsigned[4] 字段。
func (g *Definition) projUnsigned(off int) (float64, error) {
	raw, err := g.u32(off)
	if err != nil {
		return 0, err
	}
	return float64(raw) * 1e-6, nil
}

// projectedPoints 按规范（数据）顺序生成 Ni×Nj 坐标表：i 最快，x = i·Dx / y = j·Dy，
// 从首个网格点锚点起算。
func (g *Definition) projectedPoints() ([]Point, error) {
	p := g.proj
	if p.Ni < 1 || p.Nj < 1 {
		return nil, fmt.Errorf("grid: template %d: Ni=%d Nj=%d, want >= 1", p.tmpl, p.Ni, p.Nj)
	}
	switch p.shape {
	case 0, 1, 6, 8: // 球面形状 —— ecCodes grib2 template.3.shape_of_the_earth.def：
		// earthIsOblate=false 严格对应 shape 0（R=6367470）、1（生产者给定半径）、
		// 6（R=6371229）、8（R=6371200）；其余值（2,3,4,5,7,9,10）均为扁球。
		// 已在 the Linux bench host 上对照安装的 ecCodes 2.41 核实：逐 shape 的 earthIsOblate 探明
		// 。
	default:
		// ecCodes PolarStereographic::init：“Only supported for spherical
		// earth”；LCC 的扁球分支（init_oblate）超出 范围。
		// 两种都 fail-loud。
		return nil, fmt.Errorf("grid: template %d: shape of earth %d is not spherical; polar stereographic / Lambert conformal coordinates require a sphere (eccodes: 'Only supported for spherical earth'; oblate LCC branch out of scope)", p.tmpl, p.shape)
	}
	if p.Dx <= 0 || p.Dy <= 0 {
		return nil, fmt.Errorf("grid: template %d: Dx=%g Dy=%g m, want > 0", p.tmpl, p.Dx, p.Dy)
	}
	radius := g.Radius()
	if math.IsNaN(radius) {
		return nil, fmt.Errorf("grid: template %d: unknown shape of earth %d (no radius)", p.tmpl, p.shape)
	}
	if p.tmpl == 20 {
		return psPoints(p, radius)
	}
	return lccPoints(p, radius)
}

// psPoints 逐字复刻 PolarStereographic::init（ecCodes 2.41.0）：先把首个点正向投影
// 到 x0/y0，再对规范 x/y 格点做逆向循环。为逐位一致，运算顺序与字面量均保留。
func psPoints(p *projectedParams, radius float64) ([]Point, error) {
	centralLongitude := p.orient * psDeg2Rad
	centralLatitude := p.LaD * psDeg2Rad
	lonFirst := p.Lo1 * psDeg2Rad
	latFirst := p.La1 * psDeg2Rad

	sign := 1.0
	if centralLatitude < 0 {
		sign = -1.0
	}
	ind := 0
	if math.Abs(math.Abs(centralLatitude)-psPiOver2) > projEpsilon {
		// 中心纬度不同于 ±90：非两极情形
		ind = 1
	}
	var mcs, tcs float64
	if ind == 1 {
		con1 := sign * centralLatitude
		mcs = math.Cos(con1)
		tcs = math.Tan(0.5 * (psPiOver2 - con1))
	}

	// 从初始 lat,lon 正向投影到初始 x,y。
	con1 := sign * (lonFirst - centralLongitude)
	ts := math.Tan(0.5 * (psPiOver2 - sign*latFirst))
	var height float64
	if ind == 1 {
		height = radius * mcs * ts / tcs
	} else {
		height = 2.0 * radius * ts
	}
	x0 := sign * height * math.Sin(con1)  // + false_easting (0)
	y0 := -sign * height * math.Cos(con1) // + false_northing (0)
	x0 = -x0
	y0 = -y0

	// 在规范格点上做逆向投影。x 按 += Dx 累加（而非 i*Dx），严格同 C 源码；y 每行同理。
	out := make([]Point, 0, p.Ni*p.Nj)
	y := 0.0
	for j := 0; j < p.Nj; j++ {
		x := 0.0
		for i := 0; i < p.Ni; i++ {
			_x := (x - x0) * sign
			_y := (y - y0) * sign
			rh := math.Sqrt(_x*_x + _y*_y)
			var t float64
			if ind == 1 {
				t = rh * tcs / (radius * mcs)
			} else {
				t = rh / (radius * 2.0)
			}
			latRad := sign * (psPiOver2 - 2*math.Atan(t))
			var lonRad float64
			if rh == 0 {
				lonRad = sign * centralLongitude
			} else {
				temp := math.Atan2(_x, -_y)
				lonRad = sign*temp + centralLongitude
			}
			lat := latRad * psRad2Deg
			lon := lonRad * psRad2Deg
			for lon < 0 {
				lon += 360
			}
			for lon > 360 {
				lon -= 360
			}
			out = append(out, Point{Lat: lat, Lon: lon})
			x += p.Dx
		}
		y += p.Dy
	}
	return out, nil
}

// lccPoints 逐字复刻 LambertConformal::init_sphere + xy2lonlat（ecCodes 2.41.0）。
// 为逐位一致，运算顺序与字面量均保留。
func lccPoints(p *projectedParams, radius float64) ([]Point, error) {
	latFirst := p.La1 * lccDegRad
	lonFirst := p.Lo1 * lccDegRad
	Latin1 := p.Latin1 * lccDegRad
	Latin2 := p.Latin2 * lccDegRad
	LaD := p.LaD * lccDegRad
	LoV := p.LoV * lccDegRad

	// ecCodes init()：标准纬线相等且分居赤道两侧时拒绝（EPSILON = 1e-10，以度检查）。
	if math.Abs(p.Latin1+p.Latin2) < projEpsilon {
		return nil, fmt.Errorf("grid: template 30: cannot have equal latitudes for standard parallels on opposite sides of equator (Latin1=%g Latin2=%g)", p.Latin1, p.Latin2)
	}

	var n float64
	if math.Abs(Latin1-Latin2) < 1e-9 {
		n = math.Sin(Latin1) // 退化的单标准纬线分支（C38）
	} else {
		n = math.Log(math.Cos(Latin1)/math.Cos(Latin2)) /
			math.Log(math.Tan(lccMpi4+Latin2/2.0)/math.Tan(lccMpi4+Latin1/2.0))
	}
	if n == 0 {
		return nil, fmt.Errorf("grid: template 30: conic constant n=0 (standard parallels degenerate), no valid projection")
	}

	f := (math.Cos(Latin1) * math.Pow(math.Tan(lccMpi4+Latin1/2.0), n)) / n
	rho := radius * f * math.Pow(math.Tan(lccMpi4+latFirst/2.0), -n)
	rho0Bare := f * math.Pow(math.Tan(lccMpi4+LaD/2.0), -n)
	rho0 := radius * rho0Bare
	lonDiff := lonFirst - LoV

	// 把经度调整到 (-180, 180] 范围（ecCodes：两条一次性 if）。
	if lonDiff > lccMpi {
		lonDiff -= 2 * lccMpi
	}
	if lonDiff < -lccMpi {
		lonDiff += 2 * lccMpi
	}
	angle := n * lonDiff
	x0 := rho * math.Sin(angle)
	y0 := rho0 - rho*math.Cos(angle)

	out := make([]Point, 0, p.Ni*p.Nj)
	for j := 0; j < p.Nj; j++ {
		y := y0 + float64(j)*p.Dy
		for i := 0; i < p.Ni; i++ {
			x := x0 + float64(i)*p.Dx

			// xy2lonlat(radius, n, f, rho0_bare, LoV, x, y):
			xx := x / radius
			yy := y / radius
			yy = rho0Bare - yy
			rh := math.Hypot(xx, yy)
			var lonDeg, latDeg float64
			if rh != 0.0 {
				if n < 0.0 {
					rh = -rh
					xx = -xx
					yy = -yy
				}
				latRad := 2.0*math.Atan(math.Pow(f/rh, 1.0/n)) - lccMpi2
				lonRad := math.Atan2(xx, yy) / n
				lonDeg = (lonRad + LoV) * lccRadDeg
				latDeg = latRad * lccRadDeg
			} else {
				lonDeg = 0.0
				if n > 0.0 {
					latDeg = lccMpi2 * lccRadDeg
				} else {
					latDeg = -lccMpi2 * lccRadDeg
				}
			}
			lonDeg = normaliseLonDegrees(lonDeg)
			out = append(out, Point{Lat: latDeg, Lon: lonDeg})
		}
	}
	return out, nil
}

// normaliseLonDegrees 复刻 ecCodes normalise_longitude_in_degrees
// （codes_util.cc）：小于 0 则加 360；大于 360 则减 360。结果落在 [0, 360]
// （恰好 360 保留 —— 循环条件是严格不等）。
func normaliseLonDegrees(lon float64) float64 {
	for lon < 0 {
		lon += 360
	}
	for lon > 360 {
		lon -= 360
	}
	return lon
}
