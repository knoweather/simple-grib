//
// gribw/grid.go —— 针对网格定义模板 3.0 的第 3 节（网格定义节）写入器。
//
// 第 3 节的字节也可以经 SetGridBytes 手工构造后注入；那种情况下调用方必须
// 自己填好 octet 55 的分辨率标志与点数。EncodeGrid 填补这个缺口：它是读取端
// grid.New 路径（模板 3.0）的精确逆运算，从参数推导分辨率标志，而不是接受
// 一个未经校验的字节。手工构造的节若存了 Di/Dj 却把 octet 55 留在 0x00，就是
// 自相矛盾——标志说"未给出增量"，于是 eccodes 会把
// iDirectionIncrement/jDirectionIncrement 报为缺失，无法构建网格。
//
// 标志表 3.3（MSB 在前的位编号；内置表见 specdata 的
// GRIB2_CodeFlag_3_3_FlagTable_en.csv）：位 1-2 保留，位 3 = 给出 i 方向增量，
// 位 4 = 给出 j 方向增量，位 5 = 矢量分量标志，位 6-8 保留且必须为 0。因此
// 同时存 Di 与 Dj 的模板 3.0 节应携带 0x30 而非 0xF0——0xF0 还置了两个保留位。
// 本文件中每个 eccodes 生成的样例对模板 3.0-3.3 都写 0x30（高斯网格写 0x20，
// 其 Dj 是推导出来的，位 4 保持 0），并且读取端无论标志如何都以存储的 Di/Dj
// 为准，所以 0x00 不只是不整洁——它让该节自相矛盾。
//
// 第 3 节长度：无点表的模板 3.0 为 72 个 octet（最后固定字段止于 octet 72）；
// 若 octet 72 之后附有准规则点表，则为 72 + Nj×宽度。
//
// gribw/grid.go — section 3 (grid definition) writer for grid definition
// template 3.0.
//
// Section 3 bytes can also be injected hand-built via SetGridBytes; in that
// case the caller must fill in the octet 55 resolution flags and the point
// count itself. EncodeGrid closes that gap: it is the exact inverse of the
// reader's grid.New path for template 3.0, deriving the resolution flags from
// the parameters instead of accepting an unchecked byte. A hand-built section
// that stored Di/Dj but left octet 55 at 0x00 is self-contradictory — the flag
// says "no increment given", so eccodes reports missing
// iDirectionIncrement/jDirectionIncrement and cannot build the grid.
//
// Flag table 3.3 (MSB-first bit numbering; vendored table in specdata,
// GRIB2_CodeFlag_3_3_FlagTable_en.csv): bits 1-2 Reserved, bit 3 = i-direction
// increment given, bit 4 = j-direction increment given, bit 5 = vector
// components flag, bits 6-8 Reserved and must be 0. A template 3.0 section
// storing both Di and Dj therefore carries 0x30, not 0xF0 — 0xF0 also sets the
// two reserved bits. Every eccodes-generated fixture here writes 0x30 for
// templates 3.0-3.3 (0x20 for Gaussian grids, where Dj is derived and bit 4
// stays 0), and the reader treats the stored Di/Dj as authoritative regardless
// of the flags, so 0x00 is not just untidy — the section contradicts itself.
//
// Section 3 length: 72 octets for template 3.0 without a point list (the last
// fixed field ends at octet 72), or 72 + Nj×width with a quasi-regular point
// list attached after octet 72.

package gribw

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/knoweather/simple-grib/grid"
	"github.com/knoweather/simple-grib/spec"
)

// missingU32 是 4 字节无符号字段的 WMO "缺失"哨兵值——
// 即 grid.New 读作缺失的那个值。
//
// missingU32 is the WMO "missing" sentinel for 4-byte unsigned fields —
// the value grid.New reads as missing.
const missingU32 = uint32(0xFFFFFFFF)

// 标志表 3.3 的位值，MSB 在前的位编号。
// Flag table 3.3 bit values, MSB-first bit numbering.
const (
	flagIIncrementGiven  = 0x20 // 位 3 / bit 3
	flagJIncrementGiven  = 0x10 // 位 4 / bit 4
	flagVectorComponents = 0x08 // 位 5 / bit 5
)

