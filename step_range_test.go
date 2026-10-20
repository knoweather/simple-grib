package grib

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/knoweather/simple-grib/grib1"
)

// ------------------------------------------------------------- 合成 GRIB2 消息
//
// 真实语料里没有 4.8 统计模板的样本（ecCodes 侧通常用 6h 累积的 tp，那是
// ECMWF 私有本地表），因此这里按 WMO 模板表手搓一条消息：第 0/1/3/4/7 节 +
// "7777" 尾。第 3 节只做占位（Product 不读它），真正的断言全在第 4 节。
//
// buildGRIB2Section4 组装第 4 节里的模板体（从第 10 八位组起）：
// [Octet 10] 类别 [11] 编号 [12] 生成过程 [18] 时间单位 [19-22] 预报时效
// [23] 第一固定面类型 … [35-42] end-of-overall-interval 时间戳。
func sec4Body(forecastTime uint32, timeUnit uint8, endYMDHMS [6]uint16, hasEnd bool) []byte {
	b := make([]byte, 49) // 第 4 节模板体：第 10..58 八位组 → b[0..48]
	b[0] = 0              // 第 10 八位组 parameter category 0 (temperature)
	b[1] = 0              // 第 11 八位组 parameter number 0    (Temperature)
	b[2] = 2              // 第 12 八位组 type of generating process: forecast
	b[18-10] = timeUnit   // 第 18 八位组 indicator of unit of time range
	binary.BigEndian.PutUint32(b[19-10:23-10], forecastTime)
	b[23-10] = 1 // 第 23 八位组 first fixed surface: ground or water surface
	if hasEnd {
		// end-of-overall-interval 时间戳：第 35-42 八位组。
		binary.BigEndian.PutUint16(b[35-10:37-10], endYMDHMS[0]) // 年
		b[37-10] = byte(endYMDHMS[1])                            // 月
		b[38-10] = byte(endYMDHMS[2])                            // 日
		b[39-10] = byte(endYMDHMS[3])                            // 时
		b[40-10] = byte(endYMDHMS[4])                            // 分
		b[41-10] = byte(endYMDHMS[5])                            // 秒
	}
	return b
}

func putU64(data []byte, off int, v uint64) {
	binary.BigEndian.PutUint64(data[off:], v)
}

func section(num byte, data []byte) []byte {
	s := make([]byte, 5+len(data))
	binary.BigEndian.PutUint32(s[0:4], uint32(len(s)))
	s[4] = num
	copy(s[5:], data)
	return s
}

// buildGRIB2 组装一条可解析的 GRIB2 消息。section4Body 是第 4 节的模板体
// （自第 10 八位组起），tmpl 是第 4 节模板号。
func buildGRIB2(t *testing.T, tmpl int, body []byte) []byte {
	t.Helper()
	// 第 0 节：indicator（16 字节）。
	s0 := make([]byte, 16)
	copy(s0[0:4], "GRIB")
	s0[5] = 0 // reserved
	s0[6] = 0 // discipline 0
	s0[7] = 2 // edition 2

	// 第 1 节：identification，21 字节（无日历段）。
	s1 := make([]byte, 16)
	binary.BigEndian.PutUint16(s1[0:2], 98) // centre: ECMWF
	s1[4] = 29                              // master table version
	s1[5] = 1                               // local table version
	binary.BigEndian.PutUint16(s1[7:9], 2025)
	s1[9] = 10 // month
	s1[10] = 9 // day
	s1[11] = 0 // hour
	s1[12] = 0 // minute
	s1[13] = 0 // second
	s1[14] = 0 // production status
	s1[15] = 1 // type of processed data: forecast

	// 第 3 节：占位（模板号 0，非 0 也无妨：本测试不解析网格）。
	s3 := make([]byte, 8)
	binary.BigEndian.PutUint16(s3[0:2], 1) // number of data points
	binary.BigEndian.PutUint16(s3[6:8], 0) // template 3.0

	// 第 4 节：NV + 模板号 + 模板体。
	s4 := make([]byte, 4+len(body))
	binary.BigEndian.PutUint16(s4[0:2], 1) // NV = 1
	binary.BigEndian.PutUint16(s4[2:4], uint16(tmpl))
	copy(s4[4:], body)

	msg := make([]byte, 0, 16+len(s1)+len(s3)+len(s4)+5+4)
	msg = append(msg, s0...)
	msg = append(msg, section(1, s1)...)
	msg = append(msg, section(3, s3)...)
	msg = append(msg, section(4, s4)...)
	msg = append(msg, section(7, nil)...)
	msg = append(msg, "7777"...)
	putU64(msg, 8, uint64(len(msg))) // 第 0 节 9-16 八位组：total length（8 字节）
	return msg
}

