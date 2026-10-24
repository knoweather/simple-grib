package gribw

import (
	"bytes"
	"fmt"
	"image/png"
)

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
