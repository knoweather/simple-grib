package spec

import "testing"

// 单位来自 CSV 的 UnitComments_en 列原文（WMO 拼写）。这里逐条钉住
// ParameterUnit / LevelUnit 的查找与回退语义：层级表优先、扁表回退、
// 无量纲归一成空但不算查不到。
func TestParameterUnitKnownParameters(t *testing.T) {
	cases := []struct {
		discipline, category, number int
		want                         string
	}{
		{0, 0, 0, "K"},      // Temperature
		{0, 0, 6, "K"},      // Dewpoint temperature
		{0, 1, 0, "kg/kg"},  // Specific humidity
		{0, 1, 1, "%"},      // Relative humidity
		{0, 1, 3, "kg m-2"}, // Precipitable water
		{0, 2, 1, "m/s"},    // Wind speed（WMO 表内是 "m/s" 而非 "m s-1"）
		{0, 3, 0, "Pa"},     // Pressure
		{0, 4, 0, "W m-2"},  // Net short-wave radiation flux (surface)
		{0, 4, 4, "K"},      // Brightness temperature（辐射类里也有 K）
		{0, 5, 3, "W m-2"},  // Long-wave radiation flux 一类
	}
	for _, c := range cases {
		got, err := ParameterUnit(c.discipline, c.category, c.number)
		if err != nil {
			t.Fatalf("ParameterUnit(%d,%d,%d): unexpected error: %v", c.discipline, c.category, c.number, err)
		}
		if got != c.want {
			t.Errorf("ParameterUnit(%d,%d,%d) = %q, want %q", c.discipline, c.category, c.number, got, c.want)
		}
	}
}

// 无量纲/未声明单位的参数：错误必须为 nil（表里有这一行），单位为空串。
func TestParameterUnitDimensionlessHasNoError(t *testing.T) {
	for _, code := range []struct{ d, c, n int }{
		{0, 0, 0 + 33}, // Reserved（4.2.0.0 的 33 号 = Reserved）
	} {
		got, err := ParameterUnit(code.d, code.c, code.n)
		if err != nil {
			t.Fatalf("ParameterUnit(%d,%d,%d): unexpected error: %v", code.d, code.c, code.n, err)
		}
		if got != "" {
			t.Errorf("ParameterUnit(%d,%d,%d) = %q, want empty (dimensionless)", code.d, code.c, code.n, got)
		}
	}
	// 本地/未定义的 (discipline,category) 组合才是真正的"表里没有"。
	if _, err := ParameterUnit(250, 250, 1); err == nil {
		t.Errorf("ParameterUnit(250,250,1): expected error for a non-existent parameter table")
	}
}

func TestLevelUnit(t *testing.T) {
	cases := []struct {
		levelType int
		want      string
		ok        bool
	}{
		{100, "Pa", true}, // Isobaric surface
		{103, "m", true},  // Specified height level above ground
		{1, "", false},    // Ground or water surface（WMO 写作 "-"）
		{105, "", false},  // Hybrid level（"-"）
		{200, "", false},  // Atmosphere（"-"）
		{244, "", false},  // unrecognised code
	}
	for _, c := range cases {
		got, ok := LevelUnit(c.levelType)
		if got != c.want || ok != c.ok {
			t.Errorf("LevelUnit(%d) = %q,%v; want %q,%v", c.levelType, got, ok, c.want, c.ok)
		}
	}
}

// Entry.Unit 必须与查表 API 一致；区间的保留段（192-254 "Reserved for local
// use"）也要按区间命中，证明 [Min,Max] 语义没有因为单位列而改变。
func TestEntryUnitConsistency(t *testing.T) {
	tbl, ok := TableByID("4.2.0.0")
	if !ok {
		t.Fatal("table 4.2.0.0 missing")
	}
	e, ok := tbl.Lookup(0)
	if !ok {
		t.Fatal("entry 0 of 4.2.0.0 missing")
	}
	if e.Name != "Temperature" || e.Unit != "K" {
		t.Errorf("entry 0 = %+v, want Temperature/K", e)
	}
	// 区间语义不变：保留段按 [Min,Max] 命中，且不带单位。
	for _, code := range []int{192, 254, 250} {
		e, ok := tbl.Lookup(code)
		if !ok {
			t.Fatalf("code %d should match the 192-254 range entry", code)
		}
		if e.Name != "Reserved for local use" || e.Unit != "" {
			t.Errorf("code %d matched %+v, want Reserved for local use / no unit", code, e)
		}
	}
}

