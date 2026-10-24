// 命令 gribdump 是 GRIB2/GRIB1 文件的检视工具：默认打印消息元数据，
// 并支持 -values N（前 N 个数据值）、-section n（逐字段 Walk 转储，带表名，
// 可重复）、-json（机器可读输出）以及 -webp（导出模板 5.49152 的 WebP 图像
// 载荷）。
package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/grib1"
	"github.com/knoweather/simple-grib/grid"
	"github.com/knoweather/simple-grib/pack"
	"github.com/knoweather/simple-grib/paramdb"
	"github.com/knoweather/simple-grib/spec"
)

func main() {
	var (
		nValues  int
		jsonOut  bool
		sections sectionFlag
		webpOut  string
	)
	flag.IntVar(&nValues, "values", 0, "print the first N data values of each message (0 = off)")
	flag.BoolVar(&jsonOut, "json", false, "JSON output")
	flag.Var(&sections, "section", "dump section n field by field with table names (repeatable; valid: 0-7)")
	flag.StringVar(&webpOut, "webp", "", "write the WebP image payload to this file (one-message input) or directory (multi-message input)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: gribdump [flags] file...")
		fmt.Fprintln(os.Stderr, "Flags:")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}

	exit := 0
	for _, path := range flag.Args() {
		if err := dumpFile(path, nValues, jsonOut, sections, webpOut); err != nil {
			fmt.Fprintf(os.Stderr, "gribdump: %s: %v\n", path, err)
			exit = 1
		}
	}
	os.Exit(exit)
}

// sectionFlag 收集重复的 -section 标志。
type sectionFlag []int

func (s *sectionFlag) String() string { return fmt.Sprint(*s) }

func (s *sectionFlag) Set(v string) error {
	n, err := strconv.Atoi(v) // Atoi 会拒绝尾部杂质（"3abc"），Sscanf 不会
	if err != nil || n < 0 || n > 7 {
		return fmt.Errorf("section number must be an integer in 0..7, got %q", v)
	}
	*s = append(*s, n)
	return nil
}

// ---------------------------------------------------------------------------
// 对 JSON 安全的浮点数
// ---------------------------------------------------------------------------

// float32Val 是一个 float32，把非有限的 IEEE 值（NaN、±Inf）编码成字符串，
// 因为 encoding/json 无法表示它们，否则会让整篇文档序列化失败。
// 有限值则保留 float32 的精确渲染。
type float32Val float32

func (v float32Val) MarshalJSON() ([]byte, error) {
	f := float64(float32(v))
	if !isFinite(f) {
		return []byte(`"` + nonFiniteName(f) + `"`), nil
	}
	return json.Marshal(float32(v))
}

// float64Val 是 float32Val 的 float64 对应物（用于缩放后的层值，其范围
// 超出 float32）。
type float64Val float64

func (v float64Val) MarshalJSON() ([]byte, error) {
	f := float64(v)
	if !isFinite(f) {
		return []byte(`"` + nonFiniteName(f) + `"`), nil
	}
	return json.Marshal(f)
}

func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func nonFiniteName(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "+Inf"
	default:
		return "-Inf"
	}
}

// ---------------------------------------------------------------------------
// 文件 / 消息层级
// ---------------------------------------------------------------------------

