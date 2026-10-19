package grib

import (
	"fmt"
	"time"

	"github.com/knoweather/simple-grib/internal/octetio"
	"github.com/knoweather/simple-grib/spec"
)

// Calendar 是第 1 节（octet 22 起）携带的可选日历定义。
//
// Calendar is the optional calendar definition carried in section 1
// (octets 22+).
type Calendar struct {
	TemplateNumber int // 模板号 octet 22-23，表 1.5（0=日历定义，2=日历+古气候偏移） / template number octets 22-23, table 1.5 (0 = calendar definition, 2 = calendar + paleoclimate offset)
	TypeOfCalendar int // 模板内容 octet 24，表 1.6（0=Gregorian，1=360 天，…），缺失时为 0 / template content octet 24, table 1.6 (0 = Gregorian, 1 = 360-day, ...); 0 when absent
}

// Identification 是解析后的第 1 节。ReferenceTime 为 UTC 参考时间；
// 对预报产品而言即分析（起报）时间。
//
// Identification is the parsed section 1. ReferenceTime is the reference time
// in UTC; for forecast products it is the analysis (initialization) time.
type Identification struct {
	// Edition 是消息的 GRIB 版本（1 或 2）。GRIB1 按 GRIB1 第 1 节布局解析；
	// ProductionStatus / TypeOfProcessedData 仅属 GRIB2，GRIB1 时为 -1。
	//
	// Edition is the message's GRIB edition (1 or 2). GRIB1 follows the
	// GRIB1 section 1 layout; ProductionStatus / TypeOfProcessedData are
	// GRIB2-only concepts and are -1 for GRIB1.
	Edition             int
	Centre              int       // 中心号 octets 6-7，表 CCT-11 / centre octets 6-7, table CCT-11
	SubCentre           int       // 子中心 octets 8-9 / sub-centre octets 8-9
	CentreName          string    // CCT-11 表中 Centre 的名称；代码不在表中时为 "" / CCT-11 name of Centre; "" when the code is not in the table
	MasterTableVersion  int       // 主表版本 octet 10 / master table version octet 10
	LocalTableVersion   int       // 本地表版本 octet 11 / local table version octet 11
	ReferenceTime       time.Time // UTC / UTC
	ProductionStatus    int       // 生产状态 octet 20，表 1.3（0=业务） / production status octet 20, table 1.3 (0 = operational)
	TypeOfProcessedData int       // 处理数据类型 octet 21，表 1.4（0=分析） / type of processed data octet 21, table 1.4 (0 = analysis)
	Calendar            *Calendar // 第 1 节未携带日历定义时为 nil / nil when section 1 carries no calendar definition
}

// Identification parses section 1. The fixed layout follows the WMO
// template 1.0 definition; the optional calendar part (octets 22+) follows
// table 1.5. The reference time is validated before use so time.Time
// normalization can never silently shift an out-of-range date.
func (m *Message) Identification() (*Identification, error) {
	if m.g1 != nil {
		return identificationFromGRIB1(m.g1), nil
	}
	sec, err := m.Section(1)
	if err != nil {
		return nil, err
	}
	r := octetio.New(sec.Data)
	centre, _ := r.U16(5)    // 第 6-7 octet / octets 6-7
	subCentre, _ := r.U16(7) // 第 8-9 octet / octets 8-9
	masterVer, _ := r.U8(9)  // 第 10 octet / octet 10
	localVer, _ := r.U8(10)  // 第 11 octet / octet 11
	year, _ := r.U16(12)     // 第 13-14 octet / octets 13-14
	month, _ := r.U8(14)
	day, _ := r.U8(15)
	hour, _ := r.U8(16)
	minute, _ := r.U8(17)
	second, _ := r.U8(18)
	prodStatus, _ := r.U8(19)
	typeProc, _ := r.U8(20)

	if month < 1 || month > 12 {
		return nil, fmt.Errorf("grib: section 1: invalid reference time month %d", int(month))
	}
	t := time.Date(int(year), time.Month(month), int(day), int(hour), int(minute), int(second), 0, time.UTC)
	if t.Month() != time.Month(month) || t.Day() != int(day) || t.Hour() != int(hour) ||
		t.Minute() != int(minute) || t.Second() != int(second) {
		return nil, fmt.Errorf("grib: section 1: invalid reference time %04d-%02d-%02d %02d:%02d:%02d",
			int(year), int(month), int(day), int(hour), int(minute), int(second))
	}

	id := &Identification{
		Edition:             2,
		Centre:              int(centre),
		SubCentre:           int(subCentre),
		MasterTableVersion:  int(masterVer),
		LocalTableVersion:   int(localVer),
		ReferenceTime:       t,
		ProductionStatus:    int(prodStatus),
		TypeOfProcessedData: int(typeProc),
	}
	if tbl, ok := spec.TableByID("CCT-11"); ok {
		if e, ok := tbl.Lookup(int(centre)); ok {
			id.CentreName = e.Name
		}
	}

	// Optional calendar definition: octets 22-23 (table 1.5), content from
	// octet 24.
	if len(sec.Data) >= 24 {
		tmplNo, _ := r.U16(21) // octets 22-23
		switch {
		case tmplNo == 0 || tmplNo == 2:
			var calType uint8
			if len(sec.Data) >= 25 {
				calType, _ = r.U8(23) // templates 1.0/1.2 octet 24 (table 1.6)
			}
			id.Calendar = &Calendar{TemplateNumber: int(tmplNo), TypeOfCalendar: int(calType)}
		case tmplNo == 1 || (tmplNo >= 32768 && tmplNo <= 65534):
			// Paleoclimate-offset template or local use: carries no
			// Gregorian calendar type, so Calendar stays nil.
		case tmplNo == 65535:
			// "Missing": there is no calendar definition.
		default:
			return nil, fmt.Errorf("grib: section 1: reserved identification template number %d", int(tmplNo))
		}
	}
	return id, nil
}
