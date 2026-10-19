package gribw

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/knoweather/simple-grib/spec"
)

// SimpleParams 保存 GRIB2 显式数据表示模板 5.0（simple packing）的打包参数。
// R 是第 5 节 octet 12-15 中存储的参考值——已按 10^D 预缩放（例如参考值
// 273.15 K 在 D=2 时存为 27315.0），与读取端 SimplePacking.R 暴露的形式
// 完全一致，保证编码/解码往返对称。
//
// SimpleParams holds the GRIB2 explicit data representation template 5.0
// (simple packing) parameters. R is the reference value as stored in section 5
// octets 12-15 — already pre-scaled by 10^D (e.g. a reference of 273.15 K with
// D=2 is stored as 27315.0), which is exactly what the reader's
// SimplePacking.R exposes, so encode/decode round-trips stay symmetric.
type SimpleParams struct {
	R    float32 // 参考值（存储形式，已按 10^D 预缩放）/ reference value (stored form, pre-scaled by 10^D)
	E    int     // 二进制缩放因子；须落在符号-幅值 int16 [-32767,32767] 内（§5.12）/ binary scale factor; must fit a sign-magnitude int16 [-32767,32767] (§5.12)
	D    int     // 十进制缩放因子；取值范围同 E / decimal scale factor; same range as E
	Bits int     // 每个打包值的位数，[0,32]；0 = 常数场 / bits per packed value, [0,32]; 0 = constant field
}

