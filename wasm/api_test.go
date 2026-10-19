//go:build js && wasm

package main

import (
	"math"
	"os"
	"path/filepath"
	"syscall/js"
	"testing"
)

// fixture 读取仓库里的 3x3 正角点样本（188 字节，ECMWF eccodes 产）。
func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "ecc-xcheck", "poscorners.grib2"))
	if err != nil {
		t.Fatalf("读样本失败: %v", err)
	}
	return b
}

// TestDecodeBytesFull 覆盖完整解码路径：元数据 + 数值 + 坐标。
func TestDecodeBytesFull(t *testing.T) {
	res := decodeBytes(fixture(t), Options{Values: true, Points: true})

	if !res.OK {
		t.Fatalf("OK=false, errors=%v", res.Errors)
	}
	if res.NumMessages != 1 || len(res.Messages) != 1 {
		t.Fatalf("消息数 = %d/%d，想要 1/1", res.NumMessages, len(res.Messages))
	}
	m := res.Messages[0]
	if len(m.Errors) != 0 {
		t.Fatalf("单条消息报错: %v", m.Errors)
	}
	if got, want := len(m.Sections), 7; got != want {
		t.Errorf("sections 长度 = %d，想要 %d（%v）", got, want, m.Sections)
	}
	if len(m.SectionSizes) != len(m.Sections) {
		t.Errorf("sectionSizes 长度 = %d，想要 %d", len(m.SectionSizes), len(m.Sections))
	}
	if m.DisciplineName == "" {
		t.Error("disciplineName 没查出来（表 0.0）")
	}
	if m.Identification == nil {
		t.Fatal("identification 缺失")
	}
	if m.Identification.CentreName == "" || m.Identification.ReferenceTime == "" {
		t.Errorf("identification 关键字段为空: %+v", m.Identification)
	}
	if m.Identification.ProductionStatusName == "" {
		t.Error("productionStatusName 没查出来（表 1.3）")
	}
	if m.Product == nil || m.Product.ParameterName != "Temperature" {
		t.Errorf("product 参数名不对: %+v", m.Product)
	}
	if m.Product.TemplateName == "" {
		t.Error("product.TemplateName 没查出来（模板 4.0）")
	}
	if m.DataRep == nil || m.DataRep.TemplateName == "" {
		t.Errorf("dataRep.TemplateName 没查出来: %+v", m.DataRep)
	}
	if m.Grid == nil {
		t.Fatal("grid 缺失")
	}
	if m.Grid.Ni != 3 || m.Grid.Nj != 3 || m.Grid.NumPoints != 9 {
		t.Errorf("网格 = %dx%d/%d 点，想要 3x3/9", m.Grid.Ni, m.Grid.Nj, m.Grid.NumPoints)
	}
	if m.Grid.TemplateNumber != 0 || m.Grid.ShapeOfEarth != 6 || m.Grid.Radius != 6371229 {
		t.Errorf("网格元数据不对: %+v", m.Grid)
	}
	if m.Grid.PointsError != "" {
		t.Errorf("坐标生成报错: %s", m.Grid.PointsError)
	}
	if len(m.Values) != 9 {
		t.Fatalf("values 长度 = %d，想要 9", len(m.Values))
	}
	// 规则网格默认只给"行纬度 + 列经度"（3 行 / 3 列），不是逐点 9 个。
	if len(m.Lats) != 3 || len(m.Lons) != 3 {
		t.Fatalf("坐标长度 = lats %d / lons %d，想要 3/3（rowsCols）", len(m.Lats), len(m.Lons))
	}
	if m.Grid.CoordinateMode != "rowsCols" {
		t.Errorf("CoordinateMode = %q，想要 rowsCols", m.Grid.CoordinateMode)
	}
	// 样本是 273.15..273.23 K 的等温场，只做量级断言（精确值另有 golden 测试把守）。
	if m.Stats == nil || m.Stats.Min < 273 || m.Stats.Max > 274 || m.Stats.Max <= m.Stats.Min {
		t.Errorf("stats 不合理: %+v", m.Stats)
	}
	if m.Stats.NaNCount != 0 || m.Stats.Count != 9 {
		t.Errorf("stats 计数不对: %+v", m.Stats)
	}
	// 首点 (45N, 10E)，末点 (25N, 30E)。
	if math.Abs(m.Lats[0]-45) > 1e-9 || math.Abs(m.Lons[0]-10) > 1e-9 {
		t.Errorf("首点坐标 = (%.6f, %.6f)，想要 (45, 10)", m.Lats[0], m.Lons[0])
	}
}

