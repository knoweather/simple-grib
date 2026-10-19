package octetio

import (
	"fmt"
	"math/bits"
)

// Bits 提供对“按字节存储的比特串”的 MSB-first 访问，GRIB2 section 6 位图即如此：
// 第 i 位是字节 i/8 的第 (7 - i%8) 位。
type Bits struct {
	b []byte
}

// NewBits 返回一个覆盖 b 的 Bits。切片是被引用而非拷贝。
func NewBits(b []byte) *Bits { return &Bits{b: b} }

// N 报告可寻址的总比特数（每字节 8 位）。
func (b *Bits) N() int { return len(b.b) * 8 }

// Bit 报告第 i 位（基于 0，MSB-first）是否被置位。越界的 i 返回 false；
// 调用方须先针对 N() 做校验以便区分“位为 0”和“位不存在”。
func (b *Bits) Bit(i int) bool {
	if i < 0 || i >= len(b.b)*8 {
		return false
	}
	return b.b[i/8]>>(7-i%8)&1 == 1
}

// Popcount 统计前 n 位（MSB-first）中置位的位数 —— GRIB2 section 6 用它
// 统计位图里指示“数据存在”的点数（置 1 位）。n 被钳制到 [0, N]；
// 需要精确语义的调用方须自行针对 N() 校验。
func (b *Bits) Popcount(n int) int {
	if n < 0 {
		return 0
	}
	if n > len(b.b)*8 {
		n = len(b.b) * 8
	}
	full, rem := n/8, n%8
	c := 0
	for i := 0; i < full; i++ {
		c += bits.OnesCount8(b.b[i])
	}
	if rem != 0 {
		c += bits.OnesCount8(b.b[full] >> uint(8-rem))
	}
	return c
}

// BitReader 从比特串里以 MSB-first 方式逐位读出无符号值，GRIB2 section 7 的
// 简单打包（任意 bitsPerValue）即采用此方式（WMO Data Template 7.0：“二进制串，
// 每个（缩放后的）数据值用 [bitsPerValue] 位编码”）。该读取器基于游标、单向推进。
type BitReader struct {
	b   []byte
	pos int // 下一个待读比特的索引（基于 0，MSB-first）
}

// NewBitReader 返回一个定位在第 0 位的 BitReader（覆盖 b）。切片是被引用而非拷贝。
func NewBitReader(b []byte) *BitReader { return &BitReader{b: b} }

// NewBitReaderAt 返回一个定位在 start 位的 BitReader（例如定位在某个 section 7
// 载荷内部、按字节对齐的块起点）。
func NewBitReaderAt(b []byte, start int) *BitReader { return &BitReader{b: b, pos: start} }

// Skip 让游标前移 n 位而不读取。
func (r *BitReader) Skip(n int) { r.pos += n }

// Remaining 报告剩余可读取的比特数。
func (r *BitReader) Remaining() int { return len(r.b)*8 - r.pos }

// ReadU 以 MSB-first 方式读取 n 位（0 <= n <= 32），作为无符号整数返回
// （值的 bit 0 = 读到的第一个比特）。若 n 越界或剩余比特不足 n 个则报错。
func (r *BitReader) ReadU(n int) (uint32, error) {
	if n < 0 || n > 32 {
		return 0, fmt.Errorf("octetio: BitReader.ReadU: bit width %d out of range [0,32]", n)
	}
	if r.pos+n > len(r.b)*8 {
		return 0, fmt.Errorf("octetio: BitReader.ReadU: need %d bits, only %d remain", n, r.Remaining())
	}
	var v uint32
	for i := 0; i < n; i++ {
		bit := r.b[r.pos/8] >> uint(7-r.pos%8) & 1
		v = v<<1 | uint32(bit)
		r.pos++
	}
	return v, nil
}
