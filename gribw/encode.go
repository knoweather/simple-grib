package gribw

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	grib "github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/spec"
)

// fieldRange 按 1 起算的 section octet 查找固定字段，返回 0 起算的偏移量，
// 查表用的是与读取端相同的内置 WMO 规范定义。字段缺失或类型不符一律快速
// 失败：一旦 WMO 表 CSV 有漂移，写入端会在这里报错，而不是在错误偏移上
// 静默写字节。
//
// fieldRange returns the 0-based offset of the fixed field that starts at the
// given 1-based section octet, looking it up in the same vendored WMO spec
// definition the reader uses. A missing field or a kind mismatch fails loud:
// if the WMO table CSV ever drifts, the writer breaks here instead of silently
// emitting bytes at the wrong offset.
func fieldRange(def *spec.TemplateDef, octet int, want spec.Kind) (int, error) {
	for _, f := range def.Fields {
		if f.Start+1 == octet {
			if f.Kind != want {
				return 0, fmt.Errorf("gribw: template %s field at octet %d is kind %d, want %d", def.ID, octet, f.Kind, want)
			}
			return f.Start, nil
		}
	}
	return 0, fmt.Errorf("gribw: template %s has no fixed field at octet %d", def.ID, octet)
}

// setU8 把 1 字节值写入模板中位于指定 octet 的 u8 字段。
// setU8 writes a 1-byte value into the template's u8 field at the given octet.
func setU8(def *spec.TemplateDef, buf []byte, octet int, v uint8) error {
	off, err := fieldRange(def, octet, spec.KindU8)
	if err != nil {
		return err
	}
	buf[off] = v
	return nil
}

// setU16 把 2 字节大端值写入模板中位于指定 octet 的 u16 字段。
// setU16 writes a 2-byte big-endian value into the template's u16 field at the given octet.
func setU16(def *spec.TemplateDef, buf []byte, octet int, v uint16) error {
	off, err := fieldRange(def, octet, spec.KindU16)
	if err != nil {
		return err
	}
	binary.BigEndian.PutUint16(buf[off:off+2], v)
	return nil
}

// smu16 把值编码为 GRIB2 符号-幅值 uint16：bit 15 为符号位，低 15 位为幅值。
// 这是第 5 节 E/D 字段采用的约定——不是补码（eccodes 把 D=-2 写成 0x8002）。
// 输入必须落在 [-32767, 32767]；范围检查由调用方负责。
//
// smu16 encodes a value as a GRIB2 sign-magnitude uint16: bit 15 = sign, the
// low 15 bits = magnitude. That is the convention used for the E/D fields of
// section 5 — not two's complement (eccodes writes D=-2 as 0x8002). Input must
// be in [-32767, 32767]; the caller is responsible for range-checking first.
func smu16(v int32) uint16 {
	if v < 0 {
		return 0x8000 | uint16(-v)
	}
	return uint16(v)
}

// setF32 把 float32 按 IEEE 754 位模式以大端写入模板中位于指定 octet 的 f32 字段。
// setF32 writes a float32 as its IEEE 754 bit pattern, big-endian, into the template's f32 field at the given octet.
func setF32(def *spec.TemplateDef, buf []byte, octet int, v float32) error {
	off, err := fieldRange(def, octet, spec.KindF32)
	if err != nil {
		return err
	}
	binary.BigEndian.PutUint32(buf[off:off+4], math.Float32bits(v))
	return nil
}

// setU32 把 4 字节大端值写入模板中位于指定 octet 的 u32 字段。
// setU32 writes a 4-byte big-endian value into the template's u32 field at the given octet.
func setU32(def *spec.TemplateDef, buf []byte, octet int, v uint32) error {
	off, err := fieldRange(def, octet, spec.KindU32)
	if err != nil {
		return err
	}
	binary.BigEndian.PutUint32(buf[off:off+4], v)
	return nil
}

