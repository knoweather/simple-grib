package paramdb

import (
	"errors"
	"strconv"

	"github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/spec"
)

// QueryFrom 从一条已解析的 GRIB 消息构造标准查询键集（键名对齐 ecCodes）。
//
// 提供的键（存在的才放进 Map；概念条件引用未提供的键时该条目不命中）：
//
//	GRIB2: discipline, parameterCategory, parameterNumber,
//	       productDefinitionTemplateNumber, typeOfGeneratingProcess,
//	       typeOfStatisticalProcessing（统计模板才有）,
//	       typeOfFirstFixedSurface / scaleFactorOfFirstFixedSurface /
//	       scaledValueOfFirstFixedSurface 及 Second 同名三者（模板有该面才给，
//	       缺失值用 Missing）,
//	       masterTablesVersionNumber, localTablesVersionNumber, centre,
//	       subCentre, pressureUnits（等压面固定为 hPa）, mtg2（概念变体开关）,
//	       is_chemical / is_chemical_distfn / is_chemical_srcsink /
//	       is_aerosol / is_aerosol_optical（每条消息都给 0/1，与 ecCodes 的
//	       section.1 派生键一致）,
//	       constituentType / aerosolType / sourceSinkChemicalPhysicalProcess
//	       以及粒径/波长的 typeOf*Interval + scaleFactor/scaledValue 键
//	       （化学与气溶胶模板才有）,
//	       productionStatusOfProcessedData（第 1 节八位组 20，表 1.3，
//	       用于伪中心数据集选择）,
//	       generation（如存在日历模板号时给出）,
//	       localTablesVersion（第 1 节八位组 11；概念条件用的键名就是这个，
//	       ecmf 的 1200 处本地条目靠它区分）
//	GRIB1: centre, table2Version, indicatorOfParameter,
//	       indicatorOfTypeOfLevel, level（层值缺失时为 Missing）、
//	       timeRangeIndicator；层对类型另给 topLevel/bottomLevel
//
// 暂不提供：mars 命名空间键（marsClass/marsType/marsStream）、卫星板键、
// 参考期模板键、timing 键（lengthOfTimeRange/indicatorOfUnitForTimeRange）。
// 经实证（tmp/probe_era_disambig.py）：ecCodes 对**不带 marsClass** 的消息
// 同样不加载 era/cerise/era6/nextgems 目录，故这些目录不接入本库与 ecCodes
// 行为一致；marsClass 只存在于真实 ECMWF MARS 归档消息（grib2/local.98.def
// 的 2 字节 codetable），需 ECMWF 专有样本方能验证。
//
// 键名注意：ecCodes 的概念条件写 localTablesVersion（见 grib2/section.1.def
// 的 `codetable[1] localTablesVersion`），不是 localTablesVersionNumber。
// 本库曾只提供后者，导致 ecmf（1200 处）与 era 家族的本地条目永不命中、
// 一律回退 master；现已两个键都给。
func QueryFrom(m *grib.Message) (Map, error) {
	if m == nil {
		return nil, errors.New("paramdb: QueryFrom with nil message")
	}
	if m.Edition() == 1 {
		return queryFromGRIB1(m)
	}
	return queryFromGRIB2(m)
}