func dumpFile(path string, nValues int, jsonOut bool, sections []int, webpOut string) error {
	f, err := grib.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	n, err := f.NumMessages()
	if err != nil {
		return err
	}

	if webpOut != "" {
		if err := extractWebP(f, n, webpOut); err != nil {
			return err
		}
	}

	type fileOut struct {
		File     string      `json:"file"`
		Messages []msgOutput `json:"messages"`
	}
	out := fileOut{File: path}
	var human []string

	for i := 0; i < n; i++ {
		m, err := f.Message(i)
		if err != nil {
			return fmt.Errorf("message %d: %w", i, err)
		}
		mo, herr := messageOutput(m, path, i, nValues, sections)
		out.Messages = append(out.Messages, mo)
		if !jsonOut {
			human = append(human, renderMessage(mo))
		}
		if herr != nil {
			// Partial failure (e.g. unsupported data representation): report
			// it on stderr but keep the rest of the dump.
			fmt.Fprintf(os.Stderr, "gribdump: %s message %d: %v\n", path, i, herr)
		}
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	for _, h := range human {
		fmt.Println(h)
	}
	return nil
}

// extractWebP 把 WebP 图像消息的载荷写成文件。-webp 的目标在单消息输入时
// 是文件名，在多消息输入时是目录（每个消息写成 msgNNN.webp）；非 WebP 消息
// 被跳过并提示，一个 WebP 都没找到则报错。
//
// extractWebP writes the payload of WebP image messages to disk. With a
// single-message input the -webp argument is a file name; with several
// messages it is a directory (msgNNN.webp per message). Non-WebP messages are
// skipped with a note; finding none is an error.
func extractWebP(f *grib.File, n int, dest string) error {
	if n == 0 {
		return fmt.Errorf("-webp: input has no messages")
	}
	if n == 1 {
		m, err := f.Message(0)
		if err != nil {
			return fmt.Errorf("message 0: %w", err)
		}
		raw, err := m.WebP()
		if err != nil {
			return fmt.Errorf("message 0: %w", err)
		}
		if err := os.WriteFile(dest, raw, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "gribdump: wrote %s (%d bytes)\n", dest, len(raw))
		return nil
	}

	written := 0
	dirReady := false
	for i := 0; i < n; i++ {
		m, err := f.Message(i)
		if err != nil {
			return fmt.Errorf("message %d: %w", i, err)
		}
		raw, err := m.WebP()
		if err != nil {
			fmt.Fprintf(os.Stderr, "gribdump: message %d: %v (skipped)\n", i, err)
			continue
		}
		if !dirReady {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return fmt.Errorf("-webp directory %s: %w", dest, err)
			}
			dirReady = true
		}
		name := filepath.Join(dest, fmt.Sprintf("msg%03d.webp", i))
		if err := os.WriteFile(name, raw, 0o644); err != nil {
			return err
		}
		written++
	}
	if written == 0 {
		return fmt.Errorf("-webp: no WebP image messages found")
	}
	fmt.Fprintf(os.Stderr, "gribdump: wrote %d WebP image(s) to %s\n", written, dest)
	return nil
}

// ---------------------------------------------------------------------------
// 输出模型（人类可读与 JSON 渲染共用）
// ---------------------------------------------------------------------------

type msgOutput struct {
	Index      int      `json:"index"`
	Offset     int64    `json:"offset"`
	Size       int      `json:"size"`
	Edition    int      `json:"edition"`
	Discipline code     `json:"discipline"`
	ID         idOut    `json:"identification"`
	Sections   []int    `json:"sections"`
	Grid       *gridOut `json:"grid,omitempty"`
	Product    *prodOut `json:"product,omitempty"`
	// Step 是 ecCodes 的 forecastTime/step/validityDate 三元组：begin 为区间
	// 起点（参考时间 + 表 4.4 预报时效），end 为区间终点（统计模板的
	// end-of-overall-interval，或 GRIB1 的 P2）。瞬时产品两者相等。
	Step *stepOut `json:"step,omitempty"`
	// ParamDB 是 paramdb 概念查询的结果（ecCodes shortName/name/paramId/
	// units/typeOfLevel 的等价物）。取值与 ecCodes 逐字一致：没命中时是
	// "unknown"/0，不是省略。
	ParamDB *pdbOut      `json:"paramdb,omitempty"`
	DataRep *drOut       `json:"dataRep,omitempty"`
	Values  []float32Val `json:"values,omitempty"`
	// Values64 只在 GRIB1 下填充：GRIB1 的取值公式在 float64 下算，直接降到
	// float32 会丢有效位。GRIB2 的数值本身就是 binary32，用上面的 values 字段。
	Values64       []float64Val `json:"values64,omitempty"`
	ValueCount     int          `json:"valueCount,omitempty"`
	SectionsDetail []secOut     `json:"sectionDetails,omitempty"`
	Error          string       `json:"error,omitempty"`
}

type code struct {
	Code int    `json:"code"`
	Name string `json:"name,omitempty"`
}

// pdbOut 是 paramdb 概念查询的输出视图，取值刻意与 ecCodes 的键逐字一致：
// 概念链没命中时 ecCodes 给 shortName/name/units/typeOfLevel = "unknown"、
// paramId = 0（见 paramdb.Unknown），所以这里不省略任何字段。
type pdbOut struct {
	ShortName   string `json:"shortName"`
	Name        string `json:"name"`
	ParamID     int    `json:"paramId"`
	Units       string `json:"units"`
	TypeOfLevel string `json:"typeOfLevel"`
}

// stepOut 是 Message.StepRange 的输出视图。
type stepOut struct {
	Begin string `json:"begin"`
	End   string `json:"end"`
	// StepHours = end − reference time（小时），即 ecCodes 的 "step"。
	// 时间单位无固定时长（月/年）时 StepRange 返回 not-ok，本字段缺席。
	StepHours float64Val `json:"stepHours"`
}