func parseGRIB2(t *testing.T, raw []byte) *Message {
	t.Helper()
	m, err := newMessage(0, 0, raw)
	if err != nil {
		t.Fatalf("newMessage: %v", err)
	}
	return m
}

const refTime = "2025-10-09T00:00:00Z"

// 瞬时模板（4.0）：区间退化为一个点，step=0，end==begin。
func TestStepRangeInstantaneous(t *testing.T) {
	m := parseGRIB2(t, buildGRIB2(t, 0, sec4Body(6, 1, [6]uint16{}, false)))
	b, e, ok := m.StepRange()
	if !ok {
		t.Fatal("StepRange: not ok on a template-4.0 message")
	}
	want := time.Date(2025, 10, 9, 6, 0, 0, 0, time.UTC)
	if !b.Equal(want) {
		t.Errorf("begin = %s, want %s", b, want)
	}
	if !b.Equal(e) {
		t.Errorf("instantaneous message: end = %s, want begin %s", e, b)
	}
	if step, ok := m.Step(); !ok || step != 6*time.Hour {
		t.Errorf("Step = %v,%v; want 6h,true", step, ok)
	}
	p, err := m.Product()
	if err != nil {
		t.Fatal(err)
	}
	if p.EndOfOverallInterval != nil {
		t.Errorf("EndOfOverallInterval = %v, want nil for a non-statistical template", *p.EndOfOverallInterval)
	}
	if p.ParameterName != "Temperature" {
		t.Errorf("ParameterName = %q, want Temperature", p.ParameterName)
	}
	if p.ProbabilityType != -1 {
		t.Errorf("ProbabilityType = %d, want -1 for a template without probability fields", p.ProbabilityType)
	}
}

// 统计模板（4.8）：forecastTime=0 + end-of-interval = 参考时间+6h，正是
// ecCodes 在 6h 累积产品上的 forecastTime=0 / step=6 / validity=+6h。
func TestStepRangeAccumulation6h(t *testing.T) {
	body := sec4Body(0, 1, [6]uint16{2025, 10, 9, 6, 0, 0}, true)
	m := parseGRIB2(t, buildGRIB2(t, 8, body))

	b, e, ok := m.StepRange()
	if !ok {
		t.Fatal("StepRange: not ok")
	}
	if want := time.Date(2025, 10, 9, 0, 0, 0, 0, time.UTC); !b.Equal(want) {
		t.Errorf("begin = %s, want %s", b, want)
	}
	wantEnd := time.Date(2025, 10, 9, 6, 0, 0, 0, time.UTC)
	if !e.Equal(wantEnd) {
		t.Errorf("end = %s, want %s", e, wantEnd)
	}
	if step, ok := m.Step(); !ok || step != 6*time.Hour {
		t.Errorf("Step = %v,%v; want 6h,true — this is the regression where a 6h accumulation reads step=0", step, ok)
	}
	p, err := m.Product()
	if err != nil {
		t.Fatal(err)
	}
	if p.EndOfOverallInterval == nil || !p.EndOfOverallInterval.Equal(wantEnd) {
		t.Fatalf("EndOfOverallInterval = %v, want %s", p.EndOfOverallInterval, wantEnd)
	}
	if p.ForecastTime == nil || p.ForecastTime.Value != 0 {
		t.Errorf("ForecastTime.Value = %v, want 0 (interval start)", p.ForecastTime)
	}
}

// 时间戳里的字段全都非法（month=13）时 end 回退成 begin。另一个方向：
// month=0 这类"空模板体"同样不算，见 spec 包里的偏移/长度性质测试。
func TestStepRangeInvalidEndInterval(t *testing.T) {
	body := sec4Body(0, 1, [6]uint16{2025, 13, 9, 6, 0, 0}, true)
	m := parseGRIB2(t, buildGRIB2(t, 8, body))
	p, err := m.Product()
	if err != nil {
		t.Fatal(err)
	}
	if p.EndOfOverallInterval != nil {
		t.Fatalf("EndOfOverallInterval = %v, want nil for an impossible date", *p.EndOfOverallInterval)
	}
	b, e, ok := m.StepRange()
	if !ok {
		t.Fatal("StepRange: not ok")
	}
	if !b.Equal(e) {
		t.Errorf("end = %s, want begin %s (no fabricated interval)", e, b)
	}
}

// 月/年单位的时间字段：无法换算，StepRange 明确 not-ok（与
// Product.ForecastTime 的 Unit>2 规则一致）。
func TestStepRangeMonthUnitNotConvertible(t *testing.T) {
	body := sec4Body(1, 3, [6]uint16{}, false)
	body[19-10] = 3 // unit of time range: month
	m := parseGRIB2(t, buildGRIB2(t, 8, body))
	if _, _, ok := m.StepRange(); ok {
		t.Error("StepRange ok with a month time unit; want false (no fixed duration)")
	}
	if _, ok := m.Step(); ok {
		t.Error("Step ok with a month time unit; want false")
	}
}