// EncodeSimple 用 GRIB2 模板 5.0（simple packing）打包数据，把第 5 节头与
// 第 7 节数据作为两段独立切片返回——这种可检视的 API 形状让测试能对两部分
// 分别做黄金比对。它是读取端解码（pack/simple.go，已对照 eccodes 验证）
// 的逆运算：
//
//	decode: value = (raw×2^E + R) × 10^(−D)，raw 取接下来的 Bits 位，MSB 在前
//	encode: raw = round((v×10^D − R) × 2^(−E))
//
// 快速失败（fail-loud）规则：E/D 超出符号-幅值 int16 范围 [-32767,32767]
// （§5.12）、bits 超出 [0,32]、出现非有限值（simple packing 无法表示
// NaN/Inf——请改用 IEEE 模板 5.4），以及任何 raw 落在 [0, 2^Bits) 之外的
// 值。Bits=0（常数场）时每个输入必须精确等于解码常数 R×10^(−D)，payload
// 为空。原始场值类型（octet 21，表 5.1）固定写 0（float）。
//
// EncodeSimple packs values with GRIB2 template 5.0 simple packing and returns
// the section 5 header and the section 7 data as separate slices — an
// inspectable API shape that lets tests golden-compare both halves
// independently. It is the inverse of the reader's decode (pack/simple.go,
// verified against eccodes):
//
//	decode: value = (raw×2^E + R) × 10^(−D), raw = the next Bits bits, MSB first
//	encode: raw = round((v×10^D − R) × 2^(−E))
//
// Fail-loud rules: E/D outside the sign-magnitude int16 range
// [-32767,32767] (§5.12), bits outside [0,32], non-finite values (simple
// packing cannot represent NaN/Inf — use IEEE template 5.4) and any value whose
// raw falls outside [0, 2^Bits). With Bits=0 (constant field) every value must
// exactly equal the decoded constant R×10^(−D), and the payload is empty. The
// original field values type (octet 21, table 5.1) is written as 0 (float).
func EncodeSimple(values []float32, p SimpleParams) (sec5, payload []byte, err error) {
	if p.E < -32767 || p.E > 32767 {
		return nil, nil, fmt.Errorf("gribw: simple packing: E=%d out of sign-magnitude int16 range [-32767,32767] (§5.12 rev 2026-10-05)", p.E)
	}
	if p.D < -32767 || p.D > 32767 {
		return nil, nil, fmt.Errorf("gribw: simple packing: D=%d out of sign-magnitude int16 range [-32767,32767] (§5.12 rev 2026-10-05)", p.D)
	}
	if p.Bits < 0 || p.Bits > 32 {
		return nil, nil, fmt.Errorf("gribw: simple packing: bits=%d out of range [0,32]", p.Bits)
	}

	tenD := math.Pow(10, float64(p.D))
	twoMinusE := math.Pow(2, -float64(p.E))

	if p.Bits == 0 {
		// 常数场：解码端对每个值都产出 (0×2^E + R)×10^(−D)，
		// 所以所有输入必须精确等于该常数。
		// Constant field: the decoder yields (0×2^E + R)×10^(−D) for every
		// value, so all inputs must equal that constant exactly.
		constant := float32(float64(p.R) * math.Pow(10, -float64(p.D)))
		for i, v := range values {
			if v != constant {
				return nil, nil, fmt.Errorf("gribw: simple packing: bits=0 constant field: value[%d]=%v != reference constant %v (all values must equal R×10^(−D))", i, v, constant)
			}
		}
		payload = []byte{}
	} else {
		maxRaw := uint64(1) << uint(p.Bits)
		payload = make([]byte, (len(values)*p.Bits+7)/8)
		var cur uint32
		nbits, pos := 0, 0
		for i, v := range values {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return nil, nil, fmt.Errorf("gribw: simple packing: value[%d]=%v not finite (use IEEE template 5.4 for NaN/Inf)", i, v)
			}
			// 精确零捷径：v == R 时无论缩放因子取何值，raw 恒为 0。若改为
			// 计算 (v·10^D − R)×2^(−E)，一旦 |E| 或 |D| 超过约 1023（pow
			// 溢出）就会得到 0×Inf = NaN，导致整场被误判失败。
			// Exact-zero shortcut: when v == R the raw is exactly 0 for any
			// scale factor. Computing (v·10^D − R)×2^(−E) instead yields
			// 0×Inf = NaN once |E| or |D| exceeds ~1023 (pow overflows),
			// which would spuriously fail the field.
			var rawF float64
			if v == p.R {
				rawF = 0
			} else {
				rawF = (float64(v)*tenD - float64(p.R)) * twoMinusE
				if math.IsNaN(rawF) || math.IsInf(rawF, 0) {
					return nil, nil, fmt.Errorf("gribw: simple packing: value[%d]=%v overflows the packing arithmetic with E=%d D=%d", i, v, p.E, p.D)
				}
			}
			raw := int64(math.Round(rawF))
			if raw < 0 || uint64(raw) >= maxRaw {
				return nil, nil, fmt.Errorf("gribw: simple packing: value[%d]=%v needs raw=%d outside [0,%d) for bits=%d (adjust R/E/D)", i, v, raw, maxRaw, p.Bits)
			}
			for b := p.Bits - 1; b >= 0; b-- {
				cur = cur<<1 | uint32((raw>>uint(b))&1)
				nbits++
				if nbits == 8 {
					payload[pos] = byte(cur)
					pos++
					cur, nbits = 0, 0
				}
			}
		}
		if nbits > 0 {
			// 尾字节：不足 8 位的最后一组占据高位（MSB 在前的比特流布局），
			// 经左移后低位 (8−nbits) 位为零填充。若不左移直接写 byte(cur)，
			// 部分组会落到低位——只要总比特数不是 8 的倍数，eccodes/wgrib2/
			// 读取端解出的最后一个值都会被破坏。
			// Tail byte: the incomplete final group occupies the HIGH bits
			// (MSB-first bitstream layout), left-shifted so the low
			// (8−nbits) bits are zero padding. Writing byte(cur) unshifted
			// would put the partial group in the low bits, corrupting the
			// last value for eccodes/wgrib2/the reader whenever the total bit
			// count is not a multiple of 8.
			payload[pos] = byte(cur << uint(8-nbits))
		}
	}

	def, ok := spec.Template(5, 0)
	if !ok {
		return nil, nil, fmt.Errorf("gribw: simple packing: no spec definition for template 5.0")
	}
	sec5 = make([]byte, 21) // 模板 5.0 最小长度（最后字段止于 octet 21）/ template 5.0 minimum length (last field ends at octet 21)
	binary.BigEndian.PutUint32(sec5[0:4], 21)
	sec5[4] = 5
	binary.BigEndian.PutUint32(sec5[5:9], uint32(len(values))) // 值个数 / octets 6-9: numberOfValues
	binary.BigEndian.PutUint16(sec5[9:11], 0)                  // 模板号 0 / octets 10-11: template number 0
	if err := setF32(def, sec5, 12, p.R); err != nil {         // 参考值 R / octets 12-15: reference value R
		return nil, nil, err
	}
	if err := setU16(def, sec5, 16, smu16(int32(p.E))); err != nil { // octets 16-17，符号-幅值编码 (§5.12) / sign-magnitude (§5.12)
		return nil, nil, err
	}
	if err := setU16(def, sec5, 18, smu16(int32(p.D))); err != nil { // octets 18-19，符号-幅值编码 (§5.12) / sign-magnitude (§5.12)
		return nil, nil, err
	}
	if err := setU8(def, sec5, 20, byte(p.Bits)); err != nil { // 每值位数 / octet 20: bits per value
		return nil, nil, err
	}
	if err := setU8(def, sec5, 21, 0); err != nil { // octet 21：原始场值类型（表 5.1）为 float / original field values type (table 5.1): float
		return nil, nil, err
	}
	return sec5, payload, nil
}
