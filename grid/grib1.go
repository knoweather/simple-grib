//
// GRIB1 的段 2（GDS，网格描述段）→ grid.Definition。
//
// 为什么放在 grid 包而不是 grib1 包：坐标生成器（规则/Gaussian/投影/旋转/准规则）
// 已经在 GRIB2 侧对照 ecCodes 逐位验证过，是这套代码里最贵的资产。GRIB1 与 GRIB2
// 只是**字节布局**不同，几何语义完全一样，所以正确做法是加一个「另一种字节布局的
// 构造器」，而不是在 grib1 包里重写一套坐标数学。
//
// 字段偏移全部对照 ecCodes 的 GRIB1 定义文件逐条核对（本机缓存
// .testcache/eccsrc/eccodes-develop/definitions/grib1/）：
//
//	grid_definition_latlon.def     类型 0/10 的公共块
//	grid_definition_gaussian.def   类型 4
//	grid_definition_5.def          类型 5 极射赤面
//	grid_definition_lambert.def    类型 3 兰勃特等角
//	grid_first_last_resandcomp.def La1/Lo1/分辨率标志/La2/Lo2
//	scanning_mode.def              扫描模式
//	grid_rotation.def              旋转参数（南极纬度/经度 + 旋转角）
//	section.2.def                  公共头 + NV/PV/PL 区
//
// 几处容易踩的点，已在代码里逐条注明：
//   - 经纬度是 signed[3] 符号-幅度、单位 1/1000 度（ecCodes grib1divider = 1000）；
//   - Di/Dj 是**无符号**的幅值，方向由扫描模式承载（与 GRIB2 3.0 同口径）；
//   - 极射赤面的 LaD 是常量 60°，兰勃特的 LaD 取 Latin1（GRIB1 没有独立 LaD 字段）；
//   - 旋转角是 IBM 浮点，不是 IEEE；
//   - 扫描模式只有位 1-3 有定义，位 4-8 非零意味着扫描顺序未知 → fail-loud。

package grid

import (
	"fmt"
	"math"

	"github.com/knoweather/simple-grib/internal/octetio"
)

// GRIB1 GDS 的数据表示类型（WMO 表 6）里本包实现了坐标的那些。
const (
	GRIB1LatLon        = 0  // 等距圆柱 / 规则经纬
	GRIB1Lambert       = 3  // 兰勃特等角圆锥
	GRIB1Gaussian      = 4  // 高斯经纬
	GRIB1PolarStereo   = 5  // 极射赤面
	GRIB1RotatedLatLon = 10 // 旋转经纬
)

// grib1Divider 是 GRIB1 经纬度的缩放：signed[3] 以 1/1000 度为单位
// （ecCodes definitions/boot.def: constant grib1divider = 1000）。
const grib1Divider = 1000

// 段 2 的公共头里两个哨兵/常量。
const (
	grib1PVAbsent              = 255    // octet 5 = 255：没有垂直坐标参数、也没有点数列表
	grib1NiMissing             = 0xFFFF // Ni/Nj 全 1 = 缺失（准规则网格用它表示"每行不同"）
	grib1ScanUndef             = 0x1F   // 扫描模式位 4-8：GRIB1 未定义，非零即 fail-loud
	grib1PolarStereographicLaD = 60     // 类型 5 的 LaD 是规范钦定常量
)