// GRIB1（ECMWF 3h 步长的真实消息）：区间按 WMO 表 5 语义从 P1/P2 解出，
// 且与 grib1 包自身的 StepRange 一致。
func TestStepRangeGRIB1RealFile(t *testing.T) {
	f, err := Open("testdata/step-range/g1_ecmwf_3h.grib1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	if n, err := f.NumMessages(); err != nil || n != 1 {
		t.Fatalf("NumMessages = %d,%v; want 1", n, err)
	}
	m, err := f.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}
	if m.Edition() != 1 {
		t.Fatalf("Edition = %d, want 1", m.Edition())
	}

	g1msg, _ := m.GRIB1()
	g1 := g1msg.Product()
	b0, e0, ok := g1.StepRange()
	if !ok {
		t.Fatal("grib1 StepRange: not ok")
	}
	id, err := m.Identification()
	if err != nil {
		t.Fatal(err)
	}
	// 2025-12-29 + 336h（P1=112 × 3h）= 2026-01-12；指示符 3 的平均区间
	// 终点 = +504h（P2=168 × 3h）= 2026-01-19。
	wantBegin := time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC)
	if got := id.ReferenceTime.Add(b0); !got.Equal(wantBegin) {
		t.Errorf("begin = %s, want %s", got, wantBegin)
	}

	b, e, ok := m.StepRange()
	if !ok {
		t.Fatal("StepRange: not ok")
	}
	if got := id.ReferenceTime.Add(b0); !b.Equal(got) {
		t.Errorf("root begin = %s, want %s", b, got)
	}
	if !e.Equal(id.ReferenceTime.Add(e0)) {
		t.Errorf("root end = %s, want %s", e, id.ReferenceTime.Add(e0))
	}
	t.Logf("GRIB1: indicator=%d unit=%d P1=%d P2=%d begin=%s end=%s step=%v",
		g1.TimeRangeIndicator, g1.TimeRangeUnit, g1.P1, g1.P2,
		b.Format(time.RFC3339), e.Format(time.RFC3339), e.Sub(b))
}

// grib1 表 5 语义的定向用例：单位 1（小时）+ 指示符 4（累积 P1→P2）。
func TestStepRangeGRIB1Indicator4(t *testing.T) {
	p := &grib1.Product{
		TimeRangeUnit:      1, // hour
		TimeRangeIndicator: 4, // accumulation (ref+P1 .. ref+P2)
		P1:                 6,
		P2:                 12,
	}
	b, e, ok := p.StepRange()
	if !ok || b != 6*time.Hour || e != 12*time.Hour {
		t.Errorf("StepRange = %v,%v,%v; want 6h,12h,true", b, e, ok)
	}
	// 时间单位为月时不可换算。
	pM := &grib1.Product{TimeRangeUnit: 3, TimeRangeIndicator: 4, P1: 6, P2: 12}
	if _, _, ok := pM.StepRange(); ok {
		t.Error("StepRange ok with a month unit; want false")
	}
	// 指示符 6：reference-P1 .. reference-P2，两端都是负偏移。
	p6 := &grib1.Product{TimeRangeUnit: 1, TimeRangeIndicator: 6, P1: 3, P2: 1}
	b6, e6, _ := p6.StepRange()
	if b6 != -3*time.Hour || e6 != -time.Hour {
		t.Errorf("indicator 6: StepRange = %v,%v; want -3h,-1h", b6, e6)
	}
	// 指示符 0（瞬时）：begin == end == P1。
	p0 := &grib1.Product{TimeRangeUnit: 1, TimeRangeIndicator: 0, P1: 9, P2: 0}
	b0, e0, _ := p0.StepRange()
	if b0 != 9*time.Hour || e0 != 9*time.Hour {
		t.Errorf("indicator 0: StepRange = %v,%v; want 9h,9h", b0, e0)
	}
	// 指示符 10：WMO 表 5 规定 P1 占 octets 19-20（两字节）。P1=6、P2=12
	// 即 0x060C=1548 个时间单位；ecCodes 对同样的消息给出 1548*unit。
	p10 := &grib1.Product{TimeRangeUnit: 10, TimeRangeIndicator: 10, P1: 6, P2: 12} // 3h
	b10, e10, ok10 := p10.StepRange()
	if want := time.Duration(1548) * 3 * time.Hour; !ok10 || b10 != want || e10 != want {
		t.Errorf("indicator 10: StepRange = %v,%v,%v; want %v,%v,true", b10, e10, ok10, want, want)
	}
	if ft, ok := p10.ForecastTime(); !ok || ft != 1548*3*time.Hour {
		t.Errorf("indicator 10: ForecastTime = %v,%v; want 4644h,true", ft, ok)
	}
}
