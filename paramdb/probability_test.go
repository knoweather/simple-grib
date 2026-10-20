package paramdb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/knoweather/simple-grib"
)

// 合成一条 GRIB2 模板 4.9（概率预报）消息，验证概率键端到端打通：
// Product 解析 probabilityType/上下限 → QueryFrom 提供键 → tpg1 命中。
//
// 模板 4.9 布局（第 4 节模板体，自第 10 八位组）：… 35 预报概率序号
// 36 概率总数 37 Probability type 38 lower SF 39-42 lower SV
// 43 upper SF 44-47 upper SV 48-54 end-of-interval 时间戳
// 55 n 56-59 缺失数 60 统计处理 61-71 时间范围说明。
func buildGRIB2Section49() []byte {
	sec4 := func(num byte, data []byte) []byte {
		s := make([]byte, 5+len(data))
		binary.BigEndian.PutUint32(s[0:4], uint32(len(s)))
		s[4] = num
		copy(s[5:], data)
		return s
	}
	s0 := make([]byte, 16)
	copy(s0[0:4], "GRIB")
	s0[6] = 0 // discipline
	s0[7] = 2 // edition

	s1 := make([]byte, 16)
	binary.BigEndian.PutUint16(s1[0:2], 98) // centre ecmf
	s1[4] = 29                              // master tables version
	s1[5] = 1                               // local tables version
	binary.BigEndian.PutUint16(s1[7:9], 2025)
	s1[9] = 10 // month
	s1[10] = 9 // day

	s3 := make([]byte, 8)
	binary.BigEndian.PutUint16(s3[0:2], 1) // 数据点数
	binary.BigEndian.PutUint16(s3[6:8], 0) // 模板 3.0（占位）

	b := make([]byte, 62) // 模板体：第 10..71 八位组
	b[0] = 1              // 类别 1（湿度）
	b[1] = 52             // 编号 52（Total precipitation）
	b[2] = 2              // 生成过程：预报
	b[18-10] = 1          // 时间单位：小时
	binary.BigEndian.PutUint32(b[19-10:23-10], 6)
	b[23-10] = 1 // 第一固定面：地表
	b[24-10] = 0xFF
	binary.BigEndian.PutUint32(b[25-10:29-10], 0xFFFFFFFF)
	b[29-10] = 255 // 第二固定面：missing
	b[30-10] = 0xFF
	binary.BigEndian.PutUint32(b[31-10:35-10], 0xFFFFFFFF)
	b[35-10] = 1                                  // 预报概率序号
	b[36-10] = 3                                  // 概率总数
	b[37-10] = 3                                  // Probability type：区间内
	b[38-10] = 0                                  // lower scale factor
	binary.BigEndian.PutUint32(b[39-10:43-10], 1) // lower scaled value = 1 mm
	b[43-10] = 0                                  // upper scale factor
	binary.BigEndian.PutUint32(b[44-10:48-10], 5) // upper scaled value = 5 mm
	binary.BigEndian.PutUint16(b[48-10:50-10], 2025)
	b[50-10] = 10 // 月
	b[51-10] = 9  // 日
	b[52-10] = 6  // 时
	b[55-10] = 1  // n = 1
	b[60-10] = 1  // 统计处理：累积
	b[61-10] = 1  // 时间增量类型
	b[62-10] = 1  // 区间长度单位：小时
	binary.BigEndian.PutUint32(b[63-10:67-10], 6)
	b[67-10] = 1 // 增量单位：小时
	binary.BigEndian.PutUint32(b[68-10:72-10], 0)

	msg := make([]byte, 0, 16+21+13+71+5+4)
	msg = append(msg, s0...)
	msg = append(msg, sec4(1, s1)...)
	msg = append(msg, sec4(3, s3)...)
	msg = append(msg, sec4(4, func() []byte {
		hdr := make([]byte, 4)
		binary.BigEndian.PutUint16(hdr[0:2], 1) // NV
		binary.BigEndian.PutUint16(hdr[2:4], 9) // 模板 4.9
		return append(hdr, b...)
	}())...)
	msg = append(msg, sec4(7, nil)...)
	msg = append(msg, "7777"...)
	binary.BigEndian.PutUint64(msg[8:16], uint64(len(msg))) // total length
	return msg
}