// GridParams 保存网格定义模板 3.0 的显式参数。角度以度为单位；存储的整数
// 是 §5.3 角度单位（basicAngle / 细分，默认 1 / 1e6）作用后的结果。比例尺
// 或值为 -1 时写为 WMO 缺失哨兵值。
//
// GridParams holds the explicit grid definition template 3.0 parameters.
// Angles are degrees; the stored integers are what the angle unit of §5.3
// (basicAngle / subdivisions, default 1 / 1e6) produces. A scale factor or
// value of -1 is written as the WMO missing sentinel.
type GridParams struct {
	Source int // 网格定义来源 / grid definition source, octet 6, table 3.0
	Shape  int // 参考系形状 / shape of the reference system, octet 15, table 3.2

	// 地球形状。对表 3.2 的固定形状而言这三对参数无意义，按缺失写出
	// （与 eccodes 一致）；生产者自定义形状 1、3、7 需要给出值。
	// 比例尺是 [0,9] 内的 WMO 指数。
	//
	// Earth shape. For the fixed shapes of table 3.2 these three pairs are
	// meaningless and written as missing (as eccodes does); producer shapes
	// 1, 3 and 7 need values. Scale factors are WMO exponents in [0,9].
	RadiusScale, RadiusValue int
	MajorScale, MajorValue   int
	MinorScale, MinorValue   int

	// Ni / Nj（octets 31-34、35-38）。-1 = 缺失，仅对带点表的准规则网格合法。
	//
	// Ni / Nj (octets 31-34, 35-38). -1 = missing, legal only for
	// quasi-regular grids with a point list.
	Ni, Nj int

	// 角度单位（§5.3）：单位 = BasicAngle / Subdivisions 度，
	// BasicAngle 为 0 按 1 读，Subdivisions 为 0 或缺失按 1e6 读。
	//
	// Angle unit (§5.3): unit = BasicAngle / Subdivisions degrees, with
	// BasicAngle 0 read as 1 and Subdivisions 0 or missing read as 1e6.
	BasicAngle   int
	Subdivisions int

	// 四角角度，单位为度。La1/Lo1/La2/Lo2 必填：读取端对缺失的缩放角度
	// 会快速失败，所以写入端绝不能产生缺失值。
	//
	// Corner angles, degrees. La1/Lo1/La2/Lo2 are required: the reader fails
	// loud on a missing scaled angle, so the writer must never produce one.
	La1, Lo1, La2, Lo2 float64

	// Di / Dj，单位为度。nil = "未给出增量"：该字段写为缺失哨兵值，
	// 对应的标志表 3.3 位保持 0。
	//
	// Di / Dj, degrees. nil = "increment not given": the field is written as
	// the missing sentinel and the corresponding flag-table 3.3 bit stays 0.
	Di, Dj *float64

	// 矢量分量标志（标志表 3.3 的位 5）。
	// Vector components flag (bit 5 of flag table 3.3).
	VectorComponents bool

	ScanMode uint8 // 扫描模式 / scan mode, octet 72, flag table 3.4

	// 附在 octet 72 之后的可选点表（表 3.11）。解释方式 0 且列表为空 =
	// 无点表；宽度 1..4 的 1 或 2 = 准规则网格，此时 Ni/Nj 必须恰好缺一个。
	//
	// Optional point list appended after octet 72 (table 3.11). Interpretation
	// 0 with an empty list = no list; 1 or 2 with width 1..4 = quasi-regular
	// grid, in which case exactly one of Ni/Nj must be missing.
	PointListInterpretation int
	PointListWidth          int
	PointList               []int
}