// EndIntervalOffset 只对带固定 end-of-interval 位置的模板返回 ok；参数化
// 位置（4.34/4.114 等）必须明确返回 not-ok，而不是猜偏移。
func TestEndIntervalOffset(t *testing.T) {
	if off, ok := EndIntervalOffset("4.8"); !ok || off != 34 {
		t.Errorf(`EndIntervalOffset("4.8") = %d,%v; want 34,true`, off, ok)
	}
	if off, ok := EndIntervalOffset("4.9"); !ok || off != 47 {
		t.Errorf(`EndIntervalOffset("4.9") = %d,%v; want 47,true`, off, ok)
	}
	for _, id := range []string{"4.114", "4.34", "4.0", "4.1", "9.9", ""} {
		if off, ok := EndIntervalOffset(id); ok {
			t.Errorf("EndIntervalOffset(%q) = %d,true; want _,false", id, off)
		}
	}
	// 参数化位置可被单独识别：字段存在、偏移不可静。
	for _, id := range []string{"4.114", "4.34"} {
		if !EndIntervalParametrized(id) {
			t.Errorf("EndIntervalParametrized(%q) = false, want true", id)
		}
	}
	if EndIntervalParametrized("4.8") {
		t.Errorf(`EndIntervalParametrized("4.8") = true, want false`)
	}
}

// 性质测试：任何一个固定偏移都必须是模板自身固定字段区间内可读的一段
// （偏移 + 8 字节不超过最后一个固定字段的结尾）。否则解析侧要么越界、
// 要么读到的不是时间戳。参数化条目（-1）不在本性质内，由
// EndIntervalOffset/Parametrized 分别表达。
func TestEndIntervalOffsetWithinTemplateSpan(t *testing.T) {
	const endIntervalLen = 8 // 2 字节年 + 月/日/时/分/秒 各 1 字节
	checked := 0
	for _, gt := range genTemplates {
		off, ok := EndIntervalOffset(gt.id)
		if !ok {
			if EndIntervalParametrized(gt.id) {
				checked++
			}
			continue
		}
		if off < 0 {
			t.Errorf("%s: negative offset %d with ok=true", gt.id, off)
			continue
		}
		end := -1
		for _, f := range gt.fields {
			if f.kind == kindSymbolic || f.start < 0 || f.count <= 0 {
				continue
			}
			if e := int(f.start) + int(f.count) - 1; e > end {
				end = e
			}
		}
		if end < 0 {
			t.Errorf("%s: offset %d but no fixed fields at all", gt.id, off)
			continue
		}
		if off+endIntervalLen-1 > end {
			t.Errorf("%s: end-of-interval at offset %d..%d exceeds last fixed field end %d",
				gt.id, off, off+endIntervalLen-1, end)
			continue
		}
		checked++
	}
	if checked < 7 {
		t.Errorf("only %d end-of-interval templates checked; want at least the 4.8-4.14 family", checked)
	}
}

// 4.112/4.123 的概率块跟着 statisticalProcessesList 平移：Probability type
// 位于八位组 61+(NT-1)*12 / 66+(NT-1)*12。NT 是第 4 节八位组 42。
func TestProbTypeOffsetParametrized(t *testing.T) {
	data := make([]byte, 200)
	data[41] = 1
	if off, ok := ProbTypeOffsetParametrized("4.112", data); !ok || off != 60 {
		t.Errorf(`ProbTypeOffsetParametrized("4.112", NT=1) = %d,%v; want 60,true`, off, ok)
	}
	if off, ok := ProbTypeOffsetParametrized("4.123", data); !ok || off != 65 {
		t.Errorf(`ProbTypeOffsetParametrized("4.123", NT=1) = %d,%v; want 65,true`, off, ok)
	}

	data[41] = 2
	if off, ok := ProbTypeOffsetParametrized("4.112", data); !ok || off != 72 {
		t.Errorf(`ProbTypeOffsetParametrized("4.112", NT=2) = %d,%v; want 72,true`, off, ok)
	}
	if off, ok := ProbTypeOffsetParametrized("4.123", data); !ok || off != 77 {
		t.Errorf(`ProbTypeOffsetParametrized("4.123", NT=2) = %d,%v; want 77,true`, off, ok)
	}

	// 只有这两个模板是参数化概率块。
	if off, ok := ProbTypeOffsetParametrized("4.9", data); ok {
		t.Errorf(`ProbTypeOffsetParametrized("4.9") = %d,true; want _,false`, off)
	}
	// NT 缺失/为 0、数据截断、计算的字段越界都不能猜。
	if _, ok := ProbTypeOffsetParametrized("4.112", data[:41]); ok {
		t.Error("ProbTypeOffsetParametrized accepted a section without octet 42")
	}
	data[41] = 0
	if _, ok := ProbTypeOffsetParametrized("4.112", data); ok {
		t.Error("ProbTypeOffsetParametrized accepted NT=0")
	}
	data[41] = 3
	if _, ok := ProbTypeOffsetParametrized("4.112", data[:84]); ok {
		t.Error("ProbTypeOffsetParametrized accepted a truncated probability block")
	}
}
