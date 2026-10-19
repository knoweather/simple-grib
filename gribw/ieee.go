package gribw

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/knoweather/simple-grib/spec"
)

// IEEEParams 保存数据表示模板 5.4（IEEE 浮点）的参数。仅支持
// Precision=1（binary32）：写入端只发出 precision=1，遇到其他值立即报错，
// 与读取端支持的范围对称。
//
// IEEEParams holds the parameters of data representation template 5.4 (IEEE
// floating point). Only Precision=1 (binary32) is supported: the writer
// emits precision=1 only and fails loud on any other value, symmetric with the
// reader's supported range.
type IEEEParams struct {
	Precision int // octet 12，表 5.7：1 = binary32（写入端唯一支持的值）/ octet 12, table 5.7: 1 = binary32 (the only value the writer supports)
}

// EncodeIEEE 用 GRIB2 模板 5.4 打包数据，返回第 5 节头与第 7 节数据两段独立
// 切片——与 EncodeSimple 相同的可检视 API 形状。payload 是 numValues × 4
// 字节的大端 IEEE 754 binary32，因此 NaN/Inf/非规格化数都能按位往返（这正是
// 模板 5.4 相对 simple packing 的意义）。不做值域检查：每一个 float32 位
// 模式都可在 payload 中表示。
//
// EncodeIEEE packs values with GRIB2 template 5.4 and returns the section 5
// header and the section 7 data as separate slices — the same inspectable API
// shape as EncodeSimple. The payload is numValues × 4 bytes of big-endian IEEE
// 754 binary32, so NaN/Inf/denormals round-trip bit-for-bit (which is the point
// of template 5.4 over simple packing). No value-range checks: every float32
// bit pattern is representable in the payload.
func EncodeIEEE(values []float32, p IEEEParams) (sec5, payload []byte, err error) {
	if p.Precision != 1 {
		return nil, nil, fmt.Errorf("gribw: IEEE packing: precision=%d not supported in (§5.13 writer clause: binary32 only)", p.Precision)
	}

	payload = make([]byte, len(values)*4)
	for i, v := range values {
		binary.BigEndian.PutUint32(payload[i*4:], math.Float32bits(v))
	}

	def, ok := spec.Template(5, 4)
	if !ok {
		return nil, nil, fmt.Errorf("gribw: IEEE packing: no spec definition for template 5.4")
	}
	sec5 = make([]byte, 12) // 模板 5.4 最小长度（precision 止于 octet 12）/ template 5.4 minimum length (precision ends at octet 12)
	binary.BigEndian.PutUint32(sec5[0:4], 12)
	sec5[4] = 5
	binary.BigEndian.PutUint32(sec5[5:9], uint32(len(values)))      // 值个数 / octets 6-9: numberOfValues
	binary.BigEndian.PutUint16(sec5[9:11], 4)                       // 模板号 4 / octets 10-11: template number 4
	if err := setU8(def, sec5, 12, byte(p.Precision)); err != nil { // octet 12：精度（表 5.7）/ precision (table 5.7)
		return nil, nil, err
	}
	return sec5, payload, nil
}
