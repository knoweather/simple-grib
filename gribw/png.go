package gribw

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image/png"
	"math"
)

// SetPNG encodes values as template 5.41 (PNG) and installs both sections.
// The image is one row of grayscale samples (width = len(values), height 1),
// quantised with the same R/E/D/bits rules as EncodeSimple. PNG bit depths are
// 1, 2, 4, 8 and 16, so other bit widths are rejected.
//
// SetPNG 用模板 5.41（PNG）编码 values 并写入两段：图像为单行灰度样本
// （宽 = len(values)，高 1），量化规则与 EncodeSimple 相同；PNG 位深只能是
// 1/2/4/8/16，其它位宽拒绝。
func (m *Message) SetPNG(values []float32, p SimpleParams) error {
	sec5, payload, err := EncodePNG(values, p)
	if err != nil {
		return err
	}
	return m.SetDataSection(sec5, payload)
}

// EncodePNG packs values with template 5.41 (PNG) and returns the section 5
// header plus the PNG file as section 7 data. It mirrors EncodeSimple's
// quantisation (value = (raw*2^E + R) * 10^-D) and its fail-loud rules.
//
// EncodePNG 用模板 5.41（PNG）打包，返回 section 5 头与作为 section 7 的 PNG
// 文件；量化公式与失败规则与 EncodeSimple 一致。
func EncodePNG(values []float32, p SimpleParams) (sec5, payload []byte, err error) {
	if p.Bits != 1 && p.Bits != 2 && p.Bits != 4 && p.Bits != 8 && p.Bits != 16 {
		return nil, nil, fmt.Errorf("gribw: PNG packing: bits=%d unsupported (PNG bit depths are 1, 2, 4, 8, 16)", p.Bits)
	}
	if len(values) == 0 {
		return nil, nil, fmt.Errorf("gribw: PNG packing: no values")
	}
	if p.E < -32767 || p.E > 32767 {
		return nil, nil, fmt.Errorf("gribw: PNG packing: E=%d out of sign-magnitude int16 range [-32767,32767]", p.E)
	}
	if p.D < -32767 || p.D > 32767 {
		return nil, nil, fmt.Errorf("gribw: PNG packing: D=%d out of sign-magnitude int16 range [-32767,32767]", p.D)
	}

	tenD := math.Pow(10, float64(p.D))
	twoMinusE := math.Pow(2, -float64(p.E))
	maxRaw := uint64(1) << uint(p.Bits)
	samples := make([]uint32, len(values))
	for i, v := range values {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, nil, fmt.Errorf("gribw: PNG packing: value[%d]=%v not finite", i, v)
		}
		var rawF float64
		if v == p.R {
			rawF = 0
		} else {
			rawF = (float64(v)*tenD - float64(p.R)) * twoMinusE
			if math.IsNaN(rawF) || math.IsInf(rawF, 0) {
				return nil, nil, fmt.Errorf("gribw: PNG packing: value[%d]=%v overflows the packing arithmetic with E=%d D=%d", i, v, p.E, p.D)
			}
		}
		raw := int64(math.Round(rawF))
		if raw < 0 || uint64(raw) >= maxRaw {
			return nil, nil, fmt.Errorf("gribw: PNG packing: value[%d]=%v needs raw=%d outside [0,%d) for bits=%d (adjust R/E/D)", i, v, raw, maxRaw, p.Bits)
		}
		samples[i] = uint32(raw)
	}

	payload, err = encodeGrayPNG(len(values), 1, p.Bits, samples)
	if err != nil {
		return nil, nil, err
	}

	sec5 = make([]byte, 21) // template 5.41 has the same fixed header as 5.0
	binary.BigEndian.PutUint32(sec5[0:4], uint32(len(sec5)))
	sec5[4] = 5
	binary.BigEndian.PutUint32(sec5[5:9], uint32(len(values)))
	binary.BigEndian.PutUint16(sec5[9:11], uint16(41))
	binary.BigEndian.PutUint32(sec5[11:15], math.Float32bits(p.R))
	binary.BigEndian.PutUint16(sec5[15:17], smu16(int32(p.E)))
	binary.BigEndian.PutUint16(sec5[17:19], smu16(int32(p.D)))
	sec5[19] = byte(p.Bits)
	sec5[20] = 0 // original field values: float
	return sec5, payload, nil
}

