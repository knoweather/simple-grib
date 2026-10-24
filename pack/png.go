package pack

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
)

// Template 5.41（PNG）解码器。
//
// 第 5 段字段与 5.0 相同（R/E/D/bits/type，octets 12-21）；第 7 段是一份完整
// 的 PNG 文件。WMO 5.41 要求 PNG 为灰度图、位深等于 bitsPerValue，样本按扫描
// 顺序排列。解码后把样本重新按 MSB 在前打成简单打包位流，再走与 5.0 完全相同的
// 公式 value = (raw×2^E + R) × 10^(−D)。
//
// 位深 1/2/4：Go 的 image/png 会把样本展开成 8 位（×255/(2^d−1)），这里做精确
// 逆变换还原原始样本；位深 8/16 直接取 Gray/Gray16 的字节。彩色、带透明、
// 调色板等非灰度 PNG 一律 fail-loud（WMO 5.41 的编码要求是灰度）。
//
// The template 5.41 (PNG) decoder.
//
// Section 5 carries the same fields as 5.0 (R/E/D/bits/type, octets 12-21);
// section 7 is one complete PNG file. WMO 5.41 requires a grayscale PNG whose
// bit depth equals bitsPerValue, with samples in scan order. After decoding,
// samples are repacked MSB-first into a simple-packing bitstream and run through
// the exact 5.0 formula value = (raw×2^E + R) × 10^(−D).
//
// For bit depths 1/2/4 the standard library expands samples to 8 bits
// (×255/(2^d−1)); this decoder inverts that exactly. Depths 8/16 read
// Gray/Gray16 bytes directly. Colour, transparent and palette PNGs fail loud
// (WMO 5.41 mandates grayscale).

// pngMaxPixels bounds the decoded canvas so a hostile IHDR cannot allocate an
// arbitrary image before we compare it with numberOfValues.
const pngMaxPixels = 1 << 28

type pngUnpacker struct{}

func (pngUnpacker) Name() string { return "PNG (5.41)" }

func (pngUnpacker) Unpack(ctx *Context) ([]float32, error) {
	if ctx.Fields == nil || ctx.Fields.Len() < 5 {
		return nil, fmt.Errorf("pack: template %d: section 5 fields missing or truncated (walk len %d)", TemplatePNG, walkLen(ctx.Fields))
	}
	p := SimplePacking{
		R:        ctx.Fields.At(0).F,
		E:        int16(smInt16(uint16(ctx.Fields.At(1).U))),
		D:        int16(smInt16(uint16(ctx.Fields.At(2).U))),
		Bits:     uint8(ctx.Fields.At(3).U),
		OrigType: uint8(ctx.Fields.At(4).U),
	}
	if p.Bits > 16 {
		// PNG cannot express sample depths above 16 bits.
		return nil, &ErrUnsupportedValue{Template: TemplatePNG, Field: "bitsPerValue", Value: int64(p.Bits)}
	}
	switch p.OrigType {
	case 0, 1:
	default:
		return nil, &ErrUnsupportedValue{Template: TemplatePNG, Field: "type of original field values", Value: int64(p.OrigType)}
	}
	if p.Bits == 0 {
		if len(ctx.Data) != 0 {
			return nil, fmt.Errorf("pack: template %d: bitsPerValue 0 (constant field) must not carry a payload (%d bytes)", TemplatePNG, len(ctx.Data))
		}
		return simpleDecode(p, nil, ctx.NumValues)
	}
	if ctx.NumValues <= 0 {
		return nil, fmt.Errorf("pack: template %d: bitsPerValue %d with numberOfValues %d", TemplatePNG, p.Bits, ctx.NumValues)
	}

	width, height, depth, err := pngHeader(ctx.Data)
	if err != nil {
		return nil, fmt.Errorf("pack: template %d: %w", TemplatePNG, err)
	}
	if depth != int(p.Bits) {
		return nil, fmt.Errorf("pack: template %d: bitsPerValue %d != PNG bit depth %d", TemplatePNG, p.Bits, depth)
	}
	pixels := int64(width) * int64(height)
	if pixels < int64(ctx.NumValues) {
		return nil, fmt.Errorf("pack: template %d: PNG holds %dx%d = %d samples, the field needs %d", TemplatePNG, width, height, pixels, ctx.NumValues)
	}
	// Allow a modest amount of padding beyond the declared values, but never an
	// arbitrarily large canvas (IHDR is attacker-controlled).
	if pixels > pngMaxPixels || pixels > int64(ctx.NumValues)+(1<<20) {
		return nil, fmt.Errorf("pack: template %d: PNG canvas %dx%d is larger than the field needs (%d samples)", TemplatePNG, width, height, ctx.NumValues)
	}

	img, err := png.Decode(bytes.NewReader(ctx.Data))
	if err != nil {
		return nil, fmt.Errorf("pack: template %d: decoding PNG: %w", TemplatePNG, err)
	}
	samples, err := pngSamples(img, depth, ctx.NumValues)
	if err != nil {
		return nil, fmt.Errorf("pack: template %d: %w", TemplatePNG, err)
	}
	packed := packSamples(samples, int(p.Bits))
	return simpleDecode(p, packed, ctx.NumValues)
}