// TestDecodeBytesMetadataOnly 确认关掉数值/坐标后不再碰 section 7 与坐标生成。
func TestDecodeBytesMetadataOnly(t *testing.T) {
	res := decodeBytes(fixture(t), Options{Values: false, Points: false})
	if !res.OK || len(res.Messages) != 1 {
		t.Fatalf("OK=%v 消息数=%d", res.OK, len(res.Messages))
	}
	m := res.Messages[0]
	if m.Values != nil || m.Stats != nil {
		t.Error("Values=false 时不该产出数值")
	}
	if m.Lats != nil || m.Lons != nil {
		t.Error("Points=false 时不该产出坐标")
	}
	if m.Product == nil || m.Grid == nil || m.DataRep == nil {
		t.Errorf("元数据不该被关掉: product=%v grid=%v dataRep=%v", m.Product, m.Grid, m.DataRep)
	}
}

// TestDecodeBytesPerPoint 验证 PerPoint 把规则网格展开成与 values 同序的逐点坐标。
func TestDecodeBytesPerPoint(t *testing.T) {
	res := decodeBytes(fixture(t), Options{Values: true, Points: true, PerPoint: true})
	m := res.Messages[0]
	if m.Grid.CoordinateMode != "perPoint" {
		t.Fatalf("CoordinateMode = %q，想要 perPoint", m.Grid.CoordinateMode)
	}
	if len(m.Lats) != 9 || len(m.Lons) != 9 {
		t.Fatalf("坐标长度 = %d/%d，想要 9/9", len(m.Lats), len(m.Lons))
	}
	if len(m.Lats) != len(m.Values) {
		t.Fatalf("坐标 %d 个 vs 数值 %d 个，必须一一对齐", len(m.Lats), len(m.Values))
	}
	// 样本 3x3：首点 (45N, 10E)，末点 (25N, 30E)。
	if math.Abs(m.Lats[0]-45) > 1e-9 || math.Abs(m.Lons[0]-10) > 1e-9 {
		t.Errorf("首点 = (%.6f, %.6f)，想要 (45, 10)", m.Lats[0], m.Lons[0])
	}
	if math.Abs(m.Lats[8]-25) > 1e-9 || math.Abs(m.Lons[8]-30) > 1e-9 {
		t.Errorf("末点 = (%.6f, %.6f)，想要 (25, 30)", m.Lats[8], m.Lons[8])
	}
}

// TestDecodeBytesRejects 覆盖 fail-loud：非 GRIB2 与截断输入都必须 ok=false 且带上下文。
func TestDecodeBytesRejects(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"空输入", nil},
		{"magic 不对", []byte("NOPE0123456789abcdef")},
		{"只有半截头", []byte("GRIB\x00\x00\x00\x02")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := decodeBytes(tc.data, Options{Values: true, Points: true})
			if res.OK {
				t.Fatal("坏输入却 OK=true")
			}
			if len(res.Errors) == 0 {
				t.Fatal("坏输入却没有错误文本")
			}
		})
	}
}

// TestStatsNaN 缺测（NaN）不进 min/max/mean，但单独计数。
func TestStatsNaN(t *testing.T) {
	s := statsOf([]float32{1, 2, float32(math.NaN()), 3})
	if s.NaNCount != 1 || s.Count != 4 {
		t.Fatalf("计数不对: %+v", s)
	}
	if s.Min != 1 || s.Max != 3 || math.Abs(s.Mean-2) > 1e-9 {
		t.Errorf("统计不对: %+v", s)
	}
	if empty := statsOf(nil); empty.Count != 0 || empty.NaNCount != 0 {
		t.Errorf("空数组统计不对: %+v", empty)
	}
}

