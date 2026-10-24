// WebP 图像载荷（local-use 模板 5.49152）。
//
// 这个模板不是数值编码器：第 7 段原样存放一整份 WebP 文件（RIFF 容器），
// 库既不解析像素，也不把数值重编码成图像。它把 GRIB 当作带元数据与网格的
// 图像容器：
//
//	octets 1-4    第 5 段长度 = 11
//	octet  5      section number = 5
//	octets 6-9    numberOfValues = 图像宽 × 高（像素数，不是网格点数）
//	octets 10-11  49152
//
// 第 5 段没有 R/E/D/bits 等打包字段。读写入口在根包（Message.WebP /
// WebPInfo / WebPImage）与 gribw.Message.SetWebP；数值解码 Message.Values
// 对这个模板返回 ErrNotNumeric —— 图像像素不是 GRIB 数值。
//
// The WebP image payload (local-use template 5.49152).
//
// This template is not a numeric codec: section 7 stores one complete WebP file
// (RIFF container) verbatim. The library neither interprets pixels nor
// re-encodes values into an image; GRIB is used as an image container that
// carries metadata and a grid:
//
//	octets 1-4    section 5 length = 11
//	octet  5      section number = 5
//	octets 6-9    numberOfValues = image width × height (pixels, not grid points)
//	octets 10-11  49152
//
// Section 5 has no R/E/D/bits packing fields. The entry points live in the root
// package (Message.WebP / WebPInfo / WebPImage) and in
// gribw.Message.SetWebP; numeric decoding (Message.Values) reports
// ErrNotNumeric for this template — image pixels are not GRIB values.

package pack

import (
	"fmt"

	"github.com/knoweather/simple-grib/spec"
)

func init() {
	if err := spec.RegisterLocalTable(5, TemplateWebP, webpTemplateDef()); err != nil {
		panic("pack: registering local-use template 5.49152: " + err.Error())
	}
	Register(TemplateWebP, webpImageUnpacker{})
}

// webpTemplateDef 定义 5.49152 的第 5 段布局：只有固定头部，没有打包字段。
//
// webpTemplateDef defines the section 5 layout of 5.49152: fixed header only,
// no packing fields.
func webpTemplateDef() *spec.TemplateDef {
	return &spec.TemplateDef{
		ID:     "5.49152",
		Name:   "WebP image (local use)",
		Status: "local-use",
	}
}

// ErrNotNumeric 报告某个数据表示模板的载荷不是数值（目前只有 local-use
// WebP 图像模板 5.49152）。图像内容请走 Message.WebP / WebPInfo / WebPImage。
//
// ErrNotNumeric reports a data-representation template whose payload is not
// numeric (currently only the local-use WebP image template 5.49152). Read the
// image through Message.WebP / WebPInfo / WebPImage instead.
type ErrNotNumeric struct {
	// Template：数据表示模板号 / data-representation template number
	Template int
	// Format：载荷类型的人类可读名称 / human-readable payload kind
	Format string
}

// Error 实现 error 接口。
//
// Error implements the error interface.
func (e *ErrNotNumeric) Error() string {
	return fmt.Sprintf("pack: template %d carries a %s payload, not numeric values; use Message.WebP or Message.WebPImage", e.Template, e.Format)
}

// webpImageUnpacker 明确拒绝把 5.49152 当作数值模板解码。
//
// webpImageUnpacker explicitly refuses to decode 5.49152 as a numeric
// template.
type webpImageUnpacker struct{}

func (webpImageUnpacker) Name() string { return "WebP image (5.49152)" }

func (webpImageUnpacker) Unpack(ctx *Context) ([]float32, error) {
	return nil, &ErrNotNumeric{Template: ctx.TemplateNumber, Format: "WebP image"}
}