// encodeSec1 构造第 1 节（标识节）。其固定部分（octets 5-21）没有 WMO
// 模板定义——内置表只描述了可选的日历模板——所以布局逐 octet 手写，与读取端
// Identification() 的解码方式互为镜像。octet 24 的可选日历类型字节通过日历
// 模板 1.0/1.2（表 1.6）定位。
//
// 关于日期校验的说明：Identification.ReferenceTime 是 time.Time，它总是持有
// 一个已规范化的日历日期——time.Date(2026, 2, 30, …) 在本函数看到之前就已被
// 存成 2026-03-02。因此下面的一致性检查纯属防御性（不可能观察到不存在的
// 日期）；真正有意义的守卫是年份与字段范围检查。
//
// encodeSec1 builds section 1 (identification). Its fixed part (octets 5-21)
// has no WMO template definition — the vendored tables only describe the
// optional calendar template — so the layout is written out octet by octet,
// mirroring how the reader's Identification() decodes it. The optional calendar
// type byte at octet 24 is located via calendar template 1.0/1.2 (table 1.6).
//
// Note on date validation: Identification.ReferenceTime is a time.Time, which
// always holds a normalized calendar date — time.Date(2026, 2, 30, …) is stored
// as 2026-03-02 before this function sees it. The consistency check below is
// therefore purely defensive (it can never observe an impossible date); the
// meaningful guards are the year and field-range checks.
func encodeSec1(id *grib.Identification) ([]byte, error) {
	if id == nil {
		return nil, fmt.Errorf("gribw: section 1: nil identification")
	}
	t := id.ReferenceTime.UTC() // 以 UTC 存储并读回 / stored and read back as UTC
	y, mo, d := t.Year(), int(t.Month()), t.Day()
	h, mi, s := t.Hour(), t.Minute(), t.Second()
	if y < 0 || y > 9999 {
		return nil, fmt.Errorf("gribw: section 1: year %d out of range [0,9999]", y)
	}
	if mo < 1 || mo > 12 {
		return nil, fmt.Errorf("gribw: section 1: month %d out of range [1,12]", mo)
	}
	check := time.Date(y, time.Month(mo), d, h, mi, s, 0, time.UTC)
	if check.Month() != time.Month(mo) || check.Day() != d || check.Hour() != h ||
		check.Minute() != mi || check.Second() != s {
		return nil, fmt.Errorf("gribw: section 1: invalid reference time %04d-%02d-%02d %02d:%02d:%02d", y, mo, d, h, mi, s)
	}
	if id.Centre < 0 || id.Centre > 65535 || id.SubCentre < 0 || id.SubCentre > 65535 {
		return nil, fmt.Errorf("gribw: section 1: centre/subcentre out of range [0,65535]")
	}
	if id.MasterTableVersion < 0 || id.MasterTableVersion > 255 ||
		id.LocalTableVersion < 0 || id.LocalTableVersion > 255 ||
		id.ProductionStatus < 0 || id.ProductionStatus > 255 ||
		id.TypeOfProcessedData < 0 || id.TypeOfProcessedData > 255 {
		return nil, fmt.Errorf("gribw: section 1: version/status field out of range [0,255]")
	}

	size := 21
	if id.Calendar != nil {
		size = 24
	}
	buf := make([]byte, size)
	binary.BigEndian.PutUint32(buf[0:4], uint32(size))
	buf[4] = 1
	binary.BigEndian.PutUint16(buf[5:7], uint16(id.Centre))    // octets 6-7，表 C-1 中心代码 / table C-1 centre code
	binary.BigEndian.PutUint16(buf[7:9], uint16(id.SubCentre)) // 副中心 / subcentre, octets 8-9
	buf[9] = byte(id.MasterTableVersion)                       // 主表版本 / master table version, octet 10, table 1.0
	buf[10] = byte(id.LocalTableVersion)                       // 本地表版本 / local table version, octet 11, table 1.1
	binary.BigEndian.PutUint16(buf[12:14], uint16(y))          // 年 / year, octets 13-14
	buf[14] = byte(mo)                                         // 月 / month, octet 15
	buf[15] = byte(d)                                          // 日 / day, octet 16
	buf[16] = byte(h)                                          // 时 / hour, octet 17
	buf[17] = byte(mi)                                         // 分 / minute, octet 18
	buf[18] = byte(s)                                          // 秒 / second, octet 19
	buf[19] = byte(id.ProductionStatus)                        // 生产状态 / production status, octet 20, table 1.3
	buf[20] = byte(id.TypeOfProcessedData)                     // 处理数据类型 / type of processed data, octet 21, table 1.4

	if id.Calendar != nil {
		c := id.Calendar
		if c.TemplateNumber != 0 && c.TemplateNumber != 2 {
			return nil, fmt.Errorf("gribw: section 1: calendar template %d not writable yet (supported: 0, 2)", c.TemplateNumber)
		}
		binary.BigEndian.PutUint16(buf[21:23], uint16(c.TemplateNumber)) // 日历模板号 / calendar template number, octets 22-23, table 1.5
		def, ok := spec.Template(1, c.TemplateNumber)
		if !ok {
			return nil, fmt.Errorf("gribw: section 1: no spec definition for calendar template %d", c.TemplateNumber)
		}
		if err := setU8(def, buf, 24, uint8(c.TypeOfCalendar)); err != nil { // 日历类型 / type of calendar, octet 24, table 1.6
			return nil, err
		}
	}
	return buf, nil
}

