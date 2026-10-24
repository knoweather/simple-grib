package gribw

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"

	simplewebp "github.com/knoweather/simple-webp"

	"github.com/knoweather/simple-grib/pack"
)

// SetWebP 把一份现成的 WebP 文件原样设为消息载荷（local-use 模板 5.49152）。
//
// 库不解码像素、也不把数值重编码成图像：第 7 段就是传入的字节，读回时
// Message.WebP 返回完全相同的字节。第 5 段只有 11 字节固定头，
// numberOfValues = 图像宽 × 高（像素数）。
//
// 网格（section 3）与产品（section 4）仍必须设置；图像尺寸不必等于网格点数。
// 容器头无法解析时立即报错（fail-loud），而不是等到写出后的读取端。
//
// SetWebP stores an existing WebP file verbatim as the message payload (the
// local-use template 5.49152).
//
// The library neither decodes pixels nor re-encodes values into an image:
// section 7 is exactly the bytes passed in, and Message.WebP returns the same
// bytes. Section 5 is the 11-byte fixed header with
// numberOfValues = image width × height (pixels).
//
// The grid (section 3) and product (section 4) are still required; the image
// size does not have to match the grid point count. An unparseable container
// header fails loud here instead of at the reader.
func (m *Message) SetWebP(webp []byte) error {
	pixels, err := webpPixels(webp)
	if err != nil {
		return fmt.Errorf("gribw: SetWebP: %w", err)
	}
	sec5 := make([]byte, 11)
	binary.BigEndian.PutUint32(sec5[0:4], uint32(len(sec5)))
	sec5[4] = 5
	binary.BigEndian.PutUint32(sec5[5:9], uint32(pixels))
	binary.BigEndian.PutUint16(sec5[9:11], uint16(pack.TemplateWebP))
	return m.SetDataSection(sec5, webp)
}

// webpPixels 读 WebP 容器头并返回画布像素数（宽 × 高）。
//
// webpPixels reads the WebP container header and returns the canvas pixel
// count (width × height).
func webpPixels(webp []byte) (int64, error) {
	cfg, err := simplewebp.DecodeConfig(bytes.NewReader(webp))
	if err != nil {
		return 0, err
	}
	if cfg.Width < 1 || cfg.Height < 1 {
		return 0, fmt.Errorf("WebP image has no pixels (%dx%d)", cfg.Width, cfg.Height)
	}
	pixels := int64(cfg.Width) * int64(cfg.Height)
	if pixels > math.MaxUint32 {
		return 0, fmt.Errorf("WebP image %dx%d exceeds the numberOfValues field", cfg.Width, cfg.Height)
	}
	return pixels, nil
}

// checkWebPPayload 校验第 7 段是可解析的 WebP，且第 5 段 numberOfValues 等于
// 图像像素数。
//
// checkWebPPayload verifies that section 7 is a parseable WebP file and that
// section 5's numberOfValues equals the image pixel count.
func checkWebPPayload(numValues int, payload []byte) error {
	if len(payload) < 12 || string(payload[0:4]) != "RIFF" || string(payload[8:12]) != "WEBP" {
		return fmt.Errorf("gribw: section 7 WebP payload must start with the RIFF....WEBP header (%d bytes)", len(payload))
	}
	pixels, err := webpPixels(payload)
	if err != nil {
		return fmt.Errorf("gribw: section 7 WebP payload: %w", err)
	}
	if int64(numValues) != pixels {
		return fmt.Errorf("gribw: section 5 numberOfValues %d != WebP image pixels %d", numValues, pixels)
	}
	return nil
}