type idOut struct {
	Centre           code   `json:"centre"`
	SubCentre        int    `json:"subCentre"`
	MasterTableVer   int    `json:"masterTableVersion"`
	LocalTableVer    int    `json:"localTableVersion"`
	ReferenceTime    string `json:"referenceTime"`
	ProductionStatus code   `json:"productionStatus"`
	TypeOfProcData   code   `json:"typeOfProcessedData"`
}

type gridOut struct {
	Template     code        `json:"template"`
	Shape        code        `json:"shapeOfEarth"`
	RadiusM      float64     `json:"radiusMeters,omitempty"`
	Points       int         `json:"points"`
	Declared     int         `json:"declaredPoints,omitempty"`
	FirstPoint   *[2]float64 `json:"firstPoint,omitempty"` // lat, lon (deg)
	LastPoint    *[2]float64 `json:"lastPoint,omitempty"`
	QuasiRegular bool        `json:"quasiRegular,omitempty"`
}

type prodOut struct {
	Template code `json:"template"`
	// Parameter is the WMO table 4.2 entry looked up in the *newest* vendored
	// tables (spec.ParameterName): Code is -1 when that lookup misses.
	Parameter code   `json:"parameter"`
	Unit      string `json:"unit,omitempty"` // WMO table 4.2 unit; "" = not declared
	Category  int    `json:"category"`
	Number    int    `json:"number"`
	// ParameterName is ecCodes' "parameterName": the table 4.2 title from the
	// table version this message declares (Product.ParameterName /
	// spec.ParameterNameFor), falling back to the decimal code exactly like
	// ecCodes. It is the field to diff against `codes_get(gid,"parameterName")`;
	// Parameter above keeps the version-independent, always-newest view.
	ParameterName string `json:"parameterName"`
	GenProcess    code   `json:"typeOfGeneratingProcess"`
	ForecastTime  *fcOut `json:"forecastTime,omitempty"`
	// EndOfInterval is the statistical templates' "time of end of overall
	// time interval" (GRIB2 4.8-4.14) — the interval *end*, while
	// ForecastTime is its start.
	EndOfInterval string  `json:"endOfOverallTimeInterval,omitempty"`
	Level1        *lvlOut `json:"level1,omitempty"`
	Level2        *lvlOut `json:"level2,omitempty"`
	NV            int     `json:"nv"`
}

type fcOut struct {
	Unit  code   `json:"unit"`
	Value string `json:"value"`
}

type lvlOut struct {
	Type  code       `json:"type"`
	Scale float64Val `json:"scale"`
}

type drOut struct {
	Template  code `json:"template"`
	NumValues int  `json:"numberOfValues"`
}

type secOut struct {
	Number   int        `json:"number"`
	Length   int        `json:"length"`
	Template *code      `json:"template,omitempty"`
	Fields   []fieldOut `json:"fields,omitempty"`
	Note     string     `json:"note,omitempty"`
}

type fieldOut struct {
	Octets string `json:"octets"`
	Raw    any    `json:"raw"`
	Name   string `json:"name,omitempty"` // code table entry name when resolvable
}

