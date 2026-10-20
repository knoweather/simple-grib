package grib

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// sec2Mars 造一个 section 2：octets 1-4 长度、octet 5 节号、octets 6-7
// grib2LocalSectionNumber，随后就是 grib2/mars_labeling.def 的四个键
// （marsClass/marsType/marsStream 各 2 字节，experimentVersionNumber 4 字节）。
func sec2Mars(localSectionNumber, class, typ, stream uint16, expver string) []byte {
	b := make([]byte, 17)
	binary.BigEndian.PutUint32(b[0:4], uint32(len(b)))
	b[4] = 2
	binary.BigEndian.PutUint16(b[5:7], localSectionNumber)
	binary.BigEndian.PutUint16(b[7:9], class)
	binary.BigEndian.PutUint16(b[9:11], typ)
	binary.BigEndian.PutUint16(b[11:13], stream)
	copy(b[13:17], expver)
	return b
}

// buildGRIB2WithSection2 拼一条完整的 GRIB2 消息，sec2 为 nil 时省略第 2 节。
func buildGRIB2WithSection2(centre int, sec2 []byte) []byte {
	sec1 := make([]byte, 21)
	binary.BigEndian.PutUint32(sec1[0:4], 21)
	sec1[4] = 1
	binary.BigEndian.PutUint16(sec1[5:7], uint16(centre))
	binary.BigEndian.PutUint16(sec1[12:14], 2026)
	sec1[14], sec1[15] = 1, 1 // month, day

	sec3 := make([]byte, 72)
	binary.BigEndian.PutUint32(sec3[0:4], 72)
	sec3[4] = 3
	sec3[14] = 6 // shape 6: R = 6371229 m
	binary.BigEndian.PutUint32(sec3[30:34], 3)
	binary.BigEndian.PutUint32(sec3[34:38], 3)
	binary.BigEndian.PutUint32(sec3[42:46], 0xFFFFFFFF)
	binary.BigEndian.PutUint32(sec3[46:50], 60_000_000)
	binary.BigEndian.PutUint32(sec3[55:59], 40_000_000)
	binary.BigEndian.PutUint32(sec3[59:63], 20_000_000)
	binary.BigEndian.PutUint32(sec3[63:67], 10_000_000)
	binary.BigEndian.PutUint32(sec3[67:71], 10_000_000)

	sec7 := make([]byte, 5)
	binary.BigEndian.PutUint32(sec7[0:4], 5)
	sec7[4] = 7

	body := append([]byte{}, sec1...)
	body = append(body, sec2...)
	body = append(body, sec3...)
	body = append(body, sec4Template0(0, 0, 0, 0)...)
	body = append(body, sec5Simple0(0)...)
	body = append(body, sec7...)

	total := 16 + len(body) + 4
	out := make([]byte, 16, total)
	copy(out[0:4], "GRIB")
	out[7] = 2
	binary.BigEndian.PutUint64(out[8:16], uint64(total))
	out = append(out, body...)
	return append(out, '7', '7', '7', '7')
}

// TestMarsLabellingGRIB2 钉住 GRIB2 侧 MARS 标签的取值与门控。
//
// 门控依据见 mars.go 与 mars_gen.go：只有 grib2/local.<centre>.def 这条模板链
// 声明了 marsClass 的中心（当前就是 98）且 grib2LocalSectionNumber != 0 时才有
// 标签；centre 82 走的是 grib2/mars_labeling.82.def，那里只有别名、没有
// marsClass，所以不给标签。
func TestMarsLabellingGRIB2(t *testing.T) {
	cases := []struct {
		name   string
		centre int
		sec2   []byte
		wantOK bool
		want   MarsLabelling
	}{
		{
			name:   "ECMWF local definition declares MARS labelling",
			centre: 98,
			sec2:   sec2Mars(1, 22, 4, 1, "0001"),
			wantOK: true,
			want:   MarsLabelling{Class: 22, Type: 4, Stream: 1, ExperimentVersionNumber: "0001"},
		},
		{
			name:   "local section number 0 means no local definition",
			centre: 98,
			sec2:   sec2Mars(0, 22, 4, 1, "0001"),
			wantOK: false,
		},
		{
			name:   "centre 82 has no marsClass in its labelling template",
			centre: 82,
			sec2:   sec2Mars(1, 22, 4, 1, "0001"),
			wantOK: false,
		},
		{
			name:   "no section 2 at all",
			centre: 98,
			sec2:   nil,
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := openMessage(t, buildGRIB2WithSection2(tc.centre, tc.sec2))
			got, ok := m.MarsLabelling()
			if ok != tc.wantOK {
				t.Fatalf("MarsLabelling ok = %v, want %v (got %+v)", ok, tc.wantOK, got)
			}
			if !tc.wantOK {
				if got != nil {
					t.Fatalf("MarsLabelling = %+v, want nil when absent", got)
				}
				return
			}
			if *got != tc.want {
				t.Errorf("MarsLabelling = %+v, want %+v", *got, tc.want)
			}
		})
	}
}

// TestMarsLabellingGRIB2EditionsAgree 确认 GRIB1 与 GRIB2 的取值口径一致：
// 两边都是 mars/*.table 的整数 code，不是缩写。
func TestMarsLabellingGRIB2ReadsIntegersNotAbbrevs(t *testing.T) {
	m := openMessage(t, buildGRIB2WithSection2(98, sec2Mars(1, 22, 4, 1, "0001")))
	got, ok := m.MarsLabelling()
	if !ok {
		t.Fatal("MarsLabelling: not present")
	}
	// marsClass 22 在 mars/class.table 里是 "e2"；这里必须是 22 这个整数。
	if got.Class != 22 {
		t.Errorf("Class = %d, want the integer code 22 (not an abbreviation string)", got.Class)
	}
	if bytes.Equal([]byte(got.ExperimentVersionNumber), []byte{0, 0, 0, 0}) {
		t.Error("ExperimentVersionNumber was not read")
	}
}
