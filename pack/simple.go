package pack

import (
	"fmt"
	"math"
)

// 数据表示模板号，用作注册表键（WMO 表 5.0 的模板号，即线上值）。
//
// Data-representation template numbers, used as registry keys (WMO table
// 5.0 / template numbers, on-wire values).
const (
	TemplateSimple   = 0     // 网格点数据 - 简单打包 / grid point data - simple packing
	TemplateIEEE     = 4     // 网格点数据 - IEEE 浮点 / grid point data - IEEE floating point
	TemplateJPEG2000 = 40    // JPEG 2000 码流 / JPEG 2000 code stream
	TemplateWebP     = 49152 // 本地使用：WebP 无损（占位）/ local use: WebP lossless (placeholder)
)

// ErrUnsupportedValue 报告模板字段值超出内置解码器支持范围。策略是
// fail-loud（响亮失败）：解码器绝不会替调用方猜测默认值。
//
// ErrUnsupportedValue reports a template field value outside the range the
// built-in decoders support. Policy is fail-loud: a decoder never guesses a
// default value on your behalf.
type ErrUnsupportedValue struct {
	// Template：数据表示模板号 / data-representation template number
	Template int
	// Field：字段名，例如 "type of original field values" / field name, e.g. "type of original field values"
	Field string
	// Value：越界的原始值 / the offending raw value
	Value int64
}

// Error 实现 error 接口，格式为 "pack: template %d: unsupported value for %s: %d"。
//
// Error implements the error interface with the message
// "pack: template %d: unsupported value for %s: %d".
func (e *ErrUnsupportedValue) Error() string {
	return fmt.Sprintf("pack: template %d: unsupported value for %s: %d", e.Template, e.Field, e.Value)
}

// SimplePacking 持有模板 5.0 的头部字段（第 5 段第 12-21 八位组）。E 与 D
// 以符号幅值形式存储（bit 15 = 符号，低 15 位 = 幅值），这是 GRIB2 生态约定——
// ecCodes 的 grib_set 把 D=-2 写成 0x8002，而非补码（WMO Note 56 就负值
// 引用 Regulation 92.1.5）。
//
// SimplePacking holds the template 5.0 header fields (section 5 octets
// 12-21). E and D are stored in sign-magnitude form (bit 15 = sign, low 15
// bits = magnitude), the GRIB2 ecosystem convention — eccodes grib_set
// writes D=-2 as 0x8002, not two's complement (WMO Note 56 cites Regulation
// 92.1.5 for negative values).
type SimplePacking struct {
	// R：参考值，IEEE 754 binary32，已按 10^D 预缩放 / reference value, IEEE 754 binary32, pre-scaled by 10^D
	R float32
	// E：二进制比例因子（符号幅值）/ binary scale factor (sign-magnitude)
	E int16
	// D：十进制比例因子（符号幅值）/ decimal scale factor (sign-magnitude)
	D int16
	// Bits：每值位数；第 7 段是 numValues×Bits 的位流（MSB 在前），不是固定每值 4 字节 / bits per value; section 7 is a numValues×Bits bitstream (MSB first), not fixed 4 bytes/value
	Bits uint8
	// OrigType：原始场值类型（表 5.1：0=浮点，1=整数）/ type of original field values (table 5.1: 0=float, 1=int)
	OrigType uint8
}