func messageOutput(m *grib.Message, path string, i, nValues int, sections []int) (msgOutput, error) {
	var mo msgOutput
	mo.Index = m.Index()
	mo.Offset = m.Offset()
	mo.Size = m.Size()
	mo.Edition = m.Edition()

	// GRIB1 没有 discipline 字段；统一 API 为兼容 GRIB2 概念映射为 0，
	// 这里标清"不适用"，避免把该兼容值伪装成实际编码。
	d := m.Discipline()
	mo.Discipline = code{Code: d}
	if m.Edition() == 1 {
		mo.Discipline.Name = "not applicable in GRIB1 (compatibility mapping = 0)"
	} else if t, ok := spec.TableByID("0.0"); ok {
		if e, ok := t.Lookup(d); ok {
			mo.Discipline.Name = e.Name
		}
	}

	// 标识（section 1）
	if id, err := m.Identification(); err != nil {
		mo.ID = idOut{Centre: code{Code: -1}, ReferenceTime: "n/a"}
	} else if id.Edition == 1 {
		// GRIB1 的段 1 布局完全不同：中心是 1 字节（通用表 C-1），表版本是
		// "表 2 的版本号"，而"生产状态 / 数据类型"是 GRIB2 独有的概念 —— 这里给
		// -1 明确表示不适用，而不是编一个 0（0 在表 1.3 里是"业务产品"，会被误读）。
		c := code{Code: id.Centre}
		if id.CentreName != "" {
			c.Name = id.CentreName
		}
		na := code{Code: -1, Name: "not applicable in GRIB1"}
		mo.ID = idOut{
			Centre:           c,
			SubCentre:        id.SubCentre,
			MasterTableVer:   id.MasterTableVersion,
			LocalTableVer:    id.LocalTableVersion,
			ReferenceTime:    id.ReferenceTime.UTC().Format("2006-01-02T15:04:05Z"),
			ProductionStatus: na,
			TypeOfProcData:   na,
		}
	} else {
		c := code{Code: id.Centre}
		if id.CentreName != "" {
			c.Name = id.CentreName
		}
		mo.ID = idOut{
			Centre:           c,
			SubCentre:        id.SubCentre,
			MasterTableVer:   id.MasterTableVersion,
			LocalTableVer:    id.LocalTableVersion,
			ReferenceTime:    id.ReferenceTime.UTC().Format("2006-01-02T15:04:05Z"),
			ProductionStatus: tableCode(1, 3, id.ProductionStatus),
			TypeOfProcData:   tableCode(1, 4, id.TypeOfProcessedData),
		}
	}

	mo.Sections = m.Sections()

	// 网格（section 3）
	if g, err := m.Grid(); err != nil {
		var abs *grib.ErrSectionAbsent
		if !errors.As(err, &abs) {
			addErr(&mo, "grid", err)
		}
	} else {
		tmpl := code{Code: g.TemplateNumber()}
		if m.Edition() == 1 {
			// GRIB1 的网格号来自表 6（数据表示类型），与 GRIB2 的 3.x 模板号不是一回事。
			tmpl.Name = grib1.GridTypeName(g.TemplateNumber())
		} else if td, ok := spec.Template(3, g.TemplateNumber()); ok {
			tmpl.Name = td.Name
		}
		go_ := gridOut{Template: tmpl, Shape: tableCode(3, 2, g.ShapeOfEarth()), Points: g.NumPoints(), QuasiRegular: g.IsQuasiRegular()}
		if r := g.Radius(); !math.IsNaN(r) {
			go_.RadiusM = r
		}
		if g.NumPointsDeclared() > 0 {
			go_.Declared = g.NumPointsDeclared()
		}
		if n := g.NumPoints(); n > 0 {
			fp := [2]float64{}
			lp := [2]float64{}
			if lat, lon, err := g.Point(0); err == nil {
				fp[0], fp[1] = lat, lon
				go_.FirstPoint = &fp
			}
			if lat, lon, err := g.Point(n - 1); err == nil {
				lp[0], lp[1] = lat, lon
				go_.LastPoint = &lp
			}
		}
		mo.Grid = &go_
	}

	// 产品（section 4）
	if p, err := m.Product(); err != nil {
		var abs *grib.ErrSectionAbsent
		if !errors.As(err, &abs) {
			addErr(&mo, "product", err)
		}
	} else if m.Edition() == 1 {
		// GRIB1 没有产品模板号、没有参数类别；参数名要注册了对应 (中心, 表版本)
		// 的表 2 才有值（见 grib1.RegisterTable2）。层类型码来自表 3、时间单位来自
		// 表 4 —— 都是 GRIB1 自己的码空间。
		g1, _ := m.GRIB1()
		gp := g1.Product()
		po := prodOut{
			Template:      code{Code: -1, Name: "not applicable in GRIB1"},
			Parameter:     code{Code: gp.Parameter, Name: gp.ParameterName},
			ParameterName: gp.ParameterName,
			Number:        gp.Parameter,
			GenProcess:    code{Code: g1.Identification().GeneratingProcess, Name: "GRIB1 generatingProcessIdentifier (centre-defined)"},
		}
		if d, ok := gp.ForecastTime(); ok {
			po.ForecastTime = &fcOut{
				Unit:  code{Code: gp.TimeRangeUnit, Name: grib1.TimeUnitName(gp.TimeRangeUnit)},
				Value: d.String(),
			}
		} else if gp.P1 != 0 || gp.P2 != 0 {
			po.ForecastTime = &fcOut{
				Unit:  code{Code: gp.TimeRangeUnit, Name: grib1.TimeUnitName(gp.TimeRangeUnit)},
				Value: fmt.Sprintf("P1=%d P2=%d (unit has no fixed duration)", gp.P1, gp.P2),
			}
		}
		po.Level1 = &lvlOut{Type: code{Code: gp.LevelType, Name: grib1.LevelTypeName(gp.LevelType)}, Scale: float64Val(gp.LevelTop)}
		if gp.LayerRange {
			po.Level2 = &lvlOut{Type: code{Code: gp.LevelType, Name: grib1.LevelTypeName(gp.LevelType)}, Scale: float64Val(gp.LevelBottom)}
		}
		mo.Product = &po
	} else {
		tmpl := code{Code: p.TemplateNumber}
		if td, ok := spec.Template(4, p.TemplateNumber); ok {
			tmpl.Name = td.Name
		}
		param := code{}
		if name, err := spec.ParameterName(m.Discipline(), p.ParameterCategory, p.ParameterNumber); err == nil {
			param.Code = p.ParameterNumber
			param.Name = name
		} else {
			param.Code = -1
		}
		unit, _ := spec.ParameterUnit(m.Discipline(), p.ParameterCategory, p.ParameterNumber)
		po := prodOut{
			Template:      tmpl,
			Parameter:     param,
			ParameterName: p.ParameterName,
			Unit:          unit,
			Category:      p.ParameterCategory,
			Number:        p.ParameterNumber,
			GenProcess:    tableCode(4, 3, p.TypeOfGeneratingProcess),
			NV:            p.NV,
		}
		if p.ForecastTime != nil {
			po.ForecastTime = &fcOut{Unit: tableCode(4, 4, p.ForecastTime.Unit), Value: p.ForecastTime.Value.String()}
		}
		if p.EndOfOverallInterval != nil {
			po.EndOfInterval = p.EndOfOverallInterval.Format(time.RFC3339)
		}
		if p.Level1 != nil {
			po.Level1 = &lvlOut{Type: tableCode(4, 5, p.Level1.Type), Scale: float64Val(p.Level1.Scale)}
		}
		if p.Level2 != nil {
			po.Level2 = &lvlOut{Type: tableCode(4, 5, p.Level2.Type), Scale: float64Val(p.Level2.Scale)}
		}
		mo.Product = &po
	}

	// paramdb 概念查询（ecCodes shortName/name/paramId/units/typeOfLevel）。
	// 这里刻意用 ecCodes 的键值视图：概念链没命中时 ecCodes 给
	// shortName/name/units = "unknown"、paramId = 0，而不是空串/缺键——本工具
	// 的输出就是拿来跟 ecCodes 对照的，所以要逐字一致（见 paramdb.Unknown）。
	if q, qerr := paramdb.QueryFrom(m); qerr == nil {
		pdb := &pdbOut{
			ShortName:   paramdb.ShortNameOrUnknown(q),
			Name:        paramdb.NameOrUnknown(q),
			ParamID:     paramdb.ParamIDOrZero(q),
			Units:       paramdb.UnitsOrUnknown(q),
			TypeOfLevel: paramdb.TypeOfLevelOrUnknown(q),
		}
		mo.ParamDB = pdb
	}

	// 预报区间（ecCodes 的 forecastTime/step/validityDate 语义）
	if b, e, ok := m.StepRange(); ok {
		step := &stepOut{Begin: b.Format(time.RFC3339), End: e.Format(time.RFC3339)}
		if id, err := m.Identification(); err == nil {
			step.StepHours = float64Val(e.Sub(id.ReferenceTime).Hours())
		}
		mo.Step = step
	}

	// 数据表示（section 5）
	if dr, err := m.DataRep(); err != nil {
		var abs *grib.ErrSectionAbsent
		if !errors.As(err, &abs) {
			addErr(&mo, "dataRep", err)
		}
	} else {
		tmpl := code{Code: dr.TemplateNumber}
		if m.Edition() == 1 {
			tmpl = code{Code: -1, Name: "GRIB1 simple packing (section 4)"}
		} else if td, ok := spec.Template(5, dr.TemplateNumber); ok {
			tmpl.Name = td.Name
		} else {
			tmpl.Name = "(no spec definition; local or unknown template — register with spec.RegisterLocalTable)"
		}
		mo.DataRep = &drOut{Template: tmpl, NumValues: dr.NumValues}
	}

	// 值
	if nValues > 0 && m.Edition() == 1 {
		// GRIB1：用 float64 原生精度，写进 values64 字段。
		v64, err := m.Values64()
		if err != nil {
			addErr(&mo, "values", err)
		} else {
			mo.ValueCount = len(v64)
			shown := v64
			if nValues < len(v64) {
				shown = v64[:nValues]
			}
			mo.Values64 = make([]float64Val, len(shown))
			for i, v := range shown {
				mo.Values64[i] = float64Val(v)
			}
		}
	} else if nValues > 0 {
		vals, err := m.Values()
		if err != nil {
			var eni *grid.ErrNotImplemented
			var ednc *pack.ErrDecoderNotConfigured
			var eus *grib.ErrUnsupportedBitmap
			switch {
			case errors.As(err, &eni):
				addErr(&mo, "values", fmt.Errorf("grid template %d not implemented", eni.Template))
			case errors.As(err, &ednc):
				addErr(&mo, "values", fmt.Errorf("template %d (%s): decoder not configured", ednc.Template, ednc.Codec))
			case errors.Is(err, pack.ErrUnsupported):
				addErr(&mo, "values", fmt.Errorf("unsupported data representation template %d", drTemplateOf(m)))
			case errors.As(err, &eus):
				addErr(&mo, "values", fmt.Errorf("unsupported bitmap indicator %d", eus.Indicator))
			default:
				addErr(&mo, "values", err)
			}
		} else {
			mo.ValueCount = len(vals)
			shown := vals
			if nValues < len(vals) {
				shown = vals[:nValues]
			}
			mo.Values = make([]float32Val, len(shown))
			for i, v := range shown {
				mo.Values[i] = float32Val(v)
			}
		}
	}

	// 请求的 section 明细
	for _, sn := range sections {
		mo.SectionsDetail = append(mo.SectionsDetail, sectionDetail(m, sn))
	}
	return mo, nil
}

