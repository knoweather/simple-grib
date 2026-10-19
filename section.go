package grib

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Section 表示一个 GRIB2 节。对第 1-7 节，Data[0:4] 是 4 八位组的节长度，
// Data[4] 是节号，Data[5:] 是模板内容（自第 6 八位组起）。第 0 节是固定的
// 16 字节头部，既无长度前缀也无节号字节（"GRIB" + 保留位 + 学科代码 + 版本号
// + 8 八位组的消息总长度）；对它而言 Length 是消息总长度，Data 是这 16 字节
// 头部。第 8 节是 4 八位组的结束标记 "7777"（ecCodes section.8.def：
// `ascii[4] '7777'`）；它不是本地使用段——GRIB2 中唯一的本地使用段是第 2 节
// （ecCodes sections.def）。WMO 模板 CSV 中的字段位置是节内的绝对八位组编号，
// 因此"第 N 八位组"的字段位于 Data[N-1]。
//
// Section is one GRIB2 section. For sections 1-7, Data[0:4] holds the
// 4-octet length, Data[4] the section number, and Data[5:] the template
// content (from octet 6). Section 0 is the fixed 16-byte header with no
// length prefix and no number byte ("GRIB" + reserved + discipline + edition
// + 8-octet message total length); for it Length is the message total length
// and Data is that 16-byte header. Section 8 is the 4-octet end marker
// "7777" (ecCodes section.8.def: `ascii[4] '7777'`); it is not a local-use
// section — the only local-use section in GRIB2 is section 2 (ecCodes
// sections.def). Field positions in the WMO template CSVs are absolute octet
// numbers within the section, so a field at "octet N" lives at Data[N-1].
type Section struct {
	Number int    // octet 5 (standard 0-7; 8 = end marker "7777", never exposed here) // 第 8 八位组（标准 0-7；8 = 结束标记 "7777"，从不对外暴露）/ octet 5 (standard 0-7; 8 = end marker "7777", never exposed here)
	Length int    // sections 1+: octets 1-4; section 0: message total length (octets 9-16) // 第 1-4 八位组；第 0 节：消息总长度（第 9-16 八位组）/ sections 1+: octets 1-4; section 0: message total length (octets 9-16)
	Data   []byte // full section bytes starting at octet 1; sections 1+: Data[5:] = content from octet 6 // 自第 1 八位组起的完整节字节；第 1 节起：Data[5:] = 自第 6 八位组起的内容 / full section bytes starting at octet 1; sections 1+: Data[5:] = content from octet 6
}

// parseOpts 携带节解析的宽松度选项：
//   - lenientEndSection：接受第 7 节之后最多 4 字节的任意尾部数据，代替
//     必须为标准 "7777" 结束标记的严格校验。
//
// 结束节（WMO 第 8 节）的规范要求：每条格式良好的 GRIB2 消息都以 4 八位组标
// 记 "7777" 结尾（ecCodes section.8.def 强制要求）。恰为 "7777" 的尾部会被
// 接受并忽略；对省略结束节的宽松生成器，缺失尾部也被容忍；除此之外任何非空
// 尾部都属于格式错误——否则解析器会把损坏的消息当成合法消息悄悄放行，
// 掩盖截断或拼接错误。
//
// parseOpts carries the leniency knobs for section parsing:
//   - lenientEndSection: accept up to 4 bytes of arbitrary trailing data
//     after section 7 instead of requiring the standard "7777" end marker.
//
// End section (WMO section 8): every well-formed GRIB2 message ends with the
// 4-octet marker "7777" (ecCodes section.8.def requires it). A trailer that
// is exactly "7777" is accepted and ignored; a missing trailer is tolerated
// for lenient producers. Any other non-empty trailer is malformed — silently
// accepting it would let truncated or spliced messages pass as valid.
type parseOpts struct {
	// lenientEndSection accepts up to 4 bytes of arbitrary trailing data
	// after section 7 instead of requiring the standard "7777" end marker.
	lenientEndSection bool
}

func parseSections(data []byte) ([]Section, error) {
	return parseSectionsOpts(data, len(data), parseOpts{})
}

