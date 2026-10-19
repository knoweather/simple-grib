package gribw1

import (
	"fmt"
	"math"
	"time"

	"github.com/knoweather/simple-grib/grib1"
)

// Params 是一条 GRIB1 消息的完整描述。零值不是合法消息 —— Encode 会逐项校验。
//
// 与 gribw.Params/Message builder 的差别：GRIB1 没有模板号、没有参数类别，
// 一切都是固定 octet 位置，所以一个结构体说清楚就行，不需要链式 setter。
type Params struct {
	// ---- 段 1 PDS：标识 ----
	Centre            int       // octet 5，通用表 C-1
	SubCentre         int       // octet 26（中心自定义）
	TableVersion      int       // octet 4，表 2 的版本号
	GeneratingProcess int       // octet 6（中心自定义）
	Parameter         int       // octet 9，表 2 的参数指示码
	ReferenceTime     time.Time // octets 13-17 + 25（世纪）；零值 fail-loud

	// ---- 段 1 PDS：层 ----
	LevelType   int // octet 10，表 3
	LevelTop    int // octets 11-12（单层：2 字节；层组：上界 1 字节）
	LevelBottom int // 层组类型的下界（octet 12）；单层时可留 0

	// ---- 段 1 PDS：时效与比例 ----
	TimeUnit           int // octet 18，表 4（1 = 小时）
	P1                 int // octet 19
	P2                 int // octet 20
	TimeRangeIndicator int // octet 21，表 5
	DecimalScaleFactor int // octets 27-28（符号-幅度）；0 = 自动选择（见 Encode）

	// ---- 段 2 GDS：类型 0（规则经纬）/ 类型 4（高斯，GaussianN > 0） ----
	Ni, Nj             int     // octets 7-10
	La1, Lo1, La2, Lo2 float64 // 度（符号-幅度写入，1/1000 度）
	Di, Dj             float64 // 度（无符号幅值写入；类型 4 的 Dj 由 N 决定，此字段忽略）
	ScanMode           uint8   // octet 28；位 1-3 有效，位 4-8 必须为 0
	GaussianN          int     // > 0 时写类型 4（octets 26-27 的高斯 N）

	// ---- 段 3 BMS（可选） ----
	// Bitmap 与 Values 等长：true = 该点有值。nil = 不写位图段。
	Bitmap []bool

	// ---- 段 4 BDS：数值 ----
	Values []float64 // 扫描顺序的场值；NaN/Inf fail-loud（简单打包表示不了）
	// Bits / BinaryScale 给"显式控制"用：Bits=0（默认）= 自动选位宽与 D；
	// Bits>0 时按给定位宽编码，BinaryScale 给出的 E 默认 0。
	Bits        int
	BinaryScale int

	// ---- 可选 ----
	Octets29To40 []byte // PDS 保留区自定义内容；不给则写 0（长度必须 ≤ 12）
}