// drTemplateOf 尽力给出模板号，用于「解码器未配置」的错误信息
// （section 5 缺失时为 0）。
func drTemplateOf(m *grib.Message) int {
	dr, err := m.DataRep()
	if err != nil {
		return 0
	}
	return dr.TemplateNumber
}

// addErr 在消息输出上记录某个组件的失败，采用累加而非覆盖，
// 一条消息的多个问题都会被完整报出。
func addErr(mo *msgOutput, component string, err error) {
	msg := fmt.Sprintf("%s: %v", component, err)
	if mo.Error == "" {
		mo.Error = msg
	} else {
		mo.Error += "; " + msg
	}
}

// tableCode 查代码表条目名，用于展示。
func tableCode(sec, flag, value int) code {
	c := code{Code: value}
	id := fmt.Sprintf("%d.%d", sec, flag)
	if t, ok := spec.TableByID(id); ok {
		if e, ok := t.Lookup(value); ok {
			c.Name = e.Name
		}
	}
	return c
}

// ---------------------------------------------------------------------------
// -section n：带表名的 Walk 逐字段转储
// ---------------------------------------------------------------------------

// sectionDetail 构建单个 section 的逐字段视图。section 3/4/5 有模板，
// 走 spec.Walk 的逐字段转储，并把可解析的码表名一并带出；
// 其余 section 没有模板结构，给出紧凑的固定摘要。
func sectionDetail(m *grib.Message, n int) secOut {
	sec, err := m.Section(n)
	if err != nil {
		var abs *grib.ErrSectionAbsent
		if errors.As(err, &abs) {
			return secOut{Number: n, Note: "section absent from this message"}
		}
		return secOut{Number: n, Note: fmt.Sprintf("error: %v", err)}
	}
	out := secOut{Number: n, Length: sec.Length}

	if m.Edition() == 1 {
		// GRIB1 的段编号与 GRIB2 完全不同（2=GDS、3=BMS、4=BDS、5=结束标记），
		// 而且它是定长字节布局、没有 GRIB2 那种"模板 + Walk"的逐字段转储机制。
		// 所以这里只给每段的角色与规模，逐字节明细请直接看段内容。
		out.Note = grib1SectionNote(n, sec.Data)
		return out
	}

	tmplNo, hasTmpl := templateNumberOf(n, sec.Data)
	if !hasTmpl {
		out.Note = fixedSectionNote(n, sec.Data)
		return out
	}
	td, ok := spec.Template(n, tmplNo)
	if !ok {
		out.Template = &code{Code: tmplNo, Name: "(no spec definition; local or unknown template — register with spec.RegisterLocalTable)"}
		out.Note = "raw section bytes only (Walk unavailable)"
		return out
	}
	out.Template = &code{Code: tmplNo, Name: td.Name}

	w, err := spec.NewWalk(td, sec.Data)
	if err != nil {
		out.Note = fmt.Sprintf("walk error: %v", err)
		return out
	}
	for i := 0; i < w.Len(); i++ {
		f := td.Fields[i]
		v := w.At(i)
		if f.Start < 0 || f.Kind == spec.KindVariable {
			continue // symbolic / unbounded tail: no fixed bytes to show
		}
		fo := fieldOut{Octets: fmt.Sprintf("%d-%d", f.Start+1, f.Start+f.Count)}
		switch f.Kind {
		case spec.KindU8, spec.KindU16, spec.KindU32:
			fo.Raw = v.U
		case spec.KindF32:
			fo.Raw = fmt.Sprintf("%v (0x%s)", v.F, hex.EncodeToString(bitsOf(v)))
		}
		if f.TableSec != 0 || f.TableFlag != 0 {
			id := fmt.Sprintf("%d.%d", f.TableSec, f.TableFlag)
			if t, ok := spec.TableByID(id); ok {
				if e, ok := t.Lookup(int(v.U)); ok {
					fo.Name = e.Name
				}
			}
		}
		out.Fields = append(out.Fields, fo)
	}
	return out
}