// pngHeader reads the PNG signature and IHDR without decoding pixels. It
// enforces the WMO 5.41 shape: grayscale, 1/2/4/8/16-bit, no interlace
// restrictions beyond what the standard library supports.
//
// pngHeader 读取 PNG 签名与 IHDR（不解码像素），并校验 WMO 5.41 要求的
// 灰度、位深 1/2/4/8/16、无压缩/滤波扩展。
func pngHeader(data []byte) (width, height, depth int, err error) {
	const signature = "\x89PNG\r\n\x1a\n"
	if len(data) < 33 {
		return 0, 0, 0, fmt.Errorf("PNG shorter than signature + IHDR (%d bytes)", len(data))
	}
	if string(data[:8]) != signature {
		return 0, 0, 0, fmt.Errorf("not a PNG file (bad signature)")
	}
	if binary.BigEndian.Uint32(data[8:12]) != 13 || string(data[12:16]) != "IHDR" {
		return 0, 0, 0, fmt.Errorf("PNG does not start with a 13-byte IHDR chunk")
	}
	width = int(binary.BigEndian.Uint32(data[16:20]))
	height = int(binary.BigEndian.Uint32(data[20:24]))
	depth = int(data[24])
	colorType := data[25]
	if width < 1 || height < 1 {
		return 0, 0, 0, fmt.Errorf("PNG canvas %dx%d is empty", width, height)
	}
	if colorType != 0 {
		return 0, 0, 0, fmt.Errorf("PNG color type %d; WMO 5.41 requires grayscale (0)", colorType)
	}
	if data[26] != 0 || data[27] != 0 {
		return 0, 0, 0, fmt.Errorf("PNG compression/filter method %d/%d; only 0/0 is defined", data[26], data[27])
	}
	if data[28] > 1 {
		return 0, 0, 0, fmt.Errorf("PNG interlace method %d; only 0 (none) and 1 (Adam7) are defined", data[28])
	}
	switch depth {
	case 1, 2, 4, 8, 16:
	default:
		return 0, 0, 0, fmt.Errorf("PNG bit depth %d; WMO 5.41 allows 1, 2, 4, 8 or 16", depth)
	}
	return width, height, depth, nil
}

// pngSamples extracts the first numValues grayscale samples in scan order.
//
// pngSamples 按扫描顺序取出前 numValues 个灰度样本。
func pngSamples(img image.Image, depth, numValues int) ([]uint32, error) {
	out := make([]uint32, 0, numValues)
	switch m := img.(type) {
	case *image.Gray:
		if depth != 1 && depth != 2 && depth != 4 && depth != 8 {
			return nil, fmt.Errorf("decoded PNG is 8-bit grayscale, want depth %d", depth)
		}
		// image/png expands sub-byte samples by 255/(2^d-1); invert exactly.
		scale := uint32(1)
		if depth < 8 {
			scale = 0xFF / ((1 << uint(depth)) - 1)
		}
		b := m.Rect
		for y := b.Min.Y; y < b.Max.Y && len(out) < numValues; y++ {
			row := m.Pix[(y-b.Min.Y)*m.Stride:]
			for x := 0; x < b.Dx() && len(out) < numValues; x++ {
				v := uint32(row[x])
				if v%scale != 0 {
					// The standard library scales sub-byte samples by exactly
					// 255/(2^d-1); anything else would mean a decoder change we
					// cannot invert, so refuse rather than round silently.
					return nil, fmt.Errorf("grayscale byte %d is not a multiple of the depth-%d scale %d", v, depth, scale)
				}
				out = append(out, v/scale)
			}
		}
	case *image.Gray16:
		if depth != 16 {
			return nil, fmt.Errorf("decoded PNG is 16-bit grayscale, want depth %d", depth)
		}
		b := m.Rect
		for y := b.Min.Y; y < b.Max.Y && len(out) < numValues; y++ {
			row := m.Pix[(y-b.Min.Y)*m.Stride:]
			for x := 0; x < b.Dx() && len(out) < numValues; x++ {
				out = append(out, uint32(binary.BigEndian.Uint16(row[x*2:])))
			}
		}
	default:
		return nil, fmt.Errorf("decoded PNG is %T; grayscale PNG required", img)
	}
	if len(out) < numValues {
		return nil, fmt.Errorf("PNG yielded %d samples, want %d", len(out), numValues)
	}
	return out, nil
}

// packSamples packs samples MSB-first, the layout section 7 uses for 5.0.
//
// packSamples 按 MSB 在前打包样本，与 5.0 第 7 段的位流一致。
func packSamples(samples []uint32, bits int) []byte {
	out := make([]byte, (len(samples)*bits+7)/8)
	var cur uint32
	n, pos := 0, 0
	for _, s := range samples {
		for b := bits - 1; b >= 0; b-- {
			cur = cur<<1 | (s>>uint(b))&1
			n++
			if n == 8 {
				out[pos] = byte(cur)
				pos++
				cur, n = 0, 0
			}
		}
	}
	if n > 0 {
		out[pos] = byte(cur << uint(8-n))
	}
	return out
}
