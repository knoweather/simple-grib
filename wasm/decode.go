//go:build js && wasm

package main

import (
	"bytes"
	"math"
	"time"

	grib "github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/grib1"
	"github.com/knoweather/simple-grib/grid"
	"github.com/knoweather/simple-grib/spec"
)

// Version 是 wasm 绑定层的版本号（与库本身独立，绑定层接口变更时 +1）。
const Version = "0.1.0"

// Options 控制解码深度。三者都可关闭以省时间/内存：
//   - Values=false 时只出元数据，不碰 section 7（大文件秒回）；
//   - Points=false 时不算坐标；
//   - PerPoint=true 时把规则网格的"行纬度 / 列经度"展开成与 values 逐点对齐的
//     完整数组（前端画等值线/散点直接可用，代价是 O(点数) 内存）。
type Options struct {
	Values   bool
	Points   bool
	PerPoint bool
}

// result 是一次 decode 的顶层结果。
type result struct {
	OK          bool        `json:"ok"`
	Bytes       int         `json:"bytes"`
	NumMessages int         `json:"numMessages"`
	Messages    []msgResult `json:"messages"`
	Errors      []string    `json:"errors,omitempty"`
}

// msgResult 是一条消息的结果。任何一条消息内部的失败（缺 section、未知模板、
// 数值解码不支持……）都只落到该消息自己的 errors 上，不影响同文件其它消息
// —— 与库本身一致：错误不静默、也不连坐。
type msgResult struct {
	Index  int   `json:"index"`
	Offset int64 `json:"offset"`
	Size   int   `json:"size"`
	// Edition 是消息的 GRIB 版本（1 或 2）。**段编号、码空间随版本变化**：
	// sections 里的 2 在 GRIB2 是 local use、在 GRIB1 是网格描述段；
	// product.level1.type 在 GRIB2 查表 4.5、在 GRIB1 查表 3。先看 edition 再解释。
	Edition        int    `json:"edition"`
	Discipline     int    `json:"discipline"`
	DisciplineName string `json:"disciplineName,omitempty"`
	Sections       []int  `json:"sections"`
	// SectionSizes 与 Sections 一一对应，给出每个 section 的字节长度。
	SectionSizes   []int        `json:"sectionSizes"`
	Identification *identInfo   `json:"identification,omitempty"`
	Product        *productInfo `json:"product,omitempty"`
	Grid           *gridInfo    `json:"grid,omitempty"`
	DataRep        *dataRepInfo `json:"dataRep,omitempty"`
	Stats          *stats       `json:"stats,omitempty"`
	Errors         []string     `json:"errors,omitempty"`

	// Values / Values64 / Lats / Lons 不进 JSON：它们通过 TypedArray 单独搬运
	//（见 api.go），免得几百万个浮点走一遍字符串序列化。
	//
	// GRIB2 数值本身是 IEEE binary32，走 Values（Float32Array）；GRIB1 的取值公式
	// 在 float64 下算（参考值是 IBM 浮点），降到 float32 会丢有效位，所以走
	// Values64（Float64Array）。前端两者都会收到，只按有值的那个用。
	Values   []float32 `json:"-"`
	Values64 []float64 `json:"-"`
	Lats     []float64 `json:"-"`
	Lons     []float64 `json:"-"`
}

// identInfo 是 section 1 的常用字段；码表名在库里查好一起带出来，
// 免得前端把 WMO 表抄一遍。
type identInfo struct {
	Centre                  int    `json:"centre"`
	CentreName              string `json:"centreName,omitempty"`
	SubCentre               int    `json:"subCentre"`
	MasterTableVersion      int    `json:"masterTableVersion"`
	LocalTableVersion       int    `json:"localTableVersion"`
	ReferenceTime           string `json:"referenceTime"` // RFC3339 UTC
	ProductionStatus        int    `json:"productionStatus"`
	ProductionStatusName    string `json:"productionStatusName,omitempty"`
	TypeOfProcessedData     int    `json:"typeOfProcessedData"`
	TypeOfProcessedDataName string `json:"typeOfProcessedDataName,omitempty"`
	CalendarTemplate        int    `json:"calendarTemplate"` // -1 = section 1 未带日历定义
}