// levelLine 渲染单个垂直层描述符。层类型 0（未定义）、1（地面/水面）、
// 2（平均海平面）在模板 1.0 中没有缩放值 —— 随后的字节是保留位 —— 所以
// 不给它们显示 scale（根包仍会报告那些字节）。
func levelLine(l *lvlOut) string {
	if l.Type.Code == 0 || l.Type.Code == 1 || l.Type.Code == 2 || l.Type.Code == 255 {
		return "type " + renderCode(l.Type) + " (no value)"
	}
	return fmt.Sprintf("type %s (value %g)", renderCode(l.Type), l.Scale)
}

// templateNumberOf 返回携带模板号的 section 的模板编号
// （3：octets 13-14，4：octets 8-9，5：octets 10-11）；否则 ok=false。
func templateNumberOf(n int, data []byte) (int, bool) {
	switch n {
	case 3:
		if len(data) < 14 {
			return 0, false
		}
		return int(binary.BigEndian.Uint16(data[12:14])), true
	case 4:
		if len(data) < 9 {
			return 0, false
		}
		return int(binary.BigEndian.Uint16(data[7:9])), true
	case 5:
		if len(data) < 11 {
			return 0, false
		}
		return int(binary.BigEndian.Uint16(data[9:11])), true
	default:
		return 0, false
	}
}

