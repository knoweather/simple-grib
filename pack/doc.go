// Package pack 处理 GRIB2 第 5 段（数据表示模板）：数值模板解包为 float32
// 网格点值；local-use 的 5.49152 是 WebP 图像载荷，拒绝数值解码。
//
// 解码器按模板号注册在全局注册表中（Register/Unpack）。已端到端实现的数值模板：
// 5.0（简单打包）、5.2（复杂打包）、5.3（带空间差分的复杂打包）、5.4（IEEE
// 浮点）、5.40（JPEG 2000，针对无损 5/3 子集的纯 Go 解码器）、5.42
// （CCSDS 推荐的无损压缩，纯 Go 移植 libaec 自适应熵编码器）。5.49152 是
// local-use 的 **WebP 图像载荷**：第 7 段原样存放完整 WebP 文件，
// numberOfValues 记录图像像素数；它不是数值模板，Unpack 返回 ErrNotNumeric，
// 读写图像请走根包的 WebP / WebPInfo / WebPImage 与 gribw 的 SetWebP。
//
// 解码器通过 spec.Walk 读取模板字段、读取第 7 段的打包数据，并遵循
// fail-loud（响亮失败）策略：不受支持或自相矛盾的声明一律报错，
// 绝不猜测默认值。
//
// Package pack handles GRIB2 data-representation templates (section 5):
// numeric templates unpack into float32 grid-point values, while the local-use
// 5.49152 template is a WebP image payload that refuses numeric decoding.
//
// Decoders are registered by template number in a global registry
// (Register/Unpack). Implemented numeric templates: 5.0 (simple packing),
// 5.2 (complex packing), 5.3 (complex packing with spatial differencing),
// 5.4 (IEEE floating point), 5.40 (JPEG 2000, a pure-Go decoder for the
// lossless 5/3 subset) and 5.42 (CCSDS recommended lossless compression, a
// pure-Go port of the libaec adaptive entropy coder). 5.49152 is a local-use
// **WebP image payload**: section 7 stores one complete WebP file verbatim and
// numberOfValues records the pixel count. It is not a numeric template —
// Unpack returns ErrNotNumeric; use the root package's WebP / WebPInfo /
// WebPImage and gribw's SetWebP for image payloads.
//
// Decoders read the template fields through spec.Walk and the packed data
// of section 7, and follow a fail-loud policy: unsupported or inconsistent
// declarations produce an error, never a guessed default value.
package pack
