package grib

import (
	"fmt"

	"github.com/knoweather/simple-grib/grib1"
)

// Message 表示一条已解析的 GRIB 消息：原始消息体加上校验过的节列表。它同时
// 承载第 1 版与第 2 版消息——用 Edition() 区分版本，用 GRIB1() 获取 GRIB1
// 原生视图（GRIB1 独有字段的入口）。
//
// Message 由 File.Message 构造；所有访问器都是只读的，且在构造完成后
// 消息不可变，可安全地跨 goroutine 共享。
//
// 跨版本的语义差异在各访问器处逐一说明。总原则：两个版本共有的概念返回共
// 有值；某个版本不存在的概念返回显式的"不适用"标记，绝不伪造一个看起来像
// 真实数据的默认值。
//
// Message is a parsed GRIB message: the raw message body plus a validated
// section list. It carries both edition 1 and edition 2 — tell them apart
// with Edition() and get the GRIB1-native view via GRIB1() (the entry point
// for GRIB1-only fields).
//
// Constructed via File.Message; all accessors are read-only and safe to share
// across goroutines (the message is immutable after construction).
//
// Cross-edition semantic differences are documented per accessor. The general
// rule: common concepts get the common value; a concept that does not exist
// in the edition gets an explicit "not applicable" marker, never a fabricated
// default that looks like real data.
type Message struct {
	index   int
	off     int64
	size    int
	edition int
	data    []byte // full message bytes (all sections, from section 0 octet 1) // 完整消息字节（所有节，自第 0 节第 1 八位组起）
	secs    []Section
	byNum   map[int]Section

	// g1 是第 1 版的原生解析结果；第 2 版消息为 nil。两个版本的结构差异
	// 足够大，与其把二者硬塞进一个结构体，不如各自保留自己的视图，由本
	// 类型提供统一外观。
	// g1 is the edition-1 native parse; nil for edition 2. The two editions
	// differ structurally enough that each keeps its own view instead of
	// squeezing both into one struct; this type provides a unified facade.
	g1 *grib1.Message
}

func newMessage(index int, off int64, data []byte) (*Message, error) {
	return newMessageOpts(index, off, data, len(data), parseOpts{})
}

// newMessageWithTotal 校验 data 是一条消息，且其第 0 节 totalLength 字段等于
// total。经典消息中 total == len(data)；在多要素信封视图中，拼装出的单要素
// 缓冲区比信封声明的长度短，此时 total 携带信封大小，Message.Size 返回它
// （始终与第 0 节的该字段保持一致）。
//
// newMessageWithTotal validates that data is a message whose section 0
// totalLength field equals total. For a classic message total == len(data);
// for a multi-field envelope view, the assembled single-field buffer is
// shorter than the envelope's declared length, so total carries the envelope
// size and Message.Size reports it (consistently with the section 0 field,
// as always).
func newMessageWithTotal(index int, off int64, data []byte, total int) (*Message, error) {
	return newMessageOpts(index, off, data, total, parseOpts{})
}

func newMessageOpts(index int, off int64, data []byte, total int, opts parseOpts) (*Message, error) {
	secs, err := parseSectionsOpts(data, total, opts)
	if err != nil {
		return nil, fmt.Errorf("grib: message %d at offset %d: %w", index, off, err)
	}
	byNum := make(map[int]Section, len(secs))
	for _, s := range secs {
		byNum[s.Number] = s
	}
	return &Message{index: index, off: off, size: total, edition: 2, data: data, secs: secs, byNum: byNum}, nil
}

// newGRIB1Message 把 grib1 包的消息包装进统一外观。节列表也一并搬运，使
// Sections()/Section(n) 在两个版本下行为一致（节号沿用各版本自己的编号）。
//
// newGRIB1Message wraps a grib1-package message into the unified facade. The
// section list is brought over too, so Sections()/Section(n) behave the same
// for both editions (the numbers are each edition's own).
func newGRIB1Message(index int, off int64, data []byte, g1 *grib1.Message) *Message {
	gsecs := g1.RawSections()
	secs := make([]Section, len(gsecs))
	for i, s := range gsecs {
		secs[i] = Section{Number: s.Number, Length: s.Length, Data: s.Data}
	}
	byNum := make(map[int]Section, len(secs))
	for _, s := range secs {
		byNum[s.Number] = s
	}
	return &Message{
		index:   index,
		off:     off,
		size:    len(data),
		edition: 1,
		data:    data,
		secs:    secs,
		byNum:   byNum,
		g1:      g1,
	}
}