// fixedSectionNote 给无模板的 section 输出一行摘要。
func fixedSectionNote(n int, data []byte) string {
	switch n {
	case 0:
		if len(data) < 16 {
			return "message header (truncated)"
		}
		// 总长度是 octets 9-16 的 8 字节字段（§5.1；eccodes 的
		// section.0.def：section_length[8]）—— 读完整的 U64，这样两种
		// 取值摆放（上半/下半）都能正确显示。
		total := binary.BigEndian.Uint64(data[8:16])
		return fmt.Sprintf("message header: discipline=%d edition=%d totalLength=%d", data[6], data[7], total)
	case 1:
		if len(data) < 21 {
			return "identification (truncated)"
		}
		centre := binary.BigEndian.Uint16(data[5:7])
		sub := binary.BigEndian.Uint16(data[7:9])
		return fmt.Sprintf("identification: centre=%d subcentre=%d", centre, sub)
	case 2:
		return "local use (no template)"
	case 6:
		if len(data) < 6 {
			return "bitmap section (truncated)"
		}
		return fmt.Sprintf("bitmap: indicator=%d (0=bitmap present, 1/255=no bitmap)", data[5])
	case 7:
		return fmt.Sprintf("data: %d payload octets (octet 6 onward)", len(data)-5)
	default:
		return "unknown section"
	}
}

// bitsOf 返回存于 Value.F 的 float32 的 IEEE 754 位模式。
func bitsOf(v spec.Value) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, math.Float32bits(v.F))
	return b
}

// ---------------------------------------------------------------------------
// 人类可读渲染
// ---------------------------------------------------------------------------