// simpleDecode 实现 GRIB2 模板 5.0（网格点数据 - 简单打包）。公式与位流布局
// 以 ecCodes 参考实现为基准校准（ecmwf/eccodes：DataSimplePacking::unpack
// 加 grib_bits_any_endian_simple.h 中的 grib_decode_array；与 ecCodes
// 2.49.0 编码消息的逐值二分比对见 xcheck_test.go）：
//
//	value = (raw × 2^E + R) × 10^(−D)，E/D 带符号
//	raw = 第 7 段接下来的 Bits 位，按 MSB 在前读为无符号整数
//
// 注意参考值 R 已按 10^D 预缩放（例如 D=2 时参考值 273.15 K 存为 27315.0）。
//
// 生效规则：
//   - 第 7 段数据长度必须等于 ceil(numValues×Bits/8) 字节（bits > 0），
//     或 0 字节（bits == 0，常数场）。输出恰好 numValues 项。（第 7 段只按
//     扫描顺序存放位图有效的点；位图的应用由调用方负责。）
//   - raw == 0xFFFFFFFF 按普通值解码：简单打包下 ecCodes 不设置全 1 的
//     NaN 哨兵。
//   - origType 不在 {0,1} 内即拒绝（表 5.1 目前只定义这两个值）；它不影响
//     上面的公式。
//
// 性能：曾几何时逐值读位流是解码 CPU 的大头（pprof：BitReader.ReadU 中
// 逐位循环约占解码时间 75%）。现在 Bits ∈ {8,16,32} 走字节对齐快路径，
// 其余位宽走 64 位滑动窗口解码器；公式保持参考实现的精确运算次序，
// 结果仍与比对基准逐位一致。
//
// simpleDecode implements GRIB2 template 5.0 (grid point data - simple
// packing). The formula and bitstream layout are calibrated against the
// eccodes reference implementation (ecmwf/eccodes: DataSimplePacking::unpack
// plus grib_decode_array in grib_bits_any_endian_simple.h; binary
// differential cross-checks against eccodes 2.49.0-encoded messages are in
// xcheck_test.go):
//
//	value = (raw × 2^E + R) × 10^(−D), with E/D signed
//	raw = the next Bits bits of section 7, read MSB first as an unsigned integer
//
// Note the reference value R is stored pre-scaled by 10^D (e.g. a reference
// of 273.15 K with D=2 is stored as 27315.0).
//
// Rules in force:
//   - The section 7 data length must equal ceil(numValues×Bits/8) bytes
//     (bits > 0), or 0 bytes (bits == 0, constant field). Output has exactly
//     numValues items. (Section 7 only holds bitmap-valid points in scan
//     order; bitmap placement is the caller's job.)
//   - raw == 0xFFFFFFFF decodes as an ordinary value: eccodes does not set an
//     all-ones NaN sentinel for simple packing.
//   - origType outside {0,1} is rejected (table 5.1 currently defines only
//     these two values); it does not affect the formula above.
//
// Performance: reading the bitstream per value used to dominate decode CPU
// (pprof: the per-bit loop in BitReader.ReadU was ~75% of decode time).
// Unpacking now takes a byte-aligned fast path for Bits ∈ {8,16,32} and a
// 64-bit sliding-window decoder otherwise; the formula keeps the exact
// operation order of the reference implementation, so results are still
// bit-for-bit identical with the cross-check baselines.
func simpleDecode(p SimplePacking, data []byte, numValues int) ([]float32, error) {
	want := (int(p.Bits)*numValues + 7) / 8
	if p.Bits == 0 {
		want = 0
	}
	if len(data) != want {
		return nil, fmt.Errorf("pack: template 5.0: data length %d != ceil(numValues×bits/8) = %d (numValues=%d, bits=%d)", len(data), want, numValues, p.Bits)
	}
	switch p.OrigType {
	case 0, 1:
	default:
		return nil, &ErrUnsupportedValue{Template: TemplateSimple, Field: "type of original field values", Value: int64(p.OrigType)}
	}
	if p.Bits > 32 {
		// WMO 表 5.5 允许 bitsPerValue ∈ [0,32]；更宽的位宽不是合法的简单
		// 打包流。无条件拒绝，包括 numValues == 0 的情形（此前的
		// `&& numValues > 0` 守卫会让 "bits=33 / numberOfValues=0" 的声明
		// 悄悄溜过去）。bits == 0 仍然合法（常数场 = R）。
		// WMO table 5.5 allows bitsPerValue ∈ [0,32]; wider is not a legal
		// simple-packing stream. Reject unconditionally, including
		// numValues == 0 (the earlier `&& numValues > 0` guard let a
		// "bits=33 / numberOfValues=0" declaration slip through silently).
		// bits == 0 remains legal (constant field = R).
		return nil, fmt.Errorf("pack: template 5.0: bitsPerValue %d out of range [0,32]", p.Bits)
	}

	twoE := math.Pow(2, float64(p.E))
	tenMinusD := math.Pow(10, -float64(p.D))
	fR := float64(p.R)
	eZero, dZero := p.E == 0, p.D == 0

	out := make([]float32, numValues)
	switch W := int(p.Bits); W {
	case 0:
		// 常数场：每个值都是 (0×2^E + R) × 10^(−D)。
		// Constant field: every value is (0×2^E + R) × 10^(−D).
		v := float32((0*twoE + fR) * tenMinusD)
		for i := range out {
			out[i] = v
		}
	case 8:
		for i := 0; i < numValues; i++ {
			raw := uint32(data[i])
			out[i] = scaleSimple(raw, twoE, fR, tenMinusD, eZero, dZero)
		}
	case 16:
		j := 0
		for i := 0; i < numValues; i++ {
			raw := uint32(data[j])<<8 | uint32(data[j+1])
			j += 2
			out[i] = scaleSimple(raw, twoE, fR, tenMinusD, eZero, dZero)
		}
	case 32:
		j := 0
		for i := 0; i < numValues; i++ {
			raw := uint32(data[j])<<24 | uint32(data[j+1])<<16 | uint32(data[j+2])<<8 | uint32(data[j+3])
			j += 4
			out[i] = scaleSimple(raw, twoE, fR, tenMinusD, eZero, dZero)
		}
	default:
		unpackWide(data, W, numValues, twoE, fR, tenMinusD, eZero, dZero, out)
	}
	return out, nil
}