// encodeSec4 从读取端的 Product 类型构造第 4 节（产品定义节）。目前仅支持
// 产品模板 4.0。模板字段偏移取自内置的 4.0 规范定义；节级字段 NV 与模板号
// （octets 6-9）直接写入，正如读取端在遍历之外读取它们那样。octets 13-16
// （时间边界类型、预报时段、时间间隔——超出读取端公共核心的部分）写 0。
//
// encodeSec4 builds section 4 (product definition) from the reader's Product
// type. Only product template 4.0 is supported. Template field offsets come
// from the vendored 4.0 spec definition; the section-level fields NV and the
// template number (octets 6-9) are written directly, just as the reader reads
// them outside the walk. Octets 13-16 (type of time boundary, step range, time
// interval — beyond the reader's common core) are written as 0.
func encodeSec4(p *grib.Product) ([]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("gribw: section 4: nil product")
	}
	if p.TemplateNumber != 0 {
		return nil, fmt.Errorf("gribw: section 4: product template %d not writable yet (supported: 0)", p.TemplateNumber)
	}
	def, ok := spec.Template(4, 0)
	if !ok {
		return nil, fmt.Errorf("gribw: section 4: no spec definition for template 4.0")
	}
	if p.NV != 0 {
		return nil, fmt.Errorf("gribw: section 4: template 4.0 carries no product definition value matrix; numberOfValues must be 0 (the grid value count lives in section 5)")
	}
	if p.ParameterCategory < 0 || p.ParameterCategory > 255 ||
		p.ParameterNumber < 0 || p.ParameterNumber > 255 ||
		p.TypeOfGeneratingProcess < 0 || p.TypeOfGeneratingProcess > 255 {
		return nil, fmt.Errorf("gribw: section 4: category/number/generating-process out of range [0,255]")
	}

	buf := make([]byte, 34) // 模板 4.0 最小长度（最后固定字段止于 octet 34）/ template 4.0 minimum length (last fixed field ends at octet 34)
	binary.BigEndian.PutUint32(buf[0:4], 34)
	buf[4] = 4
	binary.BigEndian.PutUint16(buf[5:7], uint16(p.NV))                      // 附加常数个数 / NV, octets 6-7
	binary.BigEndian.PutUint16(buf[7:9], 0)                                 // 模板号 0 / template number 0, octets 8-9
	if err := setU8(def, buf, 10, uint8(p.ParameterCategory)); err != nil { // 参数类别 / parameter category, table 4.1
		return nil, err
	}
	if err := setU8(def, buf, 11, uint8(p.ParameterNumber)); err != nil { // 参数编号 / parameter number, table 4.2
		return nil, err
	}
	if err := setU8(def, buf, 12, uint8(p.TypeOfGeneratingProcess)); err != nil { // 生成过程类型 / type of generating process, table 4.3
		return nil, err
	}

	if p.ForecastTime == nil {
		return nil, fmt.Errorf("gribw: section 4: template 4.0 requires ForecastTime (pass &grib.ForecastTime{Unit: 1, Value: 0} for analyses)")
	}
	ft := p.ForecastTime
	var unitDur time.Duration
	switch ft.Unit {
	case 0:
		unitDur = time.Minute
	case 1:
		unitDur = time.Hour
	case 2:
		unitDur = 24 * time.Hour
	default:
		return nil, fmt.Errorf("gribw: section 4: forecast time unit %d has no fixed duration; writes units 0 (minute), 1 (hour), 2 (day) only", ft.Unit)
	}
	if ft.Value < 0 || ft.Value%unitDur != 0 {
		return nil, fmt.Errorf("gribw: section 4: forecast time %v is not a whole number of unit %d", ft.Value, ft.Unit)
	}
	count := int64(ft.Value / unitDur)
	if count > math.MaxUint32 {
		return nil, fmt.Errorf("gribw: section 4: forecast time count %d exceeds U32", count)
	}
	if err := setU8(def, buf, 18, uint8(ft.Unit)); err != nil { // 预报时间单位 / forecast time unit, octet 18, table 4.4
		return nil, err
	}
	if err := setU32(def, buf, 19, uint32(count)); err != nil { // 未缩放的原始计数 / raw, unscaled count, octets 19-22
		return nil, err
	}

	if err := encodeLevelAt(def, buf, 23, p.Level1); err != nil { // 类型 @23，比例尺 @24，值 @25-28 / type @23, scale @24, value @25-28
		return nil, err
	}
	if err := encodeLevelAt(def, buf, 29, p.Level2); err != nil { // 类型 @29，比例尺 @30，值 @31-34 / type @29, scale @30, value @31-34
		return nil, err
	}
	return buf, nil
}