// parseSectionsWithTotal 是显式给出期望总长度的 parseSections：第 0 节
// totalLength 字段必须等于 total（而不是 len(data)）。由多要素信封
// [s0][s1][s3][s4..s7] 拼装出的单要素视图比信封声明的 totalLength 短，这正
// 是该校验需要参数化的原因；对独立消息 total == len(data)，行为与
// parseSections 完全一致。
//
// parseSectionsWithTotal is parseSections with an explicit expected total
// length: the section 0 totalLength field must equal total (rather than
// len(data)). The single-field view assembled from a multi-field envelope
// [s0][s1][s3][s4..s7] is shorter than the envelope's declared totalLength,
// which is why the check is parameterized; for a standalone message
// total == len(data) and the behavior matches parseSections exactly.
func parseSectionsWithTotal(data []byte, total int) ([]Section, error) {
	return parseSectionsOpts(data, total, parseOpts{})
}

func parseSectionsOpts(data []byte, total int, opts parseOpts) ([]Section, error) {
	if len(data) < 16 {
		return nil, fmt.Errorf("grib: %w (message has only %d bytes)", ErrTruncated, len(data))
	}
	secs := []Section{{Number: 0, Length: len(data), Data: data[0:16]}}
	pos := 16
	for pos < len(data) {
		if len(data)-pos < 5 {
			// 剩余字节只可能是第 8 节的结束标记 "7777"。
			// The remaining bytes can only be the section 8 end marker "7777".
			rem := data[pos:]
			if !opts.lenientEndSection && !isEndMarker(rem) {
				return nil, fmt.Errorf("grib: %w (%d trailing bytes after the last section are not the '7777' end section)",
					ErrBadSectionLength, len(rem))
			}
			break
		}
		slen := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		num := int(data[pos+4])
		if slen < 5 {
			return nil, fmt.Errorf("grib: %w (section at offset %d claims length %d)", ErrBadSectionLength, pos, slen)
		}
		if slen > len(data)-pos {
			return nil, fmt.Errorf("grib: %w (section at offset %d claims %d bytes, only %d left in message)",
				ErrBadSectionLength, pos, slen, len(data)-pos)
		}
		if num <= secs[len(secs)-1].Number {
			return nil, fmt.Errorf("grib: sections out of order (got %d after %d)", num, secs[len(secs)-1].Number)
		}
		secs = append(secs, Section{Number: num, Length: slen, Data: data[pos : pos+slen]})
		pos += slen
	}
	if secs[len(secs)-1].Number != 7 {
		return nil, fmt.Errorf("grib: last section is %d, want 7", secs[len(secs)-1].Number)
	}
	for _, need := range []int{1, 3, 4} {
		if !hasSection(secs, need) {
			return nil, &ErrSectionAbsent{Number: need}
		}
	}
	if totalField := int64(binary.BigEndian.Uint64(data[8:16])); totalField != int64(total) {
		return nil, fmt.Errorf("grib: %w (section 0 total length field is %d, message is %d bytes)",
			ErrBadSectionLength, totalField, total)
	}
	return secs, nil
}

// SectionRef 是文件中某个节的位置（仅头部；正文未读取）。供多要素信封索引
// 使用。
//
// SectionRef is the location of one section in the file (header only; the
// body is not read). Used by the multi-field envelope index.
type SectionRef struct {
	Number int   // section number octet // 节号八位组 / section number octet
	Off    int64 // absolute file offset of the section's first octet // 该节第一个八位组的文件绝对偏移 / absolute file offset of the section's first octet
	Length int   // full section length (octets 1-4) // 完整节长度（第 1-4 八位组）/ full section length (octets 1-4)
}