// NewGRIB1 从 GRIB1 的段 2（GDS）字节构造 Definition。gds 从段 2 的 octet 1 起，
// 含 3 字节长度前缀。
//
// 与 New（GRIB2 section 3）的关系：两者产出同一种 Definition，因此坐标访问器
// （Point/Lats/Lons/Ni/Nj/NumPoints/Radius…）对两个版本完全一致。
//
// 未实现的类型（1 Mercator、6 UTM、13 斜兰勃特、50 球谐……）不报错：返回仅元数据的
// Definition，Point/Lats/Lons 报 ErrNotImplemented —— 与 GRIB2 侧「未知模板优雅降级」
// 的契约一致。
func NewGRIB1(gds []byte) (*Definition, error) {
	if len(gds) < 6 {
		return nil, fmt.Errorf("grid: GRIB1 GDS too short: %d bytes, want >= 6", len(gds))
	}
	// 长度前缀必须与实际字节数一致：不一致意味着调用方切错了段，继续解析只会
	// 在别处炸得更难懂。
	if n := int(octetio.U24(gds[0:3])); n != len(gds) {
		return nil, fmt.Errorf("grid: GRIB1 GDS length field is %d, section has %d bytes", n, len(gds))
	}
	reprType := int(gds[5]) // octet 6
	nv := int(gds[3])       // octet 4
	pv := int(gds[4])       // octet 5

	g := &Definition{
		sectionData:    gds,
		edition:        1,
		templateNumber: reprType,
	}
	switch reprType {
	case GRIB1LatLon, GRIB1RotatedLatLon, GRIB1Gaussian:
		if err := g.parseGRIB1LatLon(reprType); err != nil {
			return nil, err
		}
	case GRIB1PolarStereo, GRIB1Lambert:
		if err := g.parseGRIB1Projected(reprType); err != nil {
			return nil, err
		}
	default:
		// 仅元数据：Ni/Nj 对多数类型仍在 octets 7-10，尽力读一下，读不到就是 0。
		g.kind = kindNone
		if len(gds) >= 10 {
			if ni := int(octetio.U16At(gds, 6)); ni != grib1NiMissing {
				g.ni = ni
			}
			if nj := int(octetio.U16At(gds, 8)); nj != grib1NiMissing {
				g.nj = nj
			}
		}
	}
	if err := g.validateCounts(); err != nil {
		return nil, err
	}
	// 准规则判定要在读点数列表之后做，所以放在公共流程的末尾。
	if err := g.resolveGRIB1Reduced(nv, pv); err != nil {
		return nil, err
	}
	return g, nil
}

// parseGRIB1LatLon 解析类型 0（规则经纬）、4（高斯）、10（旋转经纬）。
// 三者共享 La1/Lo1/分辨率标志/La2/Lo2 这一块，差别只在 octets 26-27 的含义
// （0/10 是 Dj，4 是高斯 N）与尾部（10 追加旋转参数）。
func (g *Definition) parseGRIB1LatLon(reprType int) error {
	b := g.sectionData
	// 最短长度：类型 0 需要到 octet 28（扫描模式）；类型 10 还要 10 字节旋转参数；
	// 类型 4 是 28 + 4 字节填充。
	minLen := 28
	if reprType == GRIB1RotatedLatLon {
		minLen = 38
	}
	if len(b) < minLen {
		return fmt.Errorf("grid: GRIB1 GDS type %d needs %d bytes, has %d", reprType, minLen, len(b))
	}

	niRaw := int(octetio.U16At(b, 6)) // octets 7-8
	njRaw := int(octetio.U16At(b, 8)) // octets 9-10
	if njRaw == grib1NiMissing {
		return fmt.Errorf("grid: GRIB1 GDS type %d: Nj is missing (all ones); a grid without a row count cannot be sized", reprType)
	}
	if niRaw != grib1NiMissing {
		g.ni = niRaw
	}
	g.nj = njRaw

	// La1/Lo1/La2/Lo2：signed[3] 符号-幅度，1/1000 度。
	g.La1 = float64(octetio.Smag24(b[10:13])) / grib1Divider // octets 11-13
	g.Lo1 = float64(octetio.Smag24(b[13:16])) / grib1Divider // octets 14-16
	g.resFlags = b[16]                                       // octet 17
	g.La2 = float64(octetio.Smag24(b[17:20])) / grib1Divider // octets 18-20
	g.Lo2 = float64(octetio.Smag24(b[20:23])) / grib1Divider // octets 21-23
	// The four encoded corners are available (Bounds()); the GRIB2 side sets
	// the same flag in parseLatLonBase.
	g.hasCorners = true

	// Di：unsigned[2]，1/1000 度。方向由扫描模式承载，所以这里只取幅值。
	g.Di = float64(octetio.U16At(b, 23)) / grib1Divider // octets 24-25

	// octets 26-27：类型 0/10 是 Dj，类型 4 是高斯的 N。
	if reprType == GRIB1Gaussian {
		n := int(octetio.U16At(b, 25))
		if n <= 0 {
			return fmt.Errorf("grid: GRIB1 GDS type 4: N (octets 26-27) = %d, want > 0", n)
		}
		if n > maxGaussianN {
			return fmt.Errorf("grid: GRIB1 GDS type 4: N=%d exceeds the supported maximum %d", n, maxGaussianN)
		}
		g.gaussN = n
		// 高斯网格的 Dj 由 La1/La2 与 Nj 推出（平行圈位置由 N 决定，不是等间距）。
		if njRaw > 1 {
			g.Dj = math.Abs(g.La2-g.La1) / float64(njRaw-1)
		} else {
			g.Dj = 0
		}
		g.kind = kindGaussian
		// 平行圈窗口与 GRIB2 3.40/3.41 完全同一套逻辑（全球网格 Nj==2N 用全列表，
		// 子区按 La1 定位起点）：必须在这里解析，否则 gaussStart/gaussJDir 是零值，
		// 每一行都会落到 full[0]。
		if err := g.resolveGaussianWindow(); err != nil {
			return fmt.Errorf("grid: GRIB1 GDS type 4: %w", err)
		}
	} else {
		g.Dj = float64(octetio.U16At(b, 25)) / grib1Divider // octets 26-27
		g.kind = kindRegular
	}

	g.scanMode = b[27] // octet 28
	if err := checkGRIB1ScanMode(g.scanMode); err != nil {
		return err
	}

	// 地球形状：GRIB1 默认是半径 6367.47 km 的球（ecCodes section.2.def 的
	// transient radius=6367470 / shapeOfTheEarth=0）；表 7 的位 2 置位时改成
	// IAU 1965 扁球（半长轴 6378160）。grid 的 shape 码里 0 = 该球、2 = 6378160。
	if g.resFlags&0x40 != 0 {
		g.shape = 2
	} else {
		g.shape = 0
	}

	// 类型 10：旋转参数（南极纬度/经度 signed[3]，旋转角 IBM 浮点）。
	if reprType == GRIB1RotatedLatLon {
		g.spLat = float64(octetio.Smag24(b[28:31])) / grib1Divider // octets 29-31
		g.spLon = float64(octetio.Smag24(b[31:34])) / grib1Divider // octets 32-34
		g.rotAngle = octetio.IBMFloat32(b[34:38])                  // octets 35-38
		g.rotated = true
	}
	return nil
}

