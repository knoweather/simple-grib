// Package octetio 提供大端、带边界检查的字节切片读取，外加为 GRIB2 位图准备的
// MSB-first 比特访问。
//
// 所有读取方法在请求范围为负或超出缓冲区时返回错误而不是 panic（返回的
// *ErrRange 携带 Off/Size/Len 上下文）。Reader 与 Bits 引用其源切片；
// 使用期间不要修改底层数组。
package octetio

import (
	"encoding/binary"
	"fmt"
	"math"
)

// ErrRange 报告对 Reader 的一次越界读取尝试。
type ErrRange struct {
	Off  int // 请求的起始偏移，基于 0
	Size int // 请求的字节数
	Len  int // 缓冲区长度（字节）
}

func (e *ErrRange) Error() string {
	return fmt.Sprintf("octetio: read %d byte(s) at offset %d out of range (buffer len %d)", e.Size, e.Off, e.Len)
}

// Reader 以边界检查方式从字节切片读取大端整数。
type Reader struct {
	b []byte
}

// New 返回一个覆盖 b 的 Reader。切片是被引用而非拷贝。
func New(b []byte) *Reader { return &Reader{b: b} }

// Len 报告缓冲区中的字节数。
func (r *Reader) Len() int { return len(r.b) }

func (r *Reader) check(off, size int) error {
	if off < 0 || off+size > len(r.b) {
		return &ErrRange{Off: off, Size: size, Len: len(r.b)}
	}
	return nil
}

// U8 读取偏移 off 处的一个字节。
func (r *Reader) U8(off int) (uint8, error) {
	if err := r.check(off, 1); err != nil {
		return 0, err
	}
	return r.b[off], nil
}

// U16 读取偏移 off 处的大端 uint16。
func (r *Reader) U16(off int) (uint16, error) {
	if err := r.check(off, 2); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(r.b[off:]), nil
}

// U32 读取偏移 off 处的大端 uint32。
func (r *Reader) U32(off int) (uint32, error) {
	if err := r.check(off, 4); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(r.b[off:]), nil
}

// F32 读取偏移 off 处的大端 IEEE 754 float32。
func (r *Reader) F32(off int) (float32, error) {
	u, err := r.U32(off)
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(u), nil
}