// EncodeGrid 构造完整的第 3 节（网格定义模板 3.0），含 4 字节长度前缀——
// 与 SetGridBytes 期望的输入形状完全一致。分辨率标志由实际存储的内容推导，
// 绝不接受未经校验的字节；构造完成后还会用 grid.New 解析一遍，保证写入端
// 不会发出自家读取端会拒绝的字节（这同时执行了"存储的 Di/Dj 必须与首/末
// 格点一致"的跨度检查）。
//
// EncodeGrid builds a complete section 3 (grid definition template 3.0)
// including the 4-byte length prefix — exactly the input shape SetGridBytes
// expects. The resolution flags are derived from what is actually stored, never
// accepted as an unchecked byte; the finished section is additionally parsed
// with grid.New so the writer cannot emit bytes its own reader would reject
// (this also runs the span check that a stored Di/Dj must agree with the
// first/last grid point).
func EncodeGrid(p GridParams) ([]byte, error) {
	def, ok := spec.Template(3, 0)
	if !ok {
		return nil, fmt.Errorf("gribw: no spec definition for grid definition template 3.0")
	}

	if err := checkTableCode("3.0", "grid definition source", p.Source); err != nil {
		return nil, fmt.Errorf("gribw: section 3: %w", err)
	}
	if p.Source == 255 {
		return nil, fmt.Errorf("gribw: section 3: grid definition source 255 means \"no grid definition applies\", which cannot be written as a grid section")
	}
	if err := checkTableCode("3.2", "shape of the reference system", p.Shape); err != nil {
		return nil, fmt.Errorf("gribw: section 3: %w", err)
	}
	if p.Shape == 255 {
		return nil, fmt.Errorf("gribw: section 3: shape of the reference system 255 (missing) cannot be written")
	}

	producerShape := p.Shape == 1 || p.Shape == 3 || p.Shape == 7
	if p.Shape == 7 && p.MajorValue < 0 && p.MinorValue < 0 {
		return nil, fmt.Errorf("gribw: section 3: table 3.2 shape 7 needs at least one of the major/minor axis values")
	}

	hasList := len(p.PointList) > 0
	if hasList {
		if p.PointListInterpretation != 1 && p.PointListInterpretation != 2 {
			return nil, fmt.Errorf("gribw: section 3: point list interpretation %d not writable (table 3.11: 0 = no list, 1 and 2 = per-row counts; 3 stores latitudes, which the reader's parseReduced reads as counts)", p.PointListInterpretation)
		}
		if p.PointListWidth < 1 || p.PointListWidth > 4 {
			return nil, fmt.Errorf("gribw: section 3: point list element width %d, want 1..4", p.PointListWidth)
		}
		if (p.Ni == -1) == (p.Nj == -1) {
			return nil, fmt.Errorf("gribw: section 3: quasi-regular grid needs exactly one of Ni/Nj missing (Ni=%d Nj=%d)", p.Ni, p.Nj)
		}
		// §5.6 / 注 15：准规则网格上，变化维度的增量没有单一取值，
		// 因此必须保持缺失，其标志位保持 0。
		// §5.6 / Note 15: on a quasi-regular grid, the increment of the
		// varying dimension has no single value, so it must stay missing and
		// its flag bit stay 0.
		if p.Ni == -1 && p.Di != nil {
			return nil, fmt.Errorf("gribw: section 3: quasi-regular grid with Ni missing (per-row counts) cannot carry a stored i increment (§5.6/Note 15): leave Di unset")
		}
		if p.Nj == -1 && p.Dj != nil {
			return nil, fmt.Errorf("gribw: section 3: quasi-regular grid with Nj missing (per-column counts) cannot carry a stored j increment (§5.6/Note 15): leave Dj unset")
		}
	} else {
		if p.PointListInterpretation != 0 || p.PointListWidth != 0 {
			return nil, fmt.Errorf("gribw: section 3: no point list but interpretation %d / width %d set (table 3.11 code 0 with width 0)", p.PointListInterpretation, p.PointListWidth)
		}
		if p.Ni < 1 || p.Nj < 1 {
			return nil, fmt.Errorf("gribw: section 3: regular grid needs Ni>=1 and Nj>=1 (got Ni=%d Nj=%d)", p.Ni, p.Nj)
		}
	}

	// octets 7-10：读取端计算出的数据点数——规则网格为 Ni×Nj，
	// 准规则网格为 sum(list)。
	// Octets 7-10: the number of data points the reader computes — Ni×Nj for
	// a regular grid, sum(list) for a quasi-regular one.
	var ndp uint64
	if hasList {
		for i, n := range p.PointList {
			if n < 1 {
				return nil, fmt.Errorf("gribw: section 3: point list entry[%d] = %d, want >= 1", i, n)
			}
			if uint64(n) >= 1<<uint(8*p.PointListWidth) {
				return nil, fmt.Errorf("gribw: section 3: point list entry[%d] = %d does not fit %d octets", i, n, p.PointListWidth)
			}
			ndp += uint64(n)
		}
	} else {
		ndp = uint64(p.Ni) * uint64(p.Nj)
	}
	if ndp > math.MaxUint32 {
		return nil, fmt.Errorf("gribw: section 3: number of data points %d exceeds U32", ndp)
	}

	unit := angleUnit(p.BasicAngle, p.Subdivisions)

	buf := make([]byte, 72)
	// octets 6-14 是模板遍历之外的节级字段（遍历从 octet 15 开始），
	// 所以像 encodeSec4 那样直接写入。
	// Octets 6-14 are section-level fields outside the template walk (the walk
	// starts at octet 15), so they are written directly, like encodeSec4 does.
	buf[4] = 3
	buf[5] = byte(p.Source) // 网格定义来源 / grid definition source, octet 6, table 3.0
	binary.BigEndian.PutUint32(buf[6:10], uint32(ndp))
	buf[10] = byte(p.PointListWidth)          // 点表元素宽度 / point list octet width, octet 11
	buf[11] = byte(p.PointListInterpretation) // 点表解释方式 / point list interpretation, octet 12, table 3.11
	binary.BigEndian.PutUint16(buf[12:14], 0) // 模板号 / template number, octets 13-14

	if err := setU8(def, buf, 15, byte(p.Shape)); err != nil { // 参考系形状 / shape of the reference system, table 3.2
		return nil, err
	}

	shapeFields := []struct {
		scaleOctet, valueOctet int
		scale, value           int
		required               bool
	}{
		{16, 17, p.RadiusScale, p.RadiusValue, p.Shape == 1},
		{21, 22, p.MajorScale, p.MajorValue, p.Shape == 3 || p.Shape == 7},
		{26, 27, p.MinorScale, p.MinorValue, p.Shape == 3},
	}
	for _, f := range shapeFields {
		s, v := f.scale, f.value
		if !producerShape {
			s, v = -1, -1 // 固定形状不携带值；eccodes 存缺失 / fixed shapes carry no values; eccodes stores missing
		}
		if f.required && v < 0 {
			return nil, fmt.Errorf("gribw: section 3: table 3.2 shape %d needs the value at octets %d-%d", p.Shape, f.valueOctet, f.valueOctet+3)
		}
		if err := writeScaledField(def, buf, f.scaleOctet, f.valueOctet, s, v); err != nil {
			return nil, fmt.Errorf("gribw: section 3: %w", err)
		}
	}

	for _, e := range []struct {
		octet, v int
		what     string
	}{
		{31, p.Ni, "Ni"}, {35, p.Nj, "Nj"},
		{39, p.BasicAngle, "basic angle"}, {43, p.Subdivisions, "subdivisions"},
	} {
		if e.v < -1 || e.v > math.MaxUint32 {
			return nil, fmt.Errorf("gribw: section 3: %s %d at octets %d-%d outside [0,2^32-1] (-1 = missing)", e.what, e.v, e.octet, e.octet+3)
		}
		raw := uint32(missingU32)
		if e.v >= 0 {
			raw = uint32(e.v)
		}
		if err := setU32(def, buf, e.octet, raw); err != nil {
			return nil, err
		}
	}

	for _, e := range []struct {
		octet int
		v     float64
		what  string
	}{
		{47, p.La1, "latitude of first grid point"},
		{51, p.Lo1, "longitude of first grid point"},
		{56, p.La2, "latitude of last grid point"},
		{60, p.Lo2, "longitude of last grid point"},
	} {
		raw, err := encodeAngle(e.v, unit)
		if err != nil {
			return nil, fmt.Errorf("gribw: section 3: %s (octets %d-%d): %w", e.what, e.octet, e.octet+3, err)
		}
		if err := setU32(def, buf, e.octet, raw); err != nil {
			return nil, err
		}
	}

	// octet 55：分辨率与分量标志，由实际存储的内容推导。
	// 这正是调用方以前必须手工填对的字段。
	// Octet 55: resolution and component flags, derived from what is actually
	// stored. This is the field callers previously had to get right by hand.
	flags := byte(0)
	if p.Di != nil {
		flags |= flagIIncrementGiven
	}
	if p.Dj != nil {
		flags |= flagJIncrementGiven
	}
	if p.VectorComponents {
		flags |= flagVectorComponents
	}
	if err := setU8(def, buf, 55, flags); err != nil {
		return nil, err
	}

	for _, e := range []struct {
		octet int
		v     *float64
		what  string
	}{
		{64, p.Di, "i direction increment"},
		{68, p.Dj, "j direction increment"},
	} {
		if e.v == nil {
			if err := setU32(def, buf, e.octet, missingU32); err != nil {
				return nil, err
			}
			continue
		}
		raw, err := encodeAngle(*e.v, unit)
		if err != nil {
			return nil, fmt.Errorf("gribw: section 3: %s (octets %d-%d): %w", e.what, e.octet, e.octet+3, err)
		}
		if err := setU32(def, buf, e.octet, raw); err != nil {
			return nil, err
		}
	}

	if err := setU8(def, buf, 72, p.ScanMode); err != nil { // 扫描模式 / scan mode, table 3.4
		return nil, err
	}

	for _, n := range p.PointList {
		for b := p.PointListWidth - 1; b >= 0; b-- {
			buf = append(buf, byte(uint64(n)>>uint(8*b)&0xFF))
		}
	}
	binary.BigEndian.PutUint32(buf[0:4], uint32(len(buf)))

	// 快速失败自检：该节必须能被本库的读取端解析。
	// Fail-loud self-check: the section must parse with this library's reader.
	if _, err := grid.New(buf); err != nil {
		return nil, fmt.Errorf("gribw: section 3: built section does not parse: %w", err)
	}
	return buf, nil
}