// Encode 把一条消息编码成完整字节（IS..7777）。它是 grib1.Parse 的逆操作：
// gribw1.Encode 产出的字节必须能被 grib1.Parse 原样读回。
func Encode(p Params) ([]byte, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}

	// 位图裁剪后的实际编码场（缺测点不进段 4，也不参与选参）。
	codedVals := p.Values
	if p.Bitmap != nil {
		codedVals = make([]float64, 0, len(p.Values))
		for i, ok := range p.Bitmap {
			if ok {
				codedVals = append(codedVals, p.Values[i])
			}
		}
		if len(codedVals) == 0 {
			return nil, fmt.Errorf("gribw1: 位图全是缺测点，没有可编码的值")
		}
	}

	// 编码计划先行：PDS 的十进制比例因子与 BDS 的打包必须同一个 D。
	pl, err := p.planPacking(codedVals)
	if err != nil {
		return nil, err
	}
	pds, err := p.encodePDS(pl)
	if err != nil {
		return nil, err
	}
	gds, err := p.encodeGDS()
	if err != nil {
		return nil, err
	}

	// 位图段（可选）。位图覆盖的点必须与 Values 等长。
	var bms []byte
	if p.Bitmap != nil {
		if len(p.Bitmap) != len(p.Values) {
			return nil, fmt.Errorf("gribw1: bitmap length %d != values %d", len(p.Bitmap), len(p.Values))
		}
		// 全 true 的位图没有信息量：GRIB1 允许不写段 3（不写更小）。
		if !allTrue(p.Bitmap) {
			bms = encodeBMS(p.Bitmap)
		}
	}
	_ = codedVals

	bds, err := p.encodeBDS(pl)
	if err != nil {
		return nil, err
	}

	// 段 0 总长：8 + PDS + [GDS] + [BMS] + BDS + 4。
	total := 8 + len(pds) + len(gds) + len(bms) + len(bds) + 4
	out := make([]byte, 8, total)
	copy(out[0:4], "GRIB")
	out[4], out[5], out[6] = byte(total>>16), byte(total>>8), byte(total)
	out[7] = 1
	out = append(out, pds...)
	out = append(out, gds...)
	if bms != nil {
		out = append(out, bms...)
	}
	out = append(out, bds...)
	return append(out, '7', '7', '7', '7'), nil
}

// File 把多条消息拼成一个多消息文件（每条消息自己带 IS/7777）。
// 对拍测试里常这么用：GRIB1 与 GRIB2 消息混装验证 edition 分派。
func File(msgs ...[]byte) []byte {
	var out []byte
	for _, m := range msgs {
		out = append(out, m...)
	}
	return out
}