func renderMessage(mo msgOutput) string {
	var b []byte
	w := func(format string, a ...any) { b = append(b, []byte(fmt.Sprintf(format, a...))...) }

	w("message %d @ offset %d (size %d bytes, edition %d)\n", mo.Index, mo.Offset, mo.Size, mo.Edition)
	w("  discipline: %s\n", renderCode(mo.Discipline))
	id := mo.ID
	w("  centre: %s (subcentre %d), master table v%d, local v%d\n", renderCode(id.Centre), id.SubCentre, id.MasterTableVer, id.LocalTableVer)
	w("  reference time: %s UTC; production status: %s; type of processed data: %s\n",
		id.ReferenceTime, renderCode(id.ProductionStatus), renderCode(id.TypeOfProcData))
	w("  sections: %v\n", mo.Sections)

	if g := mo.Grid; g != nil && g.Template.Code >= 0 {
		w("  grid: template %d %s, shape %s, %d points", g.Template.Code, orEmpty(g.Template.Name), renderCode(g.Shape), g.Points)
		if g.QuasiRegular {
			w(" (quasi-regular)")
		}
		if g.Declared > 0 {
			w(", declared %d", g.Declared)
		}
		w("\n")
		if g.FirstPoint != nil && g.LastPoint != nil {
			w("    first point (%.4f, %.4f), last point (%.4f, %.4f)\n",
				g.FirstPoint[0], g.FirstPoint[1], g.LastPoint[0], g.LastPoint[1])
		}
	}

	if p := mo.Product; p != nil {
		if mo.Edition == 1 {
			// GRIB1 的参数只有"指示码"一个维度（没有 GRIB2 的参数类别）。
			w("  product: parameter %d %s", p.Number, orEmpty(p.Parameter.Name))
		} else {
			w("  product: template %d %s, parameter %d.%d %s", p.Template.Code, orEmpty(p.Template.Name), p.Category, p.Number, orEmpty(p.Parameter.Name))
		}
		w(", generating process: %s, NV=%d\n", renderCode(p.GenProcess), p.NV)
		if p.ParameterName != "" && p.ParameterName != p.Parameter.Name {
			// ecCodes' parameterName (selected by the message's own table
			// version) whenever it differs from the always-newest lookup above.
			w("    parameterName: %s\n", p.ParameterName)
		}
		if p.ForecastTime != nil {
			w("    forecast time: %s %s\n", p.ForecastTime.Value, renderCode(p.ForecastTime.Unit))
		}
		if p.Level1 != nil {
			w("    level 1: %s\n", levelLine(p.Level1))
		}
		if p.Level2 != nil {
			w("    level 2: %s\n", levelLine(p.Level2))
		}
	}

	if d := mo.DataRep; d != nil {
		if mo.Edition == 1 {
			w("  data representation: %s, numberOfValues=%d\n", orEmpty(d.Template.Name), d.NumValues)
		} else {
			w("  data representation: template %d %s, numberOfValues=%d\n", d.Template.Code, orEmpty(d.Template.Name), d.NumValues)
		}
	}

	if len(mo.Values) > 0 {
		w("  values (first %d of %d): %v\n", len(mo.Values), mo.ValueCount, mo.Values)
	}
	if len(mo.Values64) > 0 {
		// GRIB1 的数值：float64 原生精度（见 msgOutput.Values64 的说明）。
		w("  values64 (first %d of %d): %v\n", len(mo.Values64), mo.ValueCount, mo.Values64)
	}

	for _, s := range mo.SectionsDetail {
		renderSection(&b, w, s)
	}

	if mo.Error != "" {
		w("  [error] %s\n", mo.Error)
	}
	return string(b)
}

func renderSection(b *[]byte, w func(string, ...any), s secOut) {
	head := fmt.Sprintf("  section %d (length %d)", s.Number, s.Length)
	if s.Template != nil {
		head += fmt.Sprintf(", template %d %s", s.Template.Code, orEmpty(s.Template.Name))
	}
	w("%s\n", head)
	if s.Note != "" {
		w("    %s\n", s.Note)
	}
	for _, f := range s.Fields {
		line := fmt.Sprintf("    octets %-10s = %-24v", f.Octets, f.Raw)
		if f.Name != "" {
			line += "  (" + f.Name + ")"
		}
		w("%s\n", line)
	}
}

func renderCode(c code) string {
	if c.Name != "" {
		return fmt.Sprintf("%d (%s)", c.Code, c.Name)
	}
	return fmt.Sprint(c.Code)
}

func orEmpty(s string) string {
	if s == "" {
		return ""
	}
	return "[" + s + "]"
}

// grib1SectionNote 给 GRIB1 的每个段一句摘要（段号语义见 grib1 包的常量）。
func grib1SectionNote(n int, data []byte) string {
	switch n {
	case 0:
		return "indicator section (IS): 'GRIB' + 3-octet total length + edition"
	case 1:
		return "product definition section (PDS)"
	case 2:
		if len(data) >= 6 {
			return fmt.Sprintf("grid description section (GDS): data representation type %d (%s)",
				data[5], orEmpty(grib1.GridTypeName(int(data[5]))))
		}
		return "grid description section (GDS)"
	case 3:
		return "bitmap section (BMS)"
	case 4:
		return "binary data section (BDS)"
	case 5:
		return "end section ('7777')"
	default:
		return "unknown section"
	}
}