// envelopeIndex 是多要素信封的仅头部结构：所有要素共享的节（按文件顺序的第 1
// 与第 3 节），加上每个数据要素组一份的节跨度列表。JMA 风格的单信封多要素消
// 息布局为 s0 | s1 s3 | [s4 s5 s6 s7] x N | （≤4 字节尾部），其中第 0 节的
// totalLength 覆盖整个信封；每个要素以虚拟消息 [s0][s1][s3][本组] 的形式对外
// 暴露。
//
// envelopeIndex is the header-only structure of a multi-field envelope: the
// sections shared by all fields (1 and 3, in file order) plus one span list
// per data-field group. The JMA-style single-envelope multi-field message
// layout is s0 | s1 s3 | [s4 s5 s6 s7] x N | (<=4-byte trailer), where
// section 0's totalLength covers the whole envelope; each field is exposed as
// the virtual message [s0][s1][s3][its group].
type envelopeIndex struct {
	shared   []SectionRef   // sections 1..3, shared by every field // 第 1..3 节，为每个要素共享 / sections 1..3, shared by every field
	groups   [][]SectionRef // one entry per data-field group (sections 4-7) // 每个数据要素组一条（第 4-7 节）/ one entry per data-field group (sections 4-7)
	totalLen int64          // section 0 totalLength (the whole envelope) // 第 0 节 totalLength（整个信封）/ section 0 totalLength (the whole envelope)
}