type productInfo struct {
	TemplateNumber          int           `json:"templateNumber"`
	TemplateName            string        `json:"templateName,omitempty"`
	ParameterCategory       int           `json:"parameterCategory"`
	ParameterNumber         int           `json:"parameterNumber"`
	ParameterName           string        `json:"parameterName"`
	TypeOfGeneratingProcess int           `json:"typeOfGeneratingProcess"`
	NV                      int           `json:"nv"`
	ForecastTime            *forecastInfo `json:"forecastTime,omitempty"`
	Level1                  *levelInfo    `json:"level1,omitempty"`
	Level2                  *levelInfo    `json:"level2,omitempty"`
}

type forecastInfo struct {
	Unit     int     `json:"unit"`
	Raw      uint32  `json:"raw"`
	Seconds  float64 `json:"seconds"`
	Overflow bool    `json:"overflow"`
}

type levelInfo struct {
	Type     int     `json:"type"`
	TypeName string  `json:"typeName,omitempty"` // 表 4.5 的名字
	Scale    float64 `json:"scale"`
	HasValue bool    `json:"hasValue"`
}

type gridInfo struct {
	TemplateNumber int     `json:"templateNumber"`
	TemplateName   string  `json:"templateName,omitempty"`
	Ni             int     `json:"ni"`
	Nj             int     `json:"nj"`
	NumPoints      int     `json:"numPoints"`
	QuasiRegular   bool    `json:"quasiRegular"`
	ShapeOfEarth   int     `json:"shapeOfEarth"`
	Radius         float64 `json:"radius"`
	// CoordinateMode 说明 lats/lons 的语义，前端据此决定要不要自己展开：
	//   "rowsCols" = 规则/Gaussian 网格：lats 是 Nj 个行纬度、lons 是 Ni 个列经度
	//                （可分离，第 j 行第 i 列 = (lats[j], lons[i])）；
	//   "perPoint" = lats/lons 已是 Ni×Nj 的逐点数组，与 values 同序；
	//   ""         = 没有坐标（准规则网格或投影模板未实现）。
	CoordinateMode string `json:"coordinateMode"`
	// PointsError 记录坐标生成的失败原因（例如投影模板尚未实现）。
	// 元数据仍然可用，只是没有 lats/lons。
	PointsError string `json:"pointsError,omitempty"`
}

type dataRepInfo struct {
	TemplateNumber int    `json:"templateNumber"`
	TemplateName   string `json:"templateName,omitempty"`
	NumValues      int    `json:"numValues"`
}

