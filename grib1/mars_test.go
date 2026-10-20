package grib1

import "testing"

// marsPDS 造一个带本地定义区的 PDS（section 1）。索引=octet-1，与 ecCodes 的
// octet 编号一一对应。
func marsPDS(centre, subCentre, localDefinition, class, typ int, stream uint16, expver string) Section {
	pds := make([]byte, 60)
	putU24(pds[0:], len(pds)) // octets 1-3: 长度
	pds[3] = 3                // octet 4: 表 2 版本
	pds[4] = byte(centre)     // octet 5: 中心
	pds[6] = 255              // octet 7: grid id = 见 section 2
	pds[8] = 11               // octet 9: 参数
	pds[9] = 1                // octet 10: 层次类型
	putU16(pds[10:], 0)       // octets 11-12: 层次值
	pds[12] = 25              // octet 13: 年
	pds[13] = 12              // octet 14: 月
	pds[14] = 29              // octet 15: 日
	pds[15] = 0               // octet 16: 时
	pds[16] = 0               // octet 17: 分
	pds[17] = 1               // octet 18: 时间单位
	pds[18] = 0               // octet 19: P1
	pds[19] = 0               // octet 20: P2
	pds[20] = 1               // octet 21: 时间范围指示
	putU16(pds[21:], 0)       // octets 22-23
	pds[23] = 0               // octet 24
	pds[24] = 21              // octet 25: 世纪
	pds[25] = byte(subCentre) // octet 26: 子中心
	putSmag16(pds[26:], 0)    // octets 27-28: 十进制比例因子
	// octets 29-40 保留；octet 41 本地定义号，octets 42+ 为本地定义内容。
	pds[40] = byte(localDefinition)
	pds[41] = byte(class)
	pds[42] = byte(typ)
	putU16(pds[43:], int(stream))
	copy(pds[45:49], expver)
	return Section{Number: SectionPDS, Length: len(pds), Data: pds}
}

// TestMarsLabellingGRIB1 钉住 GRIB1 侧 MARS 标签的取值与门控。
//
// 门控依据见 mars.go 与 mars_gen.go：
//   - grib1/section.1.def:248-277 只有 centre 或 subCentre 为 ECMWF(98) 时才按
//     grib1/local.98.[localDefinitionNumber].def 取模板；
//   - 这些模板里只有 43 个 template 了 grib1/mars_labeling.def，那里才声明
//     marsClass/marsType/marsStream/experimentVersionNumber。
func TestMarsLabellingGRIB1(t *testing.T) {
	cases := []struct {
		name            string
		centre          int
		subCentre       int
		localDefinition int
		wantOK          bool
		want            [3]int
	}{
		{name: "local definition 30 declares MARS labelling", centre: 98, localDefinition: 30, wantOK: true, want: [3]int{22, 4, 1}},
		{name: "local definition 1 declares MARS labelling", centre: 98, localDefinition: 1, wantOK: true, want: [3]int{22, 4, 1}},
		{name: "local definition 218 declares MARS labelling", centre: 98, localDefinition: 218, wantOK: true, want: [3]int{22, 4, 1}},
		{name: "subCentre ECMWF takes the same branch", centre: 7, subCentre: 98, localDefinition: 30, wantOK: true, want: [3]int{22, 4, 1}},
		{name: "local definition 244 has no mars_labeling", centre: 98, localDefinition: 244, wantOK: false},
		{name: "local definition 245 has no mars_labeling", centre: 98, localDefinition: 245, wantOK: false},
		{name: "local definition 192 uses this* keys, not marsClass", centre: 98, localDefinition: 192, wantOK: false},
		{name: "unlisted local definition number", centre: 98, localDefinition: 99, wantOK: false},
		{name: "non-ECMWF centre has no MARS labelling", centre: 82, localDefinition: 30, wantOK: false},
		{name: "non-ECMWF centre and subCentre", centre: 34, localDefinition: 1, wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sec := marsPDS(tc.centre, tc.subCentre, tc.localDefinition, 22, 4, 1, "0001")
			id, _, err := parsePDS(sec)
			if err != nil {
				t.Fatalf("parsePDS: %v", err)
			}
			if id.HasMars != tc.wantOK {
				t.Fatalf("HasMars = %v, want %v (LocalDefinition=%d centre=%d subCentre=%d)",
					id.HasMars, tc.wantOK, id.LocalDefinition, id.Centre, id.SubCentre)
			}
			if !tc.wantOK {
				if id.MarsClass != 0 || id.MarsType != 0 || id.MarsStream != 0 {
					t.Errorf("MARS fields must stay zero without labelling, got %d/%d/%d",
						id.MarsClass, id.MarsType, id.MarsStream)
				}
				return
			}
			if got := [3]int{id.MarsClass, id.MarsType, id.MarsStream}; got != tc.want {
				t.Errorf("MARS class/type/stream = %v, want %v", got, tc.want)
			}
			if id.ExperimentVersionNumber != "0001" {
				t.Errorf("ExperimentVersionNumber = %q, want %q", id.ExperimentVersionNumber, "0001")
			}
		})
	}
}

// TestMarsLabellingGRIB1ShortLocalDefinition 确认 PDS 恰好 40 字节（无本地定义）
// 时不越界、也不给标签。
func TestMarsLabellingGRIB1ShortLocalDefinition(t *testing.T) {
	sec := marsPDS(98, 0, 30, 22, 4, 1, "0001")
	sec.Data = sec.Data[:40]
	sec.Length = 40
	id, _, err := parsePDS(sec)
	if err != nil {
		t.Fatalf("parsePDS: %v", err)
	}
	if id.LocalDefinition != -1 {
		t.Errorf("LocalDefinition = %d, want -1 when the PDS carries no local definition", id.LocalDefinition)
	}
	if id.HasMars {
		t.Error("HasMars = true, want false without a local definition")
	}
}