// SetGridParams 由 GridParams 构造第 3 节并挂接到消息上
// （EncodeGrid + SetGridBytes 合为一次调用）。
//
// SetGridParams builds section 3 from GridParams and attaches it to the
// message (EncodeGrid + SetGridBytes in one call).
func (m *Message) SetGridParams(p GridParams) error {
	data, err := EncodeGrid(p)
	if err != nil {
		return err
	}
	return m.SetGridBytes(data)
}

// checkTableCode 对照内置 WMO 表校验单字节代码码值。保留区间一律拒绝；
// 标记为"留给本地使用（reserved for local use）"的代码则接受。
//
// checkTableCode validates a single-byte code against the vendored WMO
// table. Reserved ranges are rejected; codes "reserved for local use" are
// accepted.
func checkTableCode(id, what string, code int) error {
	if code < 0 || code > 255 {
		return fmt.Errorf("%s %d out of range [0,255]", what, code)
	}
	t, ok := spec.TableByID(id)
	if !ok {
		return fmt.Errorf("no vendored table %s", id)
	}
	e, found := t.Lookup(code)
	if !found {
		return fmt.Errorf("%s %d is not in table %s", what, code, id)
	}
	if e.Name == "Reserved" {
		return fmt.Errorf("%s %d is Reserved in table %s", what, code, id)
	}
	return nil
}