// TestBytesArg 覆盖三类入参：Uint8Array / ArrayBuffer / number[]。
func TestBytesArg(t *testing.T) {
	raw := fixture(t)
	u8 := js.Global().Get("Uint8Array").New(len(raw))
	js.CopyBytesToJS(u8, raw)

	fromU8, err := bytesArg(u8)
	if err != nil || len(fromU8) != len(raw) || fromU8[0] != 'G' {
		t.Fatalf("Uint8Array 入参: err=%v len=%d", err, len(fromU8))
	}

	ab := js.Global().Get("ArrayBuffer").New(len(raw))
	js.CopyBytesToJS(js.Global().Get("Uint8Array").New(ab), raw)
	fromAB, err := bytesArg(ab)
	if err != nil || len(fromAB) != len(raw) || fromAB[len(fromAB)-1] != raw[len(raw)-1] {
		t.Fatalf("ArrayBuffer 入参: err=%v len=%d", err, len(fromAB))
	}

	// number[]（小数组即可，够验证转换路径）
	arr := js.Global().Get("Array").New(3)
	arr.SetIndex(0, 7)
	arr.SetIndex(1, 8)
	arr.SetIndex(2, 9)
	fromArr, err := bytesArg(arr)
	if err != nil || len(fromArr) != 3 || fromArr[0] != 7 || fromArr[2] != 9 {
		t.Fatalf("number[] 入参: err=%v %v", err, fromArr)
	}

	if _, err := bytesArg(js.ValueOf(42)); err == nil {
		t.Error("数字入参应当报错")
	}
}

// TestToJSShape 验证 JS 侧拿到的对象形状：大数组是 TypedArray，元数据是普通对象。
func TestToJSShape(t *testing.T) {
	res := decodeBytes(fixture(t), Options{Values: true, Points: true})
	obj := toJS(res)

	if !obj.Get("ok").Bool() {
		t.Fatal("ok 不是 true")
	}
	msgs := obj.Get("messages")
	if msgs.Length() != 1 {
		t.Fatalf("messages 长度 = %d", msgs.Length())
	}
	m := msgs.Index(0)

	vals := m.Get("values")
	if !vals.InstanceOf(js.Global().Get("Float32Array")) {
		t.Fatalf("values 不是 Float32Array，而是 %s", vals.Type())
	}
	if vals.Length() != 9 {
		t.Fatalf("values 长度 = %d，想要 9", vals.Length())
	}
	if got, want := vals.Index(0).Float(), float64(res.Messages[0].Values[0]); math.Abs(got-want) > 1e-6 {
		t.Errorf("values[0] = %v，Go 侧 %v", got, want)
	}

	grid := m.Get("grid")
	if !grid.Get("lats").InstanceOf(js.Global().Get("Float64Array")) {
		t.Fatal("lats 不是 Float64Array")
	}
	if grid.Get("lats").Length() != 3 || grid.Get("lons").Length() != 3 {
		t.Errorf("坐标长度 = %d/%d，想要 3/3", grid.Get("lats").Length(), grid.Get("lons").Length())
	}
	if got := grid.Get("lats").Index(0).Float(); math.Abs(got-45) > 1e-9 {
		t.Errorf("lats[0] = %v，想要 45", got)
	}
	if got := m.Get("product").Get("parameterName").String(); got != "Temperature" {
		t.Errorf("parameterName = %q", got)
	}
	// 统计对象要能直接被前端读走
	if got := m.Get("stats").Get("count").Int(); got != 9 {
		t.Errorf("stats.count = %d", got)
	}
}

// TestHandleOptions 验证 JS 入口的 opts 解析（含默认值与显式覆盖）。
func TestHandleOptions(t *testing.T) {
	raw := fixture(t)
	u8 := js.Global().Get("Uint8Array").New(len(raw))
	js.CopyBytesToJS(u8, raw)

	// decode 默认出数值
	full := handle([]js.Value{u8}, true).(js.Value)
	if !full.Get("ok").Bool() {
		t.Fatalf("decode 失败: %v", full.Get("errors"))
	}
	if full.Get("messages").Index(0).Get("values").Type() != js.TypeObject {
		t.Error("decode 默认应当带 values")
	}

	// opts 显式关掉
	opts := js.Global().Get("Object").New()
	opts.Set("values", false)
	opts.Set("points", false)
	lean := handle([]js.Value{u8, opts}, true).(js.Value)
	lm := lean.Get("messages").Index(0)
	if lm.Get("values").Type() != js.TypeUndefined {
		t.Error("values=false 时不该有 values 字段")
	}
	if lm.Get("grid").Get("lats").Type() != js.TypeUndefined {
		t.Error("points=false 时不该有 lats 字段")
	}

	// 无参调用 → 结构化错误对象，而不是 panic
	bad := handle(nil, true).(js.Value)
	if bad.Get("ok").Bool() || bad.Get("errors").Length() == 0 {
		t.Error("缺参数应当返回 ok=false + errors")
	}
}

// TestVersion 绑定层版本号必须非空（前端据此做兼容判断）。
func TestVersion(t *testing.T) {
	if Version == "" {
		t.Fatal("Version 为空")
	}
}
