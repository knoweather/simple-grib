package grib1

// MARS 标签（ECMWF 的 mars_labeling.def）在 PDS 的本地定义区，只有 centre 或
// subCentre 为 ECMWF、且该本地定义确实声明了它时才存在。判定与 ecCodes 一致：
//
//   - grib1/section.1.def:248-277 —— section1Length > 40 才算有本地定义；
//     且只有 centre == ECMWF 或 subCentre == ECMWF 才按
//     grib1/local.98.[localDefinitionNumber].def 取模板。其它中心走
//     grib1/local.[centre].def，那些模板不声明 MARS 键。
//   - 这些模板里只有一部分 template 了 grib1/mars_labeling.def，那才是声明
//     marsClass/marsType/marsStream/experimentVersionNumber 的地方；集合见
//     mars_gen.go（由 cmd/paramgen 逐个模板解析引用导出）。
//
// 反例：grib1/local.98.192.def 声明的是 thisMarsClass 等带前缀的键，没有
// marsClass，所以不在集合里。

// ecmfCentre 是 ECMWF 的中心号（CCT-1 第 98 号）。
const ecmfCentre = 98

// marsClassOctet 是 MARS 标签在 PDS 里的起始 octet。
//
// ecCodes 把本地定义号放在 octet 41，随后铺开 grib1/local.98.<N>.def 的模板
// 内容。在这 43 个声明了 MARS 的模板里，grib1/mars_labeling.def 都是第一个
// 定长字段——它前面只有 constant / transient / alias，三者都不占 section 字节
// （可直接在 paramdata/grib1/local.98.*.def 里逐条核对），因此 marsClass 恒在
// octet 42。
//
// 其后依次是 grib1/mars_labeling.def 的固定布局：
//
//	octet 42      marsClass               codetable[1]
//	octet 43      marsType                codetable[1]
//	octets 44-45  marsStream              codetable[2]
//	octets 46-49  experimentVersionNumber ksec1expver[4]
const marsClassOctet = 42

// parseMarsLabelling 读本地定义区的 MARS 标签并写入 id。
//
// 该本地定义不提供标签时直接返回，id 的 MARS 字段保持零值（HasMars 仍为
// false）——调用方据此区分"没有标签"与"标签恰好为零"。
func parseMarsLabelling(r reader, id *Identification) error {
	if !marsHasLabelling(id.Centre, id.SubCentre, id.LocalDefinition) {
		return nil
	}
	class, err := r.u8(marsClassOctet)
	if err != nil {
		return err
	}
	typ, err := r.u8(marsClassOctet + 1)
	if err != nil {
		return err
	}
	stream, err := r.u16(marsClassOctet + 2)
	if err != nil {
		return err
	}
	expver, err := r.slice(marsClassOctet+4, 4)
	if err != nil {
		return err
	}
	id.HasMars = true
	id.MarsClass = int(class)
	id.MarsType = int(typ)
	id.MarsStream = int(stream)
	id.ExperimentVersionNumber = string(expver)
	return nil
}

// marsHasLabelling 报告该消息的本地定义是否提供 MARS 标签。
func marsHasLabelling(centre, subCentre, localDefinition int) bool {
	if centre != ecmfCentre && subCentre != ecmfCentre {
		return false
	}
	return marsLocalDefinitions[localDefinition]
}