// angleUnit 是读取端 §5.3 规则在写入侧的复刻：
// basicAngle / subdivisions；basicAngle 为 0 按 1 读，subdivisions 为 0 或
// 缺失按 1e6 读。
//
// angleUnit is the write-side copy of the reader's §5.3 rule:
// basicAngle / subdivisions, with basicAngle 0 read as 1 and subdivisions 0 or
// missing read as 1e6.
func angleUnit(basicAngle, subdivisions int) float64 {
	num := float64(basicAngle)
	if num == 0 || basicAngle == -1 {
		num = 1 // §5.3：basic angle 为 0 或缺失时按 1 读 / a basic angle of 0 or missing reads as 1
	}
	den := 1e6
	if subdivisions > 0 && subdivisions != int(missingU32) {
		den = float64(subdivisions)
	}
	return num / den
}

// writeScaledField 写出一对 WMO 比例尺 + 缩放值。任一侧为 -1 时写为缺失
// 哨兵值（255 / 全 1）。
//
// writeScaledField writes a WMO scale factor + scaled value pair. A -1 on
// either side is written as the missing sentinel (255 / all-ones).
func writeScaledField(def *spec.TemplateDef, buf []byte, scaleOctet, valueOctet, scale, value int) error {
	if scale < -1 || scale > 9 {
		return fmt.Errorf("scale factor %d at octet %d outside [0,9] (-1 = missing)", scale, scaleOctet)
	}
	if value < -1 || value > math.MaxUint32 {
		return fmt.Errorf("value %d at octets %d-%d outside [0,2^32-1] (-1 = missing)", value, valueOctet, valueOctet+3)
	}
	s := uint8(255)
	if scale >= 0 {
		s = uint8(scale)
	}
	v := uint32(missingU32)
	if value >= 0 {
		v = uint32(value)
	}
	if err := setU8(def, buf, scaleOctet, s); err != nil {
		return err
	}
	return setU32(def, buf, valueOctet, v)
}

// encodeAngle 按读取端的"符号-幅值 int32"约定存储角度：
// bit 31 为符号位，低 31 位为幅值。该值必须是角度单位的精确整数倍——否则
// 经 grid.New 往返后会与输入不一致，届时宁可快速失败也不静默取整。
//
// encodeAngle stores an angle as the reader's "sign-magnitude int32":
// bit 31 = sign, the low 31 bits = magnitude. The value must be an exact
// multiple of the angle unit — otherwise a round-trip through grid.New would
// disagree with the input, and that fails loud instead of silently rounding.
func encodeAngle(deg float64, unit float64) (uint32, error) {
	if math.IsNaN(deg) || math.IsInf(deg, 0) {
		return 0, fmt.Errorf("angle %v is not finite", deg)
	}
	q := deg / unit
	r := math.Round(q)
	if math.Abs(q-r) > 1e-6 {
		return 0, fmt.Errorf("angle %v degrees is %.6f angle units, not an exact multiple of the unit %g", deg, q, unit)
	}
	if math.Abs(r) > math.MaxInt32 {
		return 0, fmt.Errorf("angle %v degrees (%.0f units) exceeds the sign-magnitude int31 range", deg, r)
	}
	if r < 0 {
		return 0x80000000 | uint32(-int64(r)), nil
	}
	return uint32(int64(r)), nil
}