// validate 校验字段范围。GRIB1 的字段几乎都是 1 字节，溢出必须在这里挡住，
// 否则字节会被静默截断、写出一个"结构合法但语义错"的文件。
func (p *Params) validate() error {
	if p.ReferenceTime.IsZero() {
		return fmt.Errorf("gribw1: ReferenceTime 不能是零值（GRIB1 的参考时间是必填字段）")
	}
	if p.Values == nil {
		return fmt.Errorf("gribw1: Values 是必填（常量场也给一段等值序列）")
	}
	for i, v := range p.Values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("gribw1: values[%d] = %v：GRIB1 简单打包表示不了 NaN/Inf", i, v)
		}
	}
	if p.Ni <= 0 || p.Nj <= 0 {
		return fmt.Errorf("gribw1: Ni=%d Nj=%d，想要 >= 1", p.Ni, p.Nj)
	}
	if p.Bitmap == nil && len(p.Values) != p.Ni*p.Nj {
		return fmt.Errorf("gribw1: 值数 %d != Ni×Nj = %d（有位图时值数须等于置位数）",
			len(p.Values), p.Ni*p.Nj)
	}
	if p.Ni > 0xFFFF || p.Nj > 0xFFFF {
		return fmt.Errorf("gribw1: Ni/Nj 超出 16 位字段")
	}
	if p.Centre < 0 || p.Centre > 0xFF {
		return fmt.Errorf("gribw1: Centre %d 超出 1 字节", p.Centre)
	}
	if p.SubCentre < 0 || p.SubCentre > 0xFF {
		return fmt.Errorf("gribw1: SubCentre %d 超出 1 字节", p.SubCentre)
	}
	if p.TableVersion < 0 || p.TableVersion > 0xFF {
		return fmt.Errorf("gribw1: TableVersion %d 超出 1 字节", p.TableVersion)
	}
	if p.GeneratingProcess < 0 || p.GeneratingProcess > 0xFF {
		return fmt.Errorf("gribw1: GeneratingProcess %d 超出 1 字节", p.GeneratingProcess)
	}
	if p.Parameter < 0 || p.Parameter > 0xFF {
		return fmt.Errorf("gribw1: Parameter %d 超出 1 字节", p.Parameter)
	}
	if p.LevelType < 0 || p.LevelType > 0xFF {
		return fmt.Errorf("gribw1: LevelType %d 超出 1 字节", p.LevelType)
	}
	if p.LevelTop < 0 || p.LevelTop > 0xFFFF {
		return fmt.Errorf("gribw1: LevelTop %d 超界（单层 2 字节 0..65535）", p.LevelTop)
	}
	if grib1.IsLayerRangeLevelType(p.LevelType) && (p.LevelBottom < 0 || p.LevelBottom > 0xFF) {
		return fmt.Errorf("gribw1: 层组类型 %d 的下界 %d 超界（1 字节 0..255）", p.LevelType, p.LevelBottom)
	}
	if p.TimeUnit < 0 || p.TimeUnit > 0xFF || p.TimeRangeIndicator < 0 || p.TimeRangeIndicator > 0xFF {
		return fmt.Errorf("gribw1: 时间单位/时间范围指示码超出 1 字节")
	}
	if p.P1 < 0 || p.P1 > 0xFF || p.P2 < 0 || p.P2 > 0xFF {
		return fmt.Errorf("gribw1: P1/P2 超出 1 字节")
	}
	if p.DecimalScaleFactor < -32767 || p.DecimalScaleFactor > 32767 {
		return fmt.Errorf("gribw1: DecimalScaleFactor %d 超出符号-幅度 int16", p.DecimalScaleFactor)
	}
	if p.Bits < 0 || p.Bits > 32 {
		return fmt.Errorf("gribw1: Bits %d 超出 [0,32]", p.Bits)
	}
	if p.BinaryScale < -32767 || p.BinaryScale > 32767 {
		return fmt.Errorf("gribw1: BinaryScale %d 超出符号-幅度 int16", p.BinaryScale)
	}
	if p.ScanMode&0x1F != 0 {
		return fmt.Errorf("gribw1: ScanMode %#02x 置了保留位 4-8（GRIB1 未定义的扫描顺序）", p.ScanMode)
	}
	if p.GaussianN < 0 || p.GaussianN > 0xFFFF {
		return fmt.Errorf("gribw1: GaussianN %d 超界", p.GaussianN)
	}
	if p.GaussianN > 0 && p.GaussianN > maxGaussianN {
		return fmt.Errorf("gribw1: GaussianN %d 超出支持上限 %d", p.GaussianN, maxGaussianN)
	}
	// 经纬度必须能写进 3 字节符号-幅度（±8388.607° 分辨率 0.001°）
	for _, c := range []struct {
		name string
		v    float64
	}{{"La1", p.La1}, {"Lo1", p.Lo1}, {"La2", p.La2}, {"Lo2", p.Lo2}} {
		if math.Abs(c.v) > 8388.607 {
			return fmt.Errorf("gribw1: %s = %v 超出 signed[3] 的 ±8388.607°", c.name, c.v)
		}
	}
	for _, c := range []struct {
		name string
		v    float64
	}{{"Di", p.Di}, {"Dj", p.Dj}} {
		if c.v < 0 || c.v > 65535 {
			return fmt.Errorf("gribw1: %s = %v 超出 unsigned[2]（0..65535，单位 1/1000 度）", c.name, c.v)
		}
	}
	if len(p.Octets29To40) > 12 {
		return fmt.Errorf("gribw1: Octets29To40 长度 %d > 12（PDS 保留区只有 octets 29-40）", len(p.Octets29To40))
	}
	// 层组类型必须给两个层值；非层组类型给两个是一头雾水 + 浪费（只写 2 字节那个）。
	if grib1.IsLayerRangeLevelType(p.LevelType) && p.LevelBottom == 0 && p.LevelTop != 0 {
		return fmt.Errorf("gribw1: 层类型 %d 是层组（两个 1 字节），LevelBottom 必须一起给", p.LevelType)
	}
	return nil
}

// maxGaussianN 与 grid 包同口径（高斯 N 的上限）。
const maxGaussianN = 1024

func allTrue(b []bool) bool {
	for _, v := range b {
		if !v {
			return false
		}
	}
	return true
}