// encodeGrayPNG writes a grayscale PNG at the requested bit depth. The caller
// supplies exactly width*height samples in row-major order; rows are padded to
// a whole byte as PNG requires.
//
// encodeGrayPNG 按指定位深写灰度 PNG；调用方按行优先提供 width*height 个
// 样本，行尾按 PNG 规则补齐到整字节。
func encodeGrayPNG(width, height, depth int, samples []uint32) ([]byte, error) {
	if width < 1 || height < 1 {
		return nil, fmt.Errorf("gribw: PNG: invalid canvas %dx%d", width, height)
	}
	if len(samples) != width*height {
		return nil, fmt.Errorf("gribw: PNG: %d samples for a %dx%d canvas", len(samples), width, height)
	}
	if width > math.MaxInt32 {
		return nil, fmt.Errorf("gribw: PNG: width %d exceeds the format limit", width)
	}
	rowBytes := (width*depth + 7) / 8
	raw := make([]byte, 0, height*(rowBytes+1))
	row := make([]byte, rowBytes)
	for y := 0; y < height; y++ {
		for i := range row {
			row[i] = 0
		}
		base := y * width
		bit := 0
		for x := 0; x < width; x++ {
			s := samples[base+x]
			for b := depth - 1; b >= 0; b-- {
				if s>>uint(b)&1 != 0 {
					row[bit/8] |= 1 << uint(7-bit%8)
				}
				bit++
			}
		}
		raw = append(raw, 0) // filter type: none
		raw = append(raw, row...)
	}

	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write(raw); err != nil {
		return nil, fmt.Errorf("gribw: PNG: zlib: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("gribw: PNG: zlib: %w", err)
	}

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8] = byte(depth)
	ihdr[9] = 0 // grayscale

	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, pngChunk("IHDR", ihdr)...)
	out = append(out, pngChunk("IDAT", z.Bytes())...)
	out = append(out, pngChunk("IEND", nil)...)
	return out, nil
}

// pngChunk frames one PNG chunk (length, type, data, CRC).
//
// pngChunk 组装一个 PNG chunk（长度、类型、数据、CRC）。
func pngChunk(typ string, data []byte) []byte {
	out := make([]byte, 0, 12+len(data))
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	out = append(out, length[:]...)
	out = append(out, typ...)
	out = append(out, data...)
	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(data)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc.Sum32())
	return append(out, sum[:]...)
}

// checkPNGPayload validates section 7 for template 5.41: a complete PNG whose
// canvas holds at least numberOfValues grayscale samples. The reader enforces
// the rest (grayscale, bit depth equal to bitsPerValue, sample extraction).
//
// checkPNGPayload 校验模板 5.41 的第 7 段：一份完整 PNG，画布至少能容纳
// numberOfValues 个灰度样本；灰度/位深/取样由读取端继续校验。
func checkPNGPayload(numValues int, payload []byte) error {
	cfg, err := png.DecodeConfig(bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("gribw: section 7 PNG payload: %w", err)
	}
	if cfg.Width < 1 || cfg.Height < 1 {
		return fmt.Errorf("gribw: section 7 PNG canvas is empty (%dx%d)", cfg.Width, cfg.Height)
	}
	if pixels := int64(cfg.Width) * int64(cfg.Height); pixels < int64(numValues) {
		return fmt.Errorf("gribw: section 7 PNG canvas %dx%d holds %d samples, numberOfValues is %d",
			cfg.Width, cfg.Height, pixels, numValues)
	}
	return nil
}
