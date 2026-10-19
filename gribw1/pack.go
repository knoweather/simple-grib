package gribw1

import (
	"fmt"
	"math"
)

// plan 是编码计划：位宽、二进制比例因子 E、十进制比例因子 D。
// 必须在 PDS 之前定下来 —— PDS octets 27-28 要写 D，两个段必须同一个 D，
// 否则解码侧 (R + X·2^E)·10^(−D) 整体错位（值放大/缩小 10^D 倍）。
type plan struct {
	bits, e, d int
}

// planPacking 按 Params 计算编码计划。自动模式（Bits=0）下 D 取
// [0,8] 中令缩放域同时装进 23 位的最大值；显式模式直接用调用方给的参数。
func (p *Params) planPacking(vals []float64) (plan, error) {
	if p.Bits > 0 {
		return plan{bits: p.Bits, e: p.BinaryScale, d: p.DecimalScaleFactor}, nil
	}
	bits, e, d, ok := choosePacking(vals, p.DecimalScaleFactor)
	if !ok {
		return plan{}, fmt.Errorf("gribw1: 值域 [%g, %g] 在 D=%d 下超出 23 位安全范围（IBM 参考值/位宽装不下）；"+
			"请给 DecimalScaleFactor 调小、或显式 Bits/BinaryScale", minOf(vals), maxOf(vals), d)
	}
	if bits > 32 {
		return plan{}, fmt.Errorf("gribw1: 需要 %d 位宽才能表示该场，超出 GRIB1 上限 32", bits)
	}
	return plan{bits: bits, e: e, d: d}, nil
}

// encodeBDS 组装段 4（二进制数据段）：简单打包，公式
//
//	Y = (R + X · 2^E) · 10^(−D)
//
// 与 grib1/bds.go 的 unpack 严格互逆。打包策略：
//
//   - DecimalScaleFactor=0（默认）时**自动选 D**：在 [0,8] 里取最大的 D，
//     使得缩放后的整数场同时满足 |v| < 2^23 且极差 < 2^23（IBM 参考值 24 位尾数
//     装得下、位宽不超过 23），E 取 0 —— 十进制网格上**精确表示**，没有舍入漂移。
//   - Bits>0 时按显式位宽与 BinaryScale 编码（X = round((v·10^D − R)·2^(−E))，
//     越界 fail-loud），用于对字节级输出有确定性要求的场景。
//   - 全场等值（或位图选中值全等）时退化成位宽 0 的常量场，段里一个比特都不占。
//
// 失败即报错：NaN/Inf（validate 已挡）、值装不进给定位宽、所需位宽 > 32。
func (p *Params) encodeBDS(pl plan) ([]byte, error) {
	// 位图裁剪出实际编码的场（缺测点不进段 4）。
	vals := p.Values
	if p.Bitmap != nil {
		vals = make([]float64, 0, len(p.Values))
		for i, ok := range p.Bitmap {
			if ok {
				vals = append(vals, p.Values[i])
			}
		}
	}
	bits, e, d := pl.bits, pl.e, pl.d

	// 常量场（位宽 0）：整场等于 R·10^(−D)，载荷为空。
	if bits == 0 {
		constant := vals[0]
		ref := constant * math.Pow(10, float64(d))
		bds := make([]byte, 11)
		putU24(bds[0:], 11)
		bds[3] = 0 // 高半字节标志 = 0（简单打包）；低半字节未用位 = 0
		putSmag16(bds[4:], e)
		ibm := ibmEncode32(ref)
		copy(bds[6:10], ibm[:])
		bds[10] = 0 // 位宽 0
		return bds, nil
	}

	// 缩放域：scaled = v·10^D。R 取**未取整**的最小缩放值 —— E<0 时小数部分
	// 要由 X·2^E 承载（原文件就是这样存的，取整会把 -140.97314453125 砍成 -141）。
	scale := math.Pow(10, float64(d))
	scaled := make([]float64, len(vals))
	r := math.Inf(1)
	for i, v := range vals {
		sv := v * scale
		scaled[i] = sv
		if sv < r {
			r = sv
		}
	}
	// 参考值必须装得进 IBM 的 24 位尾数（自动选参已由 rangeFits 保证；显式
	// 参数路径在这里挡）。
	if math.Abs(r) >= 1<<23 {
		return nil, fmt.Errorf("gribw1: 参考值 R=%g 超出 IBM 24 位尾数范围，请调整 DecimalScaleFactor", r)
	}

	// X = round((scaled − R)·2^(−E))，逐值检查。
	shift := math.Pow(2, -float64(e))
	limit := uint64(1)<<uint(bits) - 1
	xs := make([]uint32, len(scaled))
	for i, sv := range scaled {
		x := uint64(math.Round((sv - r) * shift))
		if x > limit {
			return nil, fmt.Errorf("gribw1: values[%d]=%v 编码为 X=%d，超出位宽 %d 的上限 %d；"+
				"请加大 Bits 或调 BinaryScale", i, vals[i], x, bits, limit)
		}
		xs[i] = uint32(x)
	}

	payload := packBits(bits, xs)
	unused := len(payload)*8 - len(xs)*bits
	total := 11 + len(payload)
	if total%2 != 0 {
		// 段长补偶数（GRIB1 规定）；多出的 1 字节落进"未用位"里由解码侧按
		// 同一把尺子（载荷比特数 − 值数×位宽）剥掉。
		payload = append(payload, 0)
		total++
		unused += 8
	}
	if unused < 0 || unused > 15 {
		return nil, fmt.Errorf("gribw1: 内参错误：未用位 %d 超出低半字节范围（这不该发生）", unused)
	}

	bds := make([]byte, 11, total)
	putU24(bds[0:], total)
	bds[3] = byte(unused) // 高半字节标志 = 0（简单打包）
	putSmag16(bds[4:], e)
	ibm := ibmEncode32(r)
	copy(bds[6:10], ibm[:])
	bds[10] = byte(bits)
	return append(bds, payload...), nil
}

