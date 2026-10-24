package grib

import (
	"bytes"
	"fmt"
	"image"

	simplewebp "github.com/knoweather/simple-webp"

	"github.com/knoweather/simple-grib/pack"
)

// WebPInfo 描述 local-use 模板 5.49152 消息携带的 WebP 图像。它只读容器头，
// 不解码像素。
//
// WebPInfo describes the WebP image carried by a local-use template 5.49152
// message. It reads the container header only and decodes no pixels.
type WebPInfo struct {
	// Width / Height：画布像素尺寸 / canvas size in pixels
	Width, Height int
	// Lossless：true = VP8L 无损码流，false = VP8 有损 / true for a VP8L lossless bitstream, false for lossy VP8
	Lossless bool
	// Animated：true = 动画 WebP（画布尺寸来自容器） / true for an animated WebP (the canvas size comes from the container)
	Animated bool
}

// WebP 原样返回消息携带的 WebP 文件字节（第 7 段第 6 八位组起）。不做任何
// 解析或重编码，因此写回的内容与写入时逐字节相同；返回的是副本，调用方可以
// 安全保留或修改。
//
// WebP 只适用于数据表示模板 5.49152 的消息；其它模板返回错误。
//
// WebP returns the WebP file bytes carried by the message (section 7, from
// octet 6) without parsing or re-encoding, so the bytes are identical to what
// was written. The returned slice is a copy the caller may retain or modify.
//
// WebP applies only to messages with data-representation template 5.49152;
// other templates produce an error.
func (m *Message) WebP() ([]byte, error) {
	raw, err := m.webpPayload()
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), raw...), nil
}

// WebPInfo 读取消息的 WebP 容器头：宽、高、是否有损、是否动画。它不解码
// 像素，因此对大图也很便宜；载荷不是合法 WebP 时返回解析错误。
//
// WebPInfo reads the message's WebP container header: width, height, lossless
// or lossy, animated or still. It decodes no pixels, so it stays cheap for
// large images; an invalid payload produces a parse error.
func (m *Message) WebPInfo() (WebPInfo, error) {
	raw, err := m.webpPayload()
	if err != nil {
		return WebPInfo{}, err
	}
	w, err := simplewebp.ParseWebp(bytes.NewReader(raw))
	if err != nil {
		return WebPInfo{}, fmt.Errorf("grib: WebP header: %w", err)
	}
	return WebPInfo{
		Width:    w.Width,
		Height:   w.Height,
		Lossless: w.VP8L != nil,
		Animated: w.HasAnimation,
	}, nil
}

// WebPImage 用 simple-webp 解码消息携带的 WebP，返回 image.Image。有损与无损
// 码流都接受；动画文件返回合成后的第一帧（simple-webp 的行为），需要逐帧时
// 请直接用 WebP 取原始字节后交给 simple-webp 的动画 API。
//
// WebPImage decodes the message's WebP with simple-webp and returns an
// image.Image. Both lossy and lossless bitstreams are accepted; for an
// animated file the composited first frame is returned (simple-webp's
// behaviour) — use WebP to get the raw bytes and drive simple-webp's animation
// API when every frame matters.
func (m *Message) WebPImage() (image.Image, error) {
	raw, err := m.webpPayload()
	if err != nil {
		return nil, err
	}
	img, err := simplewebp.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("grib: decoding WebP image: %w", err)
	}
	return img, nil
}

// webpPayload 返回第 7 段载荷（不复制），并校验消息确实是 WebP 模板。
//
// webpPayload returns the section 7 payload (no copy) after checking that the
// message really uses the WebP template.
func (m *Message) webpPayload() ([]byte, error) {
	if m.g1 != nil {
		return nil, fmt.Errorf("grib: data template %d is GRIB2-only (message is GRIB edition 1)", pack.TemplateWebP)
	}
	dr, err := m.DataRep()
	if err != nil {
		return nil, err
	}
	if dr.TemplateNumber != pack.TemplateWebP {
		return nil, fmt.Errorf("grib: message data template is %d, not a WebP image (%d)", dr.TemplateNumber, pack.TemplateWebP)
	}
	sec7, err := m.Section(7)
	if err != nil {
		return nil, err
	}
	if len(sec7.Data) <= 5 {
		return nil, fmt.Errorf("grib: WebP message has an empty section 7 payload")
	}
	return sec7.Data[5:], nil
}
