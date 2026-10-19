// Package pack 将 GRIB2 第 5 段（数据表示模板）解包为 float32 网格点值。
//
// 解码器按模板号注册在全局注册表中（Register/Unpack）。已端到端实现的模板：
// 5.0（简单打包）、5.2（复杂打包）、5.3（带空间差分的复杂打包）、5.4（IEEE
// 浮点）、5.40（JPEG 2000，针对无损 5/3 子集的纯 Go 解码器）以及 5.42
// （CCSDS 推荐的无损压缩，纯 Go 移植 libaec 自适应熵编码器）。模板 49152
// （WebP 无损，本地扩展）仅被识别，需要外部编解码器，因此在通过 Register
// 安装之前返回 ErrDecoderNotConfigured。
//
// 解码器通过 spec.Walk 读取模板字段、读取第 7 段的打包数据，并遵循
// fail-loud（响亮失败）策略：不受支持或自相矛盾的声明一律报错，
// 绝不猜测默认值。
//
// Package pack unpacks GRIB2 data-representation templates (section 5) into
// float32 grid-point values.
//
// Decoders are registered by template number in a global registry
// (Register/Unpack). Implemented end to end: template 5.0 (simple packing),
// 5.2 (complex packing), 5.3 (complex packing with spatial differencing),
// 5.4 (IEEE floating point), 5.40 (JPEG 2000, a pure-Go decoder for the
// lossless 5/3 subset), and 5.42 (CCSDS recommended lossless compression,
// a pure-Go port of the libaec adaptive entropy coder). Template 49152
// (WebP lossless, local use) is recognized but needs an external codec, so
// it returns ErrDecoderNotConfigured until one is installed via Register.
//
// Decoders read the template fields through spec.Walk and the packed data
// of section 7, and follow a fail-loud policy: unsupported or inconsistent
// declarations produce an error, never a guessed default value.
package pack