// walkEnvelope 从 off（信封第 0 节第一个八位组处）开始遍历信封的节头，其声明
// 总长度为 totalLen，并判定布局类型。对单要素信封返回 nil——无论是经典的
// （节号严格递增）还是在任何多要素组开始之前就结构性损坏的——把完整校验推迟
// 到读取时的 parseSections，使 open/read 的错误行为保持逐字节一致。对格式良
// 好的多要素信封返回 *envelopeIndex；只有当多要素结构已经确立（第 7 节之后直
// 接出现第 4 节）而其后的数据又损坏时才返回错误。
//
// 多要素规则（失败即报错；JMA GPV 单信封多要素）：
//   - 头部节必须恰为 [1, 3]（多要素信封不支持第 2 节与第 8-255 本地使用节）；
//   - 每个要素组是从 4 开始、到 7 结束、编号 4 至 7 严格递增的一段；
//   - 每个组的节布局必须与第一个组完全相同；
//   - 新组只能以"第 7 节之后直接出现第 4 节"的方式开始；任何其他回退都属于
//     "节顺序错误"（即经典消息）；
//   - 遍历必须恰好耗尽信封（与 parseSections 一样容忍尾随的第 8 节结束标记
//     "7777"）。
//
// walkEnvelope walks the section headers of an envelope starting at off (the
// first octet of its section 0), with declared total length totalLen, and
// classifies the layout. It returns nil for a single-field envelope — whether
// classic (section numbers strictly increasing) or structurally corrupted
// before any multi-field group starts — deferring full validation to the
// read-time parseSections so open/read error behavior stays byte-identical.
// For a well-formed multi-field envelope it returns *envelopeIndex; an error
// is returned only once the multi-field structure is already armed (a section
// 4 directly following a section 7) and then the data goes bad.
//
// Multi-field rules (fail-loud; JMA GPV single-envelope multi-field):
//   - header sections must be exactly [1, 3] (section 2 and local-use
//     sections 8-255 are not supported in multi-field envelopes);
//   - each field group is a strictly increasing run numbered 4 through 7,
//     starting at 4 and ending at 7;
//   - every group must have the same section layout as the first;
//   - a new group may only start with a section 4 directly following a
//     section 7; any other decrease is "sections out of order" (a classic
//     message);
//   - the walk must consume the envelope exactly (a trailing section 8 end
//     marker "7777" is tolerated, like parseSections).
func walkEnvelope(r io.ReaderAt, off, totalLen int64) (*envelopeIndex, error) {
	pos := off + 16
	end := off + totalLen
	var nums []int
	var spans []SectionRef
	prev := 0
	multiField := false // 看到"7 之后直接 4"的组边界时立即置位 / set the moment a "4 directly after 7" group boundary is seen
	for pos < end {
		if end-pos < 5 {
			trailing := end - pos
			if trailing > 4 && multiField {
				return nil, fmt.Errorf("grib: %w (%d trailing bytes after last section)", ErrTruncated, trailing)
			}
			break // 第 8 节结束标记（或经典异常；由读取时的 parseSections 复查）/ section 8 end marker (or classic anomaly; parseSections re-checks at read time)
		}
		var hdr [5]byte
		if _, err := r.ReadAt(hdr[:], pos); err != nil {
			if multiField {
				return nil, fmt.Errorf("grib: read section header at offset %d: %w", pos, err)
			}
			break // 经典异常；推迟到读取时处理 / classic anomaly; defer to read time
		}
		slen := int64(binary.BigEndian.Uint32(hdr[0:4]))
		num := int(hdr[4])
		if slen < 5 || pos+slen > end {
			if multiField {
				if slen < 5 {
					return nil, fmt.Errorf("grib: %w (section at offset %d claims length %d)", ErrBadSectionLength, pos, slen)
				}
				return nil, fmt.Errorf("grib: %w (section at offset %d claims %d bytes, only %d left in message)",
					ErrBadSectionLength, pos, slen, end-pos)
			}
			break // 经典异常；读取时同样推迟并得到相同错误 / classic anomaly; deferred to read time there, which yields the same error
		}
		if num <= prev {
			if num == 4 && prev == 7 {
				multiField = true // 新数据要素组的开始 / start of a new data-field group
			} else if multiField {
				return nil, fmt.Errorf("grib: sections out of order (got %d after %d)", num, prev)
			} else {
				break // 经典乱序；读取时同样推迟并得到相同错误 / classic out-of-order; deferred to read time there, same error
			}
		}
		nums = append(nums, num)
		spans = append(spans, SectionRef{Number: num, Off: pos, Length: int(slen)})
		prev = num
		pos += slen
	}
	if len(nums) == 0 || !multiField {
		return nil, nil // 单要素（经典或损坏的经典消息）；读取时再做完整校验 / single field (classic or corrupted classic); validated at read time
	}
	// 多要素：所有节头都在手，立即校验结构。
	// Multi-field: validate the structure now (all headers are in hand).
	if len(nums) < 2 || nums[0] != 1 || nums[1] != 3 {
		return nil, fmt.Errorf("grib: multi-field envelope must start with sections 1 and 3 (got %v)", nums[:2])
	}
	for _, n := range nums {
		if n > 7 {
			return nil, fmt.Errorf("grib: local-use section %d not supported in multi-field envelopes", n)
		}
	}
	h := 0
	for h < len(spans) && spans[h].Number != 4 {
		h++
	}
	if h == len(spans) {
		return nil, fmt.Errorf("grib: %w (multi-field envelope has no data description section)", ErrTruncated)
	}
	var groups [][]SectionRef
	var cur []SectionRef
	for _, s := range spans[h:] {
		cur = append(cur, s)
		if s.Number == 7 {
			groups = append(groups, cur)
			cur = nil
		}
	}
	if len(cur) > 0 || len(groups) < 2 {
		return nil, fmt.Errorf("grib: %w (multi-field envelope has an incomplete or single field group)", ErrTruncated)
	}
	for k := 1; k < len(groups); k++ {
		if len(groups[k]) != len(groups[0]) {
			return nil, fmt.Errorf("grib: multi-field envelope field %d has %d sections, field 0 has %d", k, len(groups[k]), len(groups[0]))
		}
		for j := range groups[k] {
			if groups[k][j].Number != groups[0][j].Number {
				return nil, fmt.Errorf("grib: multi-field envelope field %d section layout differs from field 0 (got %d at position %d)", k, groups[k][j].Number, j)
			}
		}
	}
	return &envelopeIndex{shared: spans[:h], groups: groups, totalLen: totalLen}, nil
}

// hasSection 报告是否存在编号为 num 的节。
// hasSection reports whether a section with the given number is present.
func hasSection(secs []Section, num int) bool {
	for _, s := range secs {
		if s.Number == num {
			return true
		}
	}
	return false
}

// isEndMarker 报告 b 是否为 WMO GRIB2 第 8 节的结束标记 "7777"（ecCodes
// section.8.def：`ascii[4] '7777'`）。空的 b 也被视为结束标记，以便仍能解析
// 省略了可选结束节的生成器输出。
//
// isEndMarker reports whether b is the WMO GRIB2 section 8 end marker "7777"
// (ecCodes section.8.def: `ascii[4] '7777'`). An empty b is accepted too, so
// producers that omit the optional end section still parse.
func isEndMarker(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	return len(b) == 4 && b[0] == '7' && b[1] == '7' && b[2] == '7' && b[3] == '7'
}