// scaleSimple 按参考实现的精确运算次序套用模板 5.0 公式（与比对基准逐位
// 一致）：(raw×2^E + R) × 10^(−D)，全程 float64 计算，只在最后舍入到
// float32。eZero/dZero 标志跳过乘以恰好 1.0 的运算——这是 IEEE 恒等式，
// 两种走法按位相同。
//
// scaleSimple applies the template 5.0 formula in the reference
// implementation's exact operation order (bit-for-bit against the cross-check
// baselines): (raw×2^E + R) × 10^(−D), computed in float64 throughout and
// rounded to float32 only at the end. The eZero/dZero flags skip the
// multiplication by exactly 1.0 — an IEEE identity, bitwise identical either
// way.
func scaleSimple(raw uint32, twoE, fR, tenMinusD float64, eZero, dZero bool) float32 {
	switch {
	case eZero && dZero:
		return float32(float64(raw) + fR)
	case eZero:
		return float32((float64(raw) + fR) * tenMinusD)
	case dZero:
		return float32(float64(raw)*twoE + fR)
	default:
		return float32((float64(raw)*twoE + fR) * tenMinusD)
	}
}

// unpackWide 用 64 位滑动窗口解码每值位数非 8 倍数的位流，取代逐位读取：
// 每个值只需一次移位 + 掩码，整字节按需流入窗口。
//
// 不变量：每次迭代开始时，尚未读取的流位占据 acc[0, nbits)，最老的位在
// nbits-1；nbits 以上的位是陈旧数据且永不读取，因为每次抽取都使用窗口
// [nbits-W, nbits)，而回填把整个窗口左移 8 位，使陈旧位留在新窗口之上
// （nbits 恒 < W+8 ≤ 40，所以有效位永不会溢出寄存器）。调用方保证至少有
// numValues×W 个流位，因此内层循环总能回填。
//
// unpackWide decodes a bitstream whose bits-per-value is not a multiple of 8
// with a 64-bit sliding window, replacing per-bit reads: each value costs one
// shift + mask, with whole bytes streamed into the window on demand.
//
// Invariant: at the start of every iteration, the stream bits not yet read
// occupy acc[0, nbits), the oldest bit at nbits-1; bits above nbits are
// stale and never read, because each extraction uses the window
// [nbits-W, nbits) and refilling shifts the whole window left by 8, keeping
// stale bits above the new window (nbits stays < W+8 ≤ 40, so no valid bits
// ever overflow the register). The caller guarantees at least numValues×W
// stream bits, so the inner loop can always refill.
func unpackWide(data []byte, W, numValues int, twoE, fR, tenMinusD float64, eZero, dZero bool, out []float32) {
	var acc uint64
	nbits := 0
	pos := 0
	mask := uint64(1)<<uint(W) - 1
	for i := 0; i < numValues; i++ {
		for nbits < W && pos < len(data) {
			acc = (acc << 8) | uint64(data[pos])
			pos++
			nbits += 8
		}
		raw := uint32((acc >> uint(nbits-W)) & mask)
		nbits -= W
		out[i] = scaleSimple(raw, twoE, fR, tenMinusD, eZero, dZero)
	}
}