// encodeLevelAt 写出一个模板 4.0 固定层次字段：先在指定 octet 写类型字节，
// 接着是比例尺字节，然后是 4 字节缩放值——即读取端 levelFrom 解码的布局。
// nil 层次或无值的层次按缺失写出：值字节全 1（WMO Note I.2 约定）；非 nil
// 但无值的层次保留其类型字节（例如 255 = 缺失）。读取端把这样的层次映射回
// HasValue=false。
//
// encodeLevelAt writes one template 4.0 fixed level: the type at the given
// octet, followed by the scale-factor byte, then a 4-byte scaled value — the
// layout the reader's levelFrom decodes. A nil level, or a level without a
// value, is written as missing: all-ones value bytes (WMO Note I.2
// conventions); a non-nil valueless level keeps its type byte (e.g. 255 =
// missing). The reader maps such a level back to HasValue=false.
func encodeLevelAt(def *spec.TemplateDef, buf []byte, octet int, lv *grib.Level) error {
	typ, scale, raw := uint8(255), uint8(0), uint32(0xFFFFFFFF)
	if lv != nil {
		if lv.Type < 0 || lv.Type > 255 {
			return fmt.Errorf("gribw: section 4: level type %d out of range [0,255]", lv.Type)
		}
		typ = uint8(lv.Type)
		if lv.HasValue {
			s, r, err := encodeScaled(lv.Scale)
			if err != nil {
				return fmt.Errorf("gribw: section 4: level type %d: %w", lv.Type, err)
			}
			scale, raw = byte(s), r
		}
	}
	if err := setU8(def, buf, octet, typ); err != nil {
		return err
	}
	if err := setU8(def, buf, octet+1, scale); err != nil {
		return err
	}
	return setU32(def, buf, octet+2, raw)
}

// pow10f 是 10^0..10^9 的查表，供 encodeScaled 做精确的十进制缩放。
// pow10f is a lookup table of 10^0..10^9 used by encodeScaled for exact decimal scaling.
var pow10f = [10]float64{1, 10, 100, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9}

// encodeScaled 是 WMO 缩放十进制规则在编码侧的对应实现：寻找 s ∈ [0,9]，
// 使 v×10^s 是能装进 U32 的精确非负整数，并优先取最小的 s（值本身已能容纳
// 时不缩放）。按表 4.5，固定层次值为非负；其余情况一律快速失败。
//
// encodeScaled is the encode-side counterpart of the WMO scaled-decimal rule:
// it finds a scale s in [0,9] such that v×10^s is an exact non-negative integer
// that fits in U32, preferring the smallest s (no scaling when the value already
// fits). Fixed-level values are non-negative per table 4.5; anything else
// fails loud.
func encodeScaled(v float64) (int8, uint32, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0, 0, fmt.Errorf("level value %v not representable (need finite non-negative)", v)
	}
	for s := int8(0); s <= 9; s++ {
		f := v * pow10f[s]
		r := math.Round(f)
		if math.Abs(f-r) <= 1e-9*math.Max(1, math.Abs(r)) && r <= math.MaxUint32 {
			return s, uint32(r), nil
		}
	}
	return 0, 0, fmt.Errorf("level value %v not representable with WMO scale 0-9 in U32", v)
}