func queryFromGRIB2(m *grib.Message) (Map, error) {
	id, err := m.Identification()
	if err != nil {
		return nil, err
	}
	p, err := m.Product()
	if err != nil {
		return nil, err
	}
	q := Map{
		"discipline":                      strconv.Itoa(m.Discipline()),
		"parameterCategory":               strconv.Itoa(p.ParameterCategory),
		"parameterNumber":                 strconv.Itoa(p.ParameterNumber),
		"productDefinitionTemplateNumber": strconv.Itoa(p.TemplateNumber),
		"masterTablesVersionNumber":       strconv.Itoa(id.MasterTableVersion),
		// ecCodes 的概念条件用 localTablesVersion（见 grib2/section.1.def 的
		// `codetable[1] localTablesVersion`）。本库曾只给 localTablesVersionNumber，
		// 而概念文件用它 0 次——ecmf（1200 处）、era/cerise/era6/nextgems
		// （共 1896 处）的本地条目因此永不命中、一律回退 master。现两个键都给。
		"localTablesVersion":       strconv.Itoa(id.LocalTableVersion),
		"localTablesVersionNumber": strconv.Itoa(id.LocalTableVersion),
		"centre":                   strconv.Itoa(id.Centre),
		"subCentre":                strconv.Itoa(id.SubCentre),
	}
	// typeOfGeneratingProcess 仅当模板确有 TableRef=4.3 字段：Product 的零值
	// 0 会伪造"生成过程=分析"，错误满足概念里的同值条件。
	if hasGeneratingProcessField(p.TemplateNumber) {
		q["typeOfGeneratingProcess"] = strconv.Itoa(p.TypeOfGeneratingProcess)
	}
	// MTG2 概念变体开关（ecCodes tablesVersionMTG2Switch = 33）：
	// master 表版本 ≤ 33 用 .lte33 概念，≥ 34 用普通概念。
	if id.MasterTableVersion <= 33 {
		q["mtg2"] = "0"
	} else {
		q["mtg2"] = "1"
	}
	addSurfaceKeys(q, "First", p.Level1)
	addSurfaceKeys(q, "Second", p.Level2)
	// 等压面：ecCodes 默认以 hPa 呈现（typeOfLevel = isobaricInhPa），
	// 概念用 pressureUnits 区分 hPa/Pa 变体。
	if p.Level1 != nil && p.Level1.Type == 100 {
		q["pressureUnits"] = "hPa"
	}
	if p.TypeOfStatisticalProcessing >= 0 {
		q["typeOfStatisticalProcessing"] = strconv.Itoa(p.TypeOfStatisticalProcessing)
	}
	// 概率模板（4.5/4.9 及派生）的事件阈值：probabilityType 与上下限的
	// scale factor/scaled value 键——tpg1、cdsn1 这类概率 shortName 的
	// 条件就锚在这些键上。限值缺失（全 1 哨兵）按 Missing 语义提供。
	if p.ProbabilityType >= 0 {
		q["probabilityType"] = strconv.Itoa(p.ProbabilityType)
		addLimitKeys(q, "Lower", p.ProbabilityLower)
		addLimitKeys(q, "Upper", p.ProbabilityUpper)
	}
	// 伪中心数据集选择键：productionStatusOfProcessedData（section 1 octet 20）
	// 和 generation（日历模板号 >0 时）。这两个键让 tigge/s2s/uerra/destine/…
	// 等伪中心目录的条目能正确匹配到目标消息。
	q["productionStatusOfProcessedData"] = strconv.Itoa(id.ProductionStatus)
	if id.Calendar != nil && id.Calendar.TemplateNumber > 0 {
		q["generation"] = strconv.Itoa(id.Calendar.TemplateNumber)
	}
	addChemicalKeys(q, p)
	// MARS 标签（marsClass/marsType/marsStream 及 class/type/stream 别名）。
	// 只有带标签的消息才有这些键；era/hydro/fire/cerise/nextgems 等按 marsClass
	// 圈定的伪中心目录靠它们激活。
	addMarsKeys(q, m)
	return q, nil
}

// addChemicalKeys supplies the chemistry/aerosol flags ecCodes derives from
// productDefinitionTemplateNumber (section.1.def's g2_chemical/g2_aerosol
// metas) and, when the template carries them, the section-4 constituent,
// source/sink and size/wavelength keys.
func addChemicalKeys(q Map, p *grib.Product) {
	t := p.TemplateNumber
	q["is_chemical"] = boolKey(t == 40 || t == 41 || t == 42 || t == 43)
	q["is_chemical_distfn"] = boolKey(t == 57 || t == 58 || t == 67 || t == 68)
	q["is_chemical_srcsink"] = boolKey(t == 76 || t == 77 || t == 78 || t == 79)
	q["is_aerosol"] = boolKey(t == 44 || t == 45 || t == 46 || t == 47 ||
		t == 48 || t == 49 || t == 50 || t == 85)
	q["is_aerosol_optical"] = boolKey(t == 48 || t == 49)

	c := p.Chemical
	if c == nil {
		return
	}
	if c.ConstituentType >= 0 {
		q["constituentType"] = strconv.Itoa(c.ConstituentType)
	}
	if c.AerosolType >= 0 {
		q["aerosolType"] = strconv.Itoa(c.AerosolType)
	}
	if c.SourceSinkChemicalPhysicalProcess >= 0 {
		q["sourceSinkChemicalPhysicalProcess"] = strconv.Itoa(c.SourceSinkChemicalPhysicalProcess)
	}
	if c.TypeOfSizeInterval >= 0 {
		q["typeOfSizeInterval"] = strconv.Itoa(c.TypeOfSizeInterval)
	}
	addScaledField(q, "scaleFactorOfFirstSize", c.ScaleFactorOfFirstSize)
	addScaledField(q, "scaledValueOfFirstSize", c.ScaledValueOfFirstSize)
	addScaledField(q, "scaleFactorOfSecondSize", c.ScaleFactorOfSecondSize)
	addScaledField(q, "scaledValueOfSecondSize", c.ScaledValueOfSecondSize)
	if c.TypeOfWavelengthInterval >= 0 {
		q["typeOfWavelengthInterval"] = strconv.Itoa(c.TypeOfWavelengthInterval)
	}
	addScaledField(q, "scaleFactorOfFirstWavelength", c.ScaleFactorOfFirstWavelength)
	addScaledField(q, "scaledValueOfFirstWavelength", c.ScaledValueOfFirstWavelength)
	addScaledField(q, "scaleFactorOfSecondWavelength", c.ScaleFactorOfSecondWavelength)
	addScaledField(q, "scaledValueOfSecondWavelength", c.ScaledValueOfSecondWavelength)
}