// buildGRIB2ProbabilityParametrized builds a section-4 template 4.112 or
// 4.123 message with NT=2 time ranges, so the probability block sits at
// 61+(NT-1)*12 / 66+(NT-1)*12 instead of the NT=1 position. Everything up to
// the probability block follows the WMO layout; the trailing reference-period
// / focal-statistics fields are omitted because Product never reads them.
func buildGRIB2ProbabilityParametrized(tmpl uint16) []byte {
	sec := func(num byte, data []byte) []byte {
		s := make([]byte, 5+len(data))
		binary.BigEndian.PutUint32(s[0:4], uint32(len(s)))
		s[4] = num
		copy(s[5:], data)
		return s
	}
	s0 := make([]byte, 16)
	copy(s0[0:4], "GRIB")
	s0[6] = 0 // discipline
	s0[7] = 2 // edition

	s1 := make([]byte, 16)
	binary.BigEndian.PutUint16(s1[0:2], 98) // centre ecmf
	s1[4] = 29                              // master tables version
	s1[5] = 1                               // local tables version
	binary.BigEndian.PutUint16(s1[7:9], 2025)
	s1[9] = 10 // month
	s1[10] = 9 // day

	s3 := make([]byte, 8)
	binary.BigEndian.PutUint16(s3[0:2], 1) // 数据点数
	binary.BigEndian.PutUint16(s3[6:8], 0) // 模板 3.0（占位）

	const nt = 2
	// 模板体：八位组 10 起。4.112 的概率块在 61+(NT-1)*12，4.123 在
	// 66+(NT-1)*12；两者之前都有 NT 个 12 字节的时间范围说明。
	probTypeOctet := 61
	if tmpl == 123 {
		probTypeOctet = 66
	}
	probOff := probTypeOctet - 10 + (nt-1)*12
	b := make([]byte, probOff+11)
	b[0] = 1 // 类别 1（湿度）
	b[1] = 52
	b[2] = 2 // 生成过程：预报
	b[8] = 1 // 时间单位：小时（八位组 18）
	binary.BigEndian.PutUint32(b[9:13], 6)
	b[13] = 1 // 第一固定面：地表（八位组 23）
	b[14] = 0xFF
	binary.BigEndian.PutUint32(b[15:19], 0xFFFFFFFF)
	b[19] = 255 // 第二固定面：missing（八位组 29）
	b[20] = 0xFF
	binary.BigEndian.PutUint32(b[21:25], 0xFFFFFFFF)
	binary.BigEndian.PutUint16(b[25:27], 2025) // end-of-interval（八位组 35）
	b[27] = 10                                 // 月
	b[28] = 9                                  // 日
	b[29] = 6                                  // 时
	b[32] = nt                                 // 八位组 42：时间范围数
	for i := 0; i < nt; i++ {                  // 每个时间范围 12 字节
		o := 37 + i*12 // 八位组 47
		b[o] = 1       // 统计处理：累积
		b[o+1] = 1     // 时间增量类型
		b[o+2] = 1     // 区间长度单位：小时
		binary.BigEndian.PutUint32(b[o+3:o+7], 6)
		b[o+7] = 1 // 增量单位：小时
	}
	if tmpl == 123 {
		b[probOff-7] = 1 // typeOfEnsembleForecast（八位组 71）
		binary.BigEndian.PutUint32(b[probOff-6:probOff-2], 50)
	}
	// forecastProbabilityNumber / totalNumber 紧挨 Probability type 之前。
	b[probOff-2] = 1
	b[probOff-1] = 3
	b[probOff] = 3 // Probability type：区间内
	b[probOff+1] = 0
	binary.BigEndian.PutUint32(b[probOff+2:probOff+6], 1)
	b[probOff+6] = 0
	binary.BigEndian.PutUint32(b[probOff+7:probOff+11], 5)

	msg := make([]byte, 0, 16+21+13+9+len(b)+5+4)
	msg = append(msg, s0...)
	msg = append(msg, sec(1, s1)...)
	msg = append(msg, sec(3, s3)...)
	msg = append(msg, sec(4, func() []byte {
		hdr := make([]byte, 4)
		binary.BigEndian.PutUint16(hdr[0:2], 1) // NV
		binary.BigEndian.PutUint16(hdr[2:4], tmpl)
		return append(hdr, b...)
	}())...)
	msg = append(msg, sec(7, nil)...)
	msg = append(msg, "7777"...)
	binary.BigEndian.PutUint64(msg[8:16], uint64(len(msg)))
	return msg
}