// parseGRIB1Projected 解析类型 5（极射赤面）与类型 3（兰勃特等角）。
// 两者在 octets 1-28 上布局相同，类型 3 在 29-40 追加标准纬线与南极。
func (g *Definition) parseGRIB1Projected(reprType int) error {
	b := g.sectionData
	minLen := 28
	if reprType == GRIB1Lambert {
		minLen = 40
	}
	if len(b) < minLen {
		return fmt.Errorf("grid: GRIB1 GDS type %d needs %d bytes, has %d", reprType, minLen, len(b))
	}

	g.ni = int(octetio.U16At(b, 6)) // octets 7-8  Nx
	g.nj = int(octetio.U16At(b, 8)) // octets 9-10 Ny
	if g.ni <= 0 || g.nj <= 0 {
		return fmt.Errorf("grid: GRIB1 GDS type %d: Nx=%d Ny=%d, want >= 1", reprType, g.ni, g.nj)
	}

	g.La1 = float64(octetio.Smag24(b[10:13])) / grib1Divider // octets 11-13
	g.Lo1 = float64(octetio.Smag24(b[13:16])) / grib1Divider // octets 14-16
	g.resFlags = b[16]                                       // octet 17
	lov := float64(octetio.Smag24(b[17:20])) / grib1Divider  // octets 18-20
	dx := float64(octetio.U24(b[20:23]))                     // octets 21-23，单位米
	dy := float64(octetio.U24(b[23:26]))                     // octets 24-26，单位米
	g.scanMode = b[27]                                       // octet 28
	if err := checkGRIB1ScanMode(g.scanMode); err != nil {
		return err
	}
	if dx <= 0 || dy <= 0 {
		return fmt.Errorf("grid: GRIB1 GDS type %d: Dx=%g Dy=%g m, want > 0", reprType, dx, dy)
	}

	if g.resFlags&0x40 != 0 {
		g.shape = 2
	} else {
		g.shape = 0
	}

	// 复用 GRIB2 侧已验证的投影数学：projectedParams 的 tmpl 决定用哪套迭代器
	// （20 = 极射赤面，30 = 兰勃特），字段语义与 GRIB2 3.20/3.30 完全一致。
	p := &projectedParams{
		Ni: g.ni, Nj: g.nj, shape: g.shape,
		La1: g.La1, Lo1: g.Lo1,
		Dx: dx, Dy: dy,
		scanMode: g.scanMode,
	}
	if reprType == GRIB1PolarStereo {
		p.tmpl = 20
		p.orient = lov
		p.LaD = grib1PolarStereographicLaD // 类型 5 的 LaD 是常量 60°
	} else {
		p.tmpl = 30
		p.LoV = lov
		p.Latin1 = float64(octetio.Smag24(b[28:31])) / grib1Divider // octets 29-31
		p.Latin2 = float64(octetio.Smag24(b[31:34])) / grib1Divider // octets 32-34
		// GRIB1 没有独立的 LaD 字段，ecCodes 用 Latin1 顶替
		// （grid_definition_lambert.def: constant LaDInDegrees = Latin1InDegrees）。
		p.LaD = p.Latin1
	}
	g.proj = p
	g.kind = kindProjected
	return nil
}