func boolKey(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func addScaledField(q Map, key string, f grib.ScaledField) {
	if !f.Valid {
		q[key] = Missing
		return
	}
	q[key] = strconv.Itoa(f.Value)
}

// addLimitKeys 填概率阈值键对（scaleFactorOfLowerLimit / scaledValueOfLowerLimit
// 及 Upper 同名），缺失哨兵按 Missing 语义。
func addLimitKeys(q Map, which string, l *grib.ProbabilityLimit) {
	if l == nil {
		return
	}
	const missingScaleByte = 0xFF
	const missingScaledValue = 0xFFFFFFFF
	if l.ScaleFactorRaw == missingScaleByte {
		q["scaleFactorOf"+which+"Limit"] = Missing
	} else {
		q["scaleFactorOf"+which+"Limit"] = strconv.Itoa(spec.Signed8(l.ScaleFactorRaw))
	}
	if l.ScaledValueRaw == missingScaledValue {
		q["scaledValueOf"+which+"Limit"] = Missing
	} else {
		q["scaledValueOf"+which+"Limit"] = strconv.FormatUint(uint64(l.ScaledValueRaw), 10)
	}
}

func queryFromGRIB1(m *grib.Message) (Map, error) {
	g1, ok := m.GRIB1()
	if !ok {
		return nil, errors.New("paramdb: edition-1 message without a GRIB1 view")
	}
	prod := g1.Product()
	id := g1.Identification()
	q := Map{
		"centre":                 strconv.Itoa(id.Centre),
		"table2Version":          strconv.Itoa(id.TableVersion),
		"indicatorOfParameter":   strconv.Itoa(prod.Parameter),
		"indicatorOfTypeOfLevel": strconv.Itoa(prod.LevelType),
		"timeRangeIndicator":     strconv.Itoa(prod.TimeRangeIndicator),
	}
	if prod.LevelMissing {
		q["level"] = Missing
	} else {
		q["level"] = strconv.Itoa(prod.LevelTop)
		// 层对类型（101/104/…）另有 topLevel/bottomLevel 两个键；单层不提供，
		// 与 ecCodes 的逐键视图一致。
		if prod.LayerRange {
			q["topLevel"] = strconv.Itoa(prod.LevelTop)
			q["bottomLevel"] = strconv.Itoa(prod.LevelBottom)
		}
	}
	// GRIB1 的 MARS 标签也只有 ECMWF 本地定义（grib1/mars_labeling.def）才有。
	addMarsKeys(q, m)
	return q, nil
}

// addSurfaceKeys 填一个固定面的 ecCodes 键三元组：面类型总是给（面存在即有），
// scale factor 与 scaled value 按 ecCodes 语义逐键判缺失——二者是相互独立的
// 键，混用"解码值"会匹配错条件（(0,2) 与 (1,20) 解码同为 2.0）。
func addSurfaceKeys(q Map, which string, l *grib.Level) {
	if l == nil {
		return // 模板没有这个面：键不存在，条件引用它就不命中（与 ecCodes 一致）
	}
	q["typeOf"+which+"FixedSurface"] = strconv.Itoa(l.Type)
	const missingScaleByte = 0xFF         // WMO Note 168 / ecCodes can_be_missing
	const missingScaledValue = 0xFFFFFFFF // 同上
	if l.ScaleFactorRaw == missingScaleByte {
		q["scaleFactorOf"+which+"FixedSurface"] = Missing
	} else {
		q["scaleFactorOf"+which+"FixedSurface"] = strconv.Itoa(spec.Signed8(l.ScaleFactorRaw))
	}
	if l.ScaledValueRaw == missingScaledValue {
		q["scaledValueOf"+which+"FixedSurface"] = Missing
	} else {
		q["scaledValueOf"+which+"FixedSurface"] = strconv.FormatUint(uint64(l.ScaledValueRaw), 10)
	}
}

// hasGeneratingProcessField 检查产品模板是否带 TableRef=4.3 的字段。
func hasGeneratingProcessField(templateNumber int) bool {
	def, ok := spec.Template(4, templateNumber)
	if !ok {
		return false
	}
	for _, f := range def.Fields {
		if f.TableSec == 4 && f.TableFlag == 3 {
			return true
		}
	}
	return false
}