// choosePacking 自动选 (bits, E, D)：E 恒 0，D 取满足约束的最大值。
// 约束（都为了 IBM 24 位尾数与合理位宽）：|scaled| < 2^23、极差 < 2^23。
// requestedD > 0 时只试这一个 D（调用方钦定，要么能用要么报错）。
func choosePacking(vals []float64, requestedD int) (bits, e, d int, ok bool) {
	mn, mx := minOf(vals), maxOf(vals)
	if requestedD != 0 {
		if !rangeFits(mn, mx, requestedD) {
			return 0, 0, requestedD, false
		}
		return bitsFor(mn, mx, requestedD), 0, requestedD, true
	}
	for cand := 8; cand >= 0; cand-- {
		if rangeFits(mn, mx, cand) {
			return bitsFor(mn, mx, cand), 0, cand, true
		}
	}
	return 0, 0, 0, false
}

// rangeFits 检查"D 下缩放后的整数域是否同时装得下绝对值与极差"。
func rangeFits(mn, mx float64, d int) bool {
	scale := math.Pow(10, float64(d))
	return math.Abs(mx*scale) < (1<<23) && math.Abs(mn*scale) < (1<<23) &&
		(mx-mn)*scale < (1<<23)
}

// bitsFor 返回表示极差所需的最低位宽（极差 0 = 常量场 = 0 位）。
func bitsFor(mn, mx float64, d int) int {
	if mn == mx {
		return 0
	}
	diff := uint64(int64(math.Round(mx*math.Pow(10, float64(d)))) -
		int64(math.Round(mn*math.Pow(10, float64(d)))))
	bits := 0
	for diff > 0 {
		bits++
		diff >>= 1
	}
	return bits
}

// packBits 把 X 数组按 MSB 优先打进比特流（与 grib1 的 BitReader 严格互逆）。
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

func minOf(v []float64) float64 {
	m := v[0]
	for _, x := range v[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

func maxOf(v []float64) float64 {
	m := v[0]
	for _, x := range v[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

// ibmEncode32 把 float64 编成 GRIB1 的 IBM 单精度（4 字节大端）。
// 与 internal/octetio.IBMFloat32 互逆。入参必须在 24 位尾数能力内
// （调用方已由 rangeFits/显式校验保证）；超出部分由 Round 收敛并可能损失精度 ——
// 所以自动选参才死死盯住 |R| < 2^23。
func ibmEncode32(v float64) [4]byte {
	var out [4]byte
	if v == 0 {
		return out
	}
	sign := byte(0)
	if v < 0 {
		sign, v = 0x80, -v
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
	if m >= 1<<24 { // 四舍五入进位时重新归一化
		m >>= 1
		exp++
	}
	out[0] = sign | byte(exp)
	out[1] = byte(m >> 16)
	out[2] = byte(m >> 8)
	out[3] = byte(m)
	return out
}