// stats 是数值的快速摘要，方便前端不遍历数组就能定标色阶。
type stats struct {
	Count    int     `json:"count"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
	Mean     float64 `json:"mean"`
	NaNCount int     `json:"nanCount"`
}

// decodeBytes 是绑定层的核心：把一段 GRIB2 字节流解成结果对象。
// 它是纯 Go 的（不碰 js.Value），因此在 wasm 下可以直接单测。
func decodeBytes(data []byte, opts Options) *result {
	res := &result{OK: true, Bytes: len(data), Messages: []msgResult{}}

	fl, err := grib.OpenReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		// 文件级失败：索引都建不起来，谈不上任何消息。
		res.OK = false
		res.Errors = append(res.Errors, err.Error())
		return res
	}
	defer fl.Close()

	n, _ := fl.NumMessages()
	res.NumMessages = n

	for i := 0; i < n; i++ {
		res.Messages = append(res.Messages, decodeMessage(fl, i, opts))
	}
	return res
}

// decodeMessage 解第 i 条消息。各子步骤互不连坐：某一项失败只记进 Errors。
func decodeMessage(fl *grib.File, i int, opts Options) msgResult {
	m, err := fl.Message(i)
	if err != nil {
		return msgResult{Index: i, Errors: []string{err.Error()}}
	}
	out := msgResult{
		Index:      m.Index(),
		Offset:     m.Offset(),
		Size:       m.Size(),
		Edition:    m.Edition(),
		Sections:   m.Sections(),
		Discipline: m.Discipline(),
	}
	// GRIB1 没有 discipline 概念（段 0 里那个字节是总长度的一部分），不给名字，
	// 免得把表 0.0 的 "Meteorological products" 误贴到 GRIB1 消息上。
	if m.Edition() == 2 {
		out.DisciplineName = tableName("0.0", m.Discipline())
	}
	for _, n := range out.Sections {
		if sec, err := m.Section(n); err == nil {
			out.SectionSizes = append(out.SectionSizes, sec.Length)
		} else {
			out.SectionSizes = append(out.SectionSizes, 0)
		}
	}

	if id, err := m.Identification(); err != nil {
		out.Errors = append(out.Errors, "identification: "+err.Error())
	} else {
		out.Identification = identOf(id)
	}
	if p, err := m.Product(); err != nil {
		out.Errors = append(out.Errors, "product: "+err.Error())
	} else {
		out.Product = productOf(p)
	}
	if dr, err := m.DataRep(); err != nil {
		out.Errors = append(out.Errors, "dataRep: "+err.Error())
	} else {
		out.DataRep = &dataRepInfo{
			TemplateNumber: dr.TemplateNumber,
			NumValues:      dr.NumValues,
		}
		if m.Edition() == 1 {
			// GRIB1 的数据表示就是"简单打包"（段 4 标志位在解析时已挡掉其它方式），
			// 没有 GRIB2 的 5.x 模板号。
			out.DataRep.TemplateName = "GRIB1 simple packing (section 4)"
		} else {
			out.DataRep.TemplateName = templateName(5, dr.TemplateNumber)
		}
	}

	if g, err := m.Grid(); err != nil {
		out.Errors = append(out.Errors, "grid: "+err.Error())
	} else {
		out.Grid = gridOf(g, m.Edition())
		if opts.Points {
			lats, lons, mode, err := coordinates(g, opts.PerPoint)
			if err != nil {
				out.Grid.PointsError = err.Error()
			} else {
				out.Grid.CoordinateMode = mode
				out.Lats, out.Lons = lats, lons
			}
		}
	}

	if opts.Values {
		if m.Edition() == 1 {
			// GRIB1 原生精度是 float64（见 msgResult.Values64 的说明）。
			vals, err := m.Values64()
			if err != nil {
				out.Errors = append(out.Errors, "values: "+err.Error())
			} else {
				out.Values64 = vals
				out.Stats = statsOf64(vals)
			}
		} else {
			vals, err := m.Values()
			if err != nil {
				out.Errors = append(out.Errors, "values: "+err.Error())
			} else {
				out.Values = vals
				out.Stats = statsOf(vals)
			}
		}
	}
	return out
}

// identOf 抽取 section 1 的常用字段，并把码表名一并查好。
func identOf(id *grib.Identification) *identInfo {
	out := &identInfo{
		Centre:                  id.Centre,
		CentreName:              id.CentreName,
		SubCentre:               id.SubCentre,
		MasterTableVersion:      id.MasterTableVersion,
		LocalTableVersion:       id.LocalTableVersion,
		ReferenceTime:           id.ReferenceTime.UTC().Format(time.RFC3339),
		ProductionStatus:        id.ProductionStatus,
		ProductionStatusName:    tableName("1.3", id.ProductionStatus),
		TypeOfProcessedData:     id.TypeOfProcessedData,
		TypeOfProcessedDataName: tableName("1.4", id.TypeOfProcessedData),
		CalendarTemplate:        -1,
	}
	if id.Calendar != nil {
		out.CalendarTemplate = id.Calendar.TemplateNumber
	}
	return out
}

// productOf 映射 product。码空间随版本不同：GRIB2 的层类型码查表 4.5、
// GRIB1 查表 3；GRIB1 没有产品模板号（给 -1，名字标注"不适用"）。
func productOf(p *grib.Product) *productInfo {
	out := &productInfo{
		TemplateNumber:          p.TemplateNumber,
		ParameterCategory:       p.ParameterCategory,
		ParameterNumber:         p.ParameterNumber,
		ParameterName:           p.ParameterName,
		TypeOfGeneratingProcess: p.TypeOfGeneratingProcess,
		NV:                      p.NV,
	}
	if p.Edition == 1 {
		out.TemplateName = "not applicable in GRIB1"
	} else {
		out.TemplateName = templateName(4, p.TemplateNumber)
	}
	if ft := p.ForecastTime; ft != nil {
		out.ForecastTime = &forecastInfo{
			Unit:    ft.Unit,
			Raw:     ft.Raw,
			Seconds: ft.Value.Seconds(),
			// GRIB1 的月/年单位没有固定时长：Value 为 0 时 Overflow 标记提示
			// 前端"按原始计数自行解释"。
			Overflow: ft.Overflow || (p.Edition == 1 && ft.Value == 0 && ft.Raw != 0),
		}
	}
	if lv := p.Level1; lv != nil {
		out.Level1 = levelOf(lv, p.Edition)
	}
	if lv := p.Level2; lv != nil {
		out.Level2 = levelOf(lv, p.Edition)
	}
	return out
}

// levelOf 映射层信息；层类型名按版本查对应的表（GRIB2 表 4.5 / GRIB1 表 3）。
func levelOf(lv *grib.Level, edition int) *levelInfo {
	out := &levelInfo{
		Type:     lv.Type,
		Scale:    lv.Scale,
		HasValue: lv.HasValue,
	}
	if edition == 1 {
		out.TypeName = grib1.LevelTypeName(lv.Type)
	} else {
		out.TypeName = tableName("4.5", lv.Type)
	}
	return out
}

// gridOf 映射网格定义；网格类型名按版本查（GRIB2 的 3.x 模板 / GRIB1 表 6）。
func gridOf(g *grid.Definition, edition int) *gridInfo {
	out := &gridInfo{
		TemplateNumber: g.TemplateNumber(),
		Ni:             g.Ni(),
		Nj:             g.Nj(),
		NumPoints:      g.NumPoints(),
		QuasiRegular:   g.IsQuasiRegular(),
		ShapeOfEarth:   g.ShapeOfEarth(),
		Radius:         g.Radius(),
	}
	if edition == 1 {
		out.TemplateName = grib1.GridTypeName(g.TemplateNumber())
	} else {
		out.TemplateName = templateName(3, g.TemplateNumber())
	}
	return out
}

// tableName 查 WMO 码表名（表 ID 形如 "0.0"/"1.3"/"4.1"）；查不到返回空串。
func tableName(id string, code int) string {
	t, ok := spec.TableByID(id)
	if !ok {
		return ""
	}
	e, ok := t.Lookup(code)
	if !ok {
		return ""
	}
	return e.Name
}

// templateName 查模板名（section 3/4/5）；本地或未注册模板返回空串。
func templateName(section, num int) string {
	def, ok := spec.Template(section, num)
	if !ok {
		return ""
	}
	return def.Name
}

// coordinates 产出坐标数组，并回报它们的语义（见 gridInfo.CoordinateMode）。
//
// 默认走库的原生语义：规则/Gaussian 网格只给"行纬度 + 列经度"（省 O(点数) 内存），
// 投影网格本来就是逐点。forcePerPoint 时统一展开成与 values 同序的逐点数组
// —— 走 Point(i)，因此自动兼容各种扫描方向（WE:SN、NS、交替行等）。
func coordinates(g *grid.Definition, forcePerPoint bool) (lats, lons []float64, mode string, err error) {
	n := g.NumPoints()
	if forcePerPoint && n > 0 {
		lats = make([]float64, n)
		lons = make([]float64, n)
		for i := 0; i < n; i++ {
			la, lo, perr := g.Point(i)
			if perr != nil {
				return nil, nil, "", perr
			}
			lats[i], lons[i] = la, lo
		}
		return lats, lons, "perPoint", nil
	}

	lats, err = g.Lats()
	if err != nil {
		return nil, nil, "", err
	}
	lons, err = g.Lons()
	if err != nil {
		return nil, nil, "", err
	}
	mode = "rowsCols"
	if len(lats) == n && len(lons) == n {
		// 投影网格（3.20/3.30）返回的就是逐点数组。
		mode = "perPoint"
	}
	return lats, lons, mode, nil
}

// statsOf64 是 statsOf 的 float64 版本，给 GRIB1 原生精度数值用。
func statsOf64(v []float64) *stats {
	s := &stats{Count: len(v)}
	if len(v) == 0 {
		return s
	}
	s.Min = math.Inf(1)
	s.Max = math.Inf(-1)
	var sum float64
	var n int
	for _, x := range v {
		if math.IsNaN(x) {
			s.NaNCount++
			continue
		}
		if x < s.Min {
			s.Min = x
		}
		if x > s.Max {
			s.Max = x
		}
		sum += x
		n++
	}
	if n == 0 {
		s.Min, s.Max, s.Mean = 0, 0, 0
		return s
	}
	s.Mean = sum / float64(n)
	return s
}

// statsOf 统计数值摘要。NaN 不计入 min/max/mean（气象场里 NaN 就是缺测），
// 但单独计数上报，前端可据此决定是否画成透明。
func statsOf(v []float32) *stats {
	s := &stats{Count: len(v)}
	if len(v) == 0 {
		return s
	}
	s.Min = math.Inf(1)
	s.Max = math.Inf(-1)
	var sum float64
	var n int
	for _, f := range v {
		x := float64(f)
		if math.IsNaN(x) {
			s.NaNCount++
			continue
		}
		if x < s.Min {
			s.Min = x
		}
		if x > s.Max {
			s.Max = x
		}
		sum += x
		n++
	}
	if n == 0 {
		s.Min, s.Max = 0, 0
		return s
	}
	s.Mean = sum / float64(n)
	return s
}