// Index 返回本消息在其文件中的位置，从 0 开始计数。
//
// Index is this message's 0-based position in its file.
func (m *Message) Index() int { return m.index }

// Offset 返回消息起点在其文件中的字节偏移。
//
// Offset is the byte offset of the message start in its file.
func (m *Message) Offset() int64 { return m.off }

// Size 返回消息总长度（字节）。
//
// Size is the message total length in bytes.
func (m *Message) Size() int { return m.size }

// Edition 返回 GRIB 版本号：1 或 2。
//
// Edition returns the GRIB edition: 1 or 2.
func (m *Message) Edition() int { return m.edition }

// GRIB1 返回 GRIB1 原生视图（表版本号、十进制比例因子、时间范围指示符、
// 生成过程标识、位图——GRIB2 中不存在的字段都在这里）。对第 2 版消息返回
// (nil, false)。
//
// GRIB1 returns the GRIB1-native view (table versions, decimal scale factor,
// time-range indicator, generating-process id, bitmap — the fields absent
// from GRIB2 all live there). For an edition-2 message it returns
// (nil, false).
func (m *Message) GRIB1() (*grib1.Message, bool) {
	if m.g1 == nil {
		return nil, false
	}
	return m.g1, true
}

// Discipline 返回 WMO 学科代码（GRIB2 第 0 节第 7 八位组，表 0.0），例如
// 0 表示由大气数据导出的产品。
//
// GRIB1 没有该字段（GRIB1 只承载气象产品）；沿用 ecCodes 的 GRIB1→GRIB2
// 转换约定返回 0——这是有定义的映射，而不是占位值。
//
// Discipline is the WMO discipline code (GRIB2 section 0 octet 7, table 0.0),
// e.g. 0 = products derived from atmospheric data.
//
// GRIB1 has no such field (GRIB1 carries only meteorological products);
// following ecCodes' GRIB1→GRIB2 conversion convention it returns 0 — a
// defined mapping, not a placeholder.
func (m *Message) Discipline() int {
	if m.g1 != nil {
		return 0
	}
	return int(m.data[6]) // section 0 octet 7，第 0 节第 7 八位组 / section 0 octet 7
}

// Sections 按消息内顺序列出实际出现的节号。
//
// 节号是各版本自己的：GRIB2 使用 0-7（以 "7777" 结尾的结束节不对外暴露），
// GRIB1 使用 0=IS、1=PDS、2=GDS、3=BMS、4=BDS、5=结束标记。同一节号在两个
// 版本中含义不同（例如 2 在 GRIB2 中是本地使用段，在 GRIB1 中是网格描述
// 段），因此跨版本遍历前先检查 Edition()。
//
// Sections lists the section numbers actually present, in message order.
//
// The numbers are each edition's own: GRIB2 uses 0-7 (the "7777" end section
// is not exposed), GRIB1 uses 0=IS, 1=PDS, 2=GDS, 3=BMS, 4=BDS, 5=end
// marker. The same number means different things in the two editions (e.g.
// 2 is local use in GRIB2 but the grid description section in GRIB1), so
// check Edition() before iterating across editions.
func (m *Message) Sections() []int {
	out := make([]int, len(m.secs))
	for i, s := range m.secs {
		out[i] = s.Number
	}
	return out
}

// Section 返回编号为 n 的节（使用当前版本的编号体系），找不到时返回
// *ErrSectionAbsent。返回的 Section 的 Data 切片与消息共用底层数组，应按只读
// 方式使用。
//
// Section returns the section with the given number (in the current edition's
// numbering), or *ErrSectionAbsent. The returned Section's Data slice shares
// the message's backing array; treat it as read-only.
func (m *Message) Section(n int) (*Section, error) {
	s, ok := m.byNum[n]
	if !ok {
		return nil, &ErrSectionAbsent{Number: n}
	}
	return &s, nil
}
