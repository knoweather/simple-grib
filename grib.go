// Package grib 提供纯 Go 实现的 WMO GRIB 第 2 版（GRIB2）消息读取器。
//
// 它可以读取包含多条首尾相接消息的文件，校验节结构（第 0-7 节，以及
// 以 "7777" 结尾的第 8 节），并以类型化方式访问标识段、网格定义段、产品
// 定义段、数据表示段与解码后的要素值。模板布局来自 spec 包（由 WMO 官方
// CSV 文件生成）；网格坐标计算在 grid 包，数值解包在 pack 包。
//
// 该读取器遵循"失败即报错"（fail-loud）原则：任何结构性违规必定返回错误，
// 绝不猜测绕过。未知模板或本地使用模板会降级为仅元数据访问
// （Section.Data / Raw 视图），不会阻断消息其余部分的读取。
//
// Package grib is a pure-Go reader for WMO GRIB edition 2 (GRIB2) messages.
//
// It reads files containing multiple concatenated messages, validates the
// section structure (sections 0-7 plus the "7777" end section, section 8),
// and provides typed access to the identification, grid definition, product
// definition, data representation, and decoded values. Template layouts come
// from the spec package (generated from the official WMO CSV files); grid
// coordinate math lives in grid, value unpacking in pack.
//
// The reader is fail-loud: structural violations always return an error and
// are never guessed around. Unknown or local-use templates degrade to
// metadata-only access (Section.Data / Raw views) without blocking the read
// of the rest of the message.
package grib

import (
	"errors"
	"fmt"
)

// Sentinel errors for file-level and section-level problems. Classify with
// errors.Is; retrieve the offending number with errors.As via
// *ErrSectionAbsent or *ErrUnknownTemplate.
//
// 文件级与节级问题的哨兵错误。用 errors.Is 分类；用 errors.As 配合
// *ErrSectionAbsent 或 *ErrUnknownTemplate 取出出错的编号。
var (
	// ErrNotGRIB2 reports a bad 'GRIB' magic — the file is not GRIB at all.
	// (The name is kept for backward compatibility; it is now used only for
	// magic mismatches.)
	//
	// ErrNotGRIB2 报告 'GRIB' 魔数不匹配——该文件根本不是 GRIB。
	// （名称保留以兼容旧代码；现在仅用于魔数校验失败。）
	ErrNotGRIB2 = errors.New("grib: not a GRIB message")
	// ErrUnsupportedEdition reports the right magic but an edition that is
	// neither 1 nor 2. This package supports GRIB edition 1 (FM 92-IX) and
	// edition 2 (FM 92-XIII); editions 0 and 3+ are rejected here.
	//
	// ErrUnsupportedEdition 报告魔数正确、但版本号既不是 1 也不是 2。本包
	// 支持 GRIB 第 1 版（FM 92-IX）与第 2 版（FM 92-XIII）；版本 0 及 3 以上
	// 在此被拒绝。
	ErrUnsupportedEdition = errors.New("grib: unsupported GRIB edition")
	// ErrTruncated reports a file that ends inside a message or a section.
	//
	// ErrTruncated 报告文件在某条消息或某个节的中途结束。
	ErrTruncated = errors.New("grib: truncated")
	// ErrBadSectionLength reports a section length field that disagrees with
	// the actual bytes (too small, or past the end of the message).
	//
	// ErrBadSectionLength 报告节长度字段与实际字节数不符（过小，或越过消息
	// 末尾）。
	ErrBadSectionLength = errors.New("grib: bad section length")
)

// ErrSectionAbsent reports a required section number missing from a message.
// ErrSectionAbsent 报告消息中缺少某个必需的节号。
type ErrSectionAbsent struct{ Number int }

func (e *ErrSectionAbsent) Error() string {
	return fmt.Sprintf("grib: section %d absent", e.Number)
}

// ErrUnknownTemplate reports a template number with neither a WMO definition
// nor a local registration (local-use range, or unassigned). Metadata-only
// access still works; registering a definition with spec.RegisterLocalTable
// enables Walk-based parsing and name display.
//
// ErrUnknownTemplate 报告某个模板号既无 WMO 定义也无本地注册（处于本地使用
// 区间或尚未分配）。此时仍可做仅元数据访问；用 spec.RegisterLocalTable 注册
// 定义后即可启用基于 Walk 的解析与名称显示。
type ErrUnknownTemplate struct {
	Section int
	Number  int
}

func (e *ErrUnknownTemplate) Error() string {
	return fmt.Sprintf("grib: unknown section %d template %d (local use?; register a definition with spec.RegisterLocalTable)", e.Section, e.Number)
}

// Option customizes File behavior. Options are applied at open time and
// carried by the File into every message it parses.
//
// Option 定制 File 的行为。选项在打开文件时生效，并随 File 传递到其解析的
// 每一条消息。
type Option func(*fileConfig)

// fileConfig holds resolved option values.
// fileConfig 保存解析完成的选项值。
type fileConfig struct {
	lenientEndSection bool
}

// WithLenientEndSection relaxes the section 8 end-marker check: up to 4
// trailing bytes after section 7 are accepted whatever their content, instead
// of requiring the standard "7777". Use only for producers known to emit
// non-standard trailers; the default is strict mode.
//
// WithLenientEndSection 放宽第 8 节结束标记的校验：第 7 节之后最多 4 个尾部
// 字节无论内容如何都被接受，而不再要求标准的 "7777"。只应用于已知会输出
// 非标准尾部的生成器；默认保持严格模式。
func WithLenientEndSection() Option {
	return func(c *fileConfig) { c.lenientEndSection = true }
}

// parseOpts is the resolved-option view passed into per-message parsing.
// parseOpts 是传入逐条消息解析的选项视图。
func (c *fileConfig) parseOpts() parseOpts {
	return parseOpts{lenientEndSection: c.lenientEndSection}
}