// resolveGRIB1Reduced 判定并读取准规则（reduced）网格的点数列表。
//
// 触发条件（WMO + ecCodes section.2.def）：**Ni 缺失**（全 1）且段长足够容纳
// Nj 个 2 字节的点数。列表位置：先按 octet 5 的 PV 跳到该 octet（PV = 255 时
// 按规范跳到 octet 33），跳过 NV 个 4 字节的垂直坐标参数，其后就是点数列表。
//
// Ni 存在时一律按规则网格处理 —— 那是网格定义自己声明的几何，不做猜测。
func (g *Definition) resolveGRIB1Reduced(nv, pv int) error {
	if g.kind != kindRegular && g.kind != kindGaussian {
		return nil
	}
	if g.ni != 0 || g.nj <= 0 {
		return nil // Ni 存在 => 规则网格；或 Nj 未知 => 上面已经报错
	}
	b := g.sectionData

	pvOctet := 33 // 规范默认：PV 区从 octet 33 起
	if pv != grib1PVAbsent {
		if pv < 33 {
			return fmt.Errorf("grid: GRIB1 GDS: PV (octet 5) = %d, want 255 or >= 33", pv)
		}
		pvOctet = pv
	}
	plOctet := pvOctet + nv*4 // 跳过 NV 个 4 字节垂直坐标参数
	plBytes := g.nj * 2
	if len(b) < plOctet-1+plBytes {
		return fmt.Errorf("grid: GRIB1 GDS: Ni is missing (quasi-regular) but the section is %d bytes; a %d-entry point list would need %d bytes from octet %d",
			len(b), g.nj, plBytes, plOctet)
	}

	pl := make([]int, g.nj)
	for i := 0; i < g.nj; i++ {
		pl[i] = int(octetio.U16At(b, plOctet-1+i*2))
	}
	g.reduced = &reducedParams{La1: g.La1, Lo1: g.Lo1, La2: g.La2, Lo2: g.Lo2, Nj: g.nj, Pl: pl}
	g.kind = kindReduced
	return nil
}

// checkGRIB1ScanMode 校验扫描模式字节。
//
// GRIB1 的表 8 只定义了位 1-3（i 方向 / j 方向 / i 还是 j 连续），位 4-8 是保留位。
// 位 4-8 非零意味着文件的扫描顺序用了我们不知道的约定 —— 猜一个顺序会给出几何上
// 错位的坐标，所以这里直接 fail-loud。位 1-3 与 GRIB2 flag table 3.4 的位 1-3 完全
// 同义，因此 scanMode 可以原样交给共享的坐标生成器。
func checkGRIB1ScanMode(sm uint8) error {
	if sm&grib1ScanUndef != 0 {
		return fmt.Errorf("grid: GRIB1 GDS: scanning mode %#02x sets reserved bit(s) 4-8 (%#02x); the scan order is undefined in GRIB1",
			sm, sm&grib1ScanUndef)
	}
	return nil
}