func TestProbabilityParametrizedTemplates(t *testing.T) {
	for _, tmpl := range []uint16{112, 123} {
		t.Run(fmt.Sprint(tmpl), func(t *testing.T) {
			raw := buildGRIB2ProbabilityParametrized(tmpl)
			f, err := grib.OpenReader(bytes.NewReader(raw), int64(len(raw)))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer f.Close()
			m, err := f.Message(0)
			if err != nil {
				t.Fatalf("Message(0): %v", err)
			}
			p, err := m.Product()
			if err != nil {
				t.Fatal(err)
			}
			if p.TemplateNumber != int(tmpl) {
				t.Fatalf("TemplateNumber = %d, want %d", p.TemplateNumber, tmpl)
			}
			if p.ProbabilityType != 3 {
				t.Errorf("ProbabilityType = %d, want 3 (within intervals)", p.ProbabilityType)
			}
			if p.ProbabilityLower == nil || !p.ProbabilityLower.HasValue ||
				p.ProbabilityLower.ScaledValueRaw != 1 {
				t.Errorf("ProbabilityLower = %+v, want raw (0,1)", p.ProbabilityLower)
			}
			if p.ProbabilityUpper == nil || !p.ProbabilityUpper.HasValue ||
				p.ProbabilityUpper.ScaledValueRaw != 5 {
				t.Errorf("ProbabilityUpper = %+v, want raw (0,5)", p.ProbabilityUpper)
			}
			if p.TypeOfStatisticalProcessing != 1 {
				t.Errorf("TypeOfStatisticalProcessing = %d, want 1 (accumulation)", p.TypeOfStatisticalProcessing)
			}
			if p.EndOfOverallInterval == nil ||
				!p.EndOfOverallInterval.Equal(time.Date(2025, 10, 9, 6, 0, 0, 0, time.UTC)) {
				t.Errorf("EndOfOverallInterval = %v, want 2025-10-09T06:00:00Z", p.EndOfOverallInterval)
			}

			q, err := QueryFrom(m)
			if err != nil {
				t.Fatal(err)
			}
			if sn, ok := ShortName(q); !ok || sn != "tpg1" {
				t.Errorf("ShortName = %q,%v; want tpg1,true", sn, ok)
			}
			if id, ok := ParamID(q); !ok || id != 131060 {
				t.Errorf("ParamID = %d,%v; want 131060,true", id, ok)
			}
		})
	}
}

func TestProbabilityTotalPrecipAtLeast1mm(t *testing.T) {
	raw := buildGRIB2Section49()
	f, err := grib.OpenReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	if n, nerr := f.NumMessages(); nerr != nil || n != 1 {
		t.Fatalf("NumMessages = %d,%v; want 1", n, nerr)
	}
	m, err := f.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}
	p, err := m.Product()
	if err != nil {
		t.Fatal(err)
	}
	if p.ProbabilityType != 3 {
		t.Errorf("ProbabilityType = %d, want 3 (within intervals)", p.ProbabilityType)
	}
	if p.ProbabilityLower == nil || !p.ProbabilityLower.HasValue ||
		p.ProbabilityLower.ScaleFactorRaw != 0 || p.ProbabilityLower.ScaledValueRaw != 1 {
		t.Errorf("ProbabilityLower = %+v, want raw (0,1)", p.ProbabilityLower)
	}
	if p.ProbabilityUpper == nil || !p.ProbabilityUpper.HasValue ||
		p.ProbabilityUpper.ScaledValueRaw != 5 {
		t.Errorf("ProbabilityUpper = %+v, want raw (0,5)", p.ProbabilityUpper)
	}
	if p.TypeOfStatisticalProcessing != 1 {
		t.Errorf("TypeOfStatisticalProcessing = %d, want 1 (accumulation)", p.TypeOfStatisticalProcessing)
	}

	q, err := QueryFrom(m)
	if err != nil {
		t.Fatal(err)
	}
	if sn, ok := ShortName(q); !ok || sn != "tpg1" {
		t.Errorf("ShortName = %q,%v; want tpg1,true", sn, ok)
	}
	if name, ok := Name(q); !ok || name != "Total precipitation of at least 1 mm" {
		t.Errorf("Name = %q,%v; want Total precipitation of at least 1 mm,true", name, ok)
	}
	if pt, ok := TypeOfLevel(q); !ok || pt != "surface" {
		t.Errorf("TypeOfLevel = %q,%v; want surface,true", pt, ok)
	}
	// paramId：tpg1 在 ECMWF 参数库的编号（参数级键不在 lte33 概念里时，
	// 这里是 master paramId.def 的 best-match 结果）。
	if id, ok := ParamID(q); !ok || id != 131060 {
		t.Errorf("ParamID = %d,%v; want 131060,true", id, ok)
	}
	// 单位：概率参数的单位是 %。
	if u, ok := Units(q); !ok || u != "%" {
		t.Errorf("Units = %q,%v; want %%,true", u, ok)
	}
	// 生效时间：本消息 forecastTime=6h，end-of-interval 也是 +6h →
	// begin=end=2025-10-09T06Z，step=6h（累积区间 [6h,6h]，消息自洽）。
	begin, end, ok := m.StepRange()
	if !ok {
		t.Fatal("StepRange: not ok")
	}
	want := time.Date(2025, 10, 9, 6, 0, 0, 0, time.UTC)
	if !begin.Equal(want) || !end.Equal(want) {
		t.Errorf("begin/end = %s/%s, want both %s", begin, end, want)
	}
	if step, ok := m.Step(); !ok || step != 6*time.Hour {
		t.Errorf("Step = %v,%v; want 6h,true", step, ok)
	}
}
