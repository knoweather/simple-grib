package grib

import "github.com/knoweather/simple-grib/internal/octetio"

// MarsLabelling 是 ECMWF 的 MARS 标签（ecCodes 的 mars_labeling.def）：GRIB 里
// 传的是整数 code，ecCodes 的 marsClass/marsType/marsStream 对外另给一个缩写
// token（od/fc/enfo …），由 mars/*.table 映射。
//
// 这些标签只对声明了它们的消息存在，因此 MarsLabelling 返回 ok=false 而不是
// 零值——调用方不能把零值当真实标签用。
//
// MarsLabelling is ECMWF's MARS labelling (ecCodes' mars_labeling.def). It is
// present only for messages whose centre/local-definition combination declares
// it, hence the ok result.
type MarsLabelling struct {
	Class                   int    // marsClass，mars/class.table 的号
	Type                    int    // marsType，mars/type.table 的号
	Stream                  int    // marsStream，mars/stream.table 的号
	ExperimentVersionNumber string // experimentVersionNumber，4 个字符
}

// MarsLabelling 返回消息携带的 MARS 标签。
//
// 是否有标签，判据与 ecCodes 的本地定义分派一致：
//
//	GRIB2 —— grib2/section.2.def 在 grib2LocalSectionNumber != 0 时按
//	  grib2/local.[centreForLocal].def 取模板；只有这些模板（含其 template
//	  引用）声明了 marsClass 码表，集合见 mars_gen.go。
//	GRIB1 —— grib1/section.1.def:248-277 在 centre 或 subCentre 为 ECMWF 时
//	  按 grib1/local.98.[localDefinitionNumber].def 取模板；集合见
//	  grib1/mars_gen.go。判定在 grib1 包内完成。
//
// 字段位置来自 grib2/mars_labeling.def：marsClass / marsType / marsStream 各
// 2 字节大端，experimentVersionNumber 4 字节。section 2 的前 7 个 octet 是
// section 2 自身与 grib2LocalSectionNumber，所以 marsClass 落在 octet 8。
func (m *Message) MarsLabelling() (*MarsLabelling, bool) {
	if m.g1 != nil {
		id := m.g1.Identification()
		if !id.HasMars {
			return nil, false
		}
		return &MarsLabelling{
			Class:                   id.MarsClass,
			Type:                    id.MarsType,
			Stream:                  id.MarsStream,
			ExperimentVersionNumber: id.ExperimentVersionNumber,
		}, true
	}
	sec, err := m.Section(2)
	if err != nil || sec == nil {
		return nil, false
	}
	// section 2 布局（Section.Data[0] = octet 1，故 octet N 在偏移 N-1）：
	//
	//	octets 1-4    section length
	//	octet  5      numberOfSection (2)
	//	octets 6-7    grib2LocalSectionNumber，0 = 无本地定义
	//	octets 8-9    marsClass
	//	octets 10-11  marsType
	//	octets 12-13  marsStream
	//	octets 14-17  experimentVersionNumber
	r := octetio.New(sec.Data)
	lsn, err := r.U16(5) // octets 6-7
	if err != nil || lsn == 0 {
		return nil, false
	}
	id, err := m.Identification()
	if err != nil || id == nil || !marsCentres[id.Centre] {
		return nil, false
	}
	class, err := r.U16(7) // octets 8-9
	if err != nil {
		return nil, false
	}
	typ, err := r.U16(9) // octets 10-11
	if err != nil {
		return nil, false
	}
	stream, err := r.U16(11) // octets 12-13
	if err != nil {
		return nil, false
	}
	expver, err := r.Bytes(13, 4) // octets 14-17
	if err != nil {
		return nil, false
	}
	return &MarsLabelling{
		Class:                   int(class),
		Type:                    int(typ),
		Stream:                  int(stream),
		ExperimentVersionNumber: string(expver),
	}, true
}
