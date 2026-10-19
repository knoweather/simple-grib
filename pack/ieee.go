package pack

import (
	"fmt"
	"math"

	"github.com/knoweather/simple-grib/internal/octetio"
)

// IEEE 持有模板 5.4（IEEE 浮点数据）的头部字段：只有精度字段
// （第 5 段第 12 八位组，码表 5.7）。
//
// IEEE holds the template 5.4 (IEEE floating point data) header fields:
// only the precision field (section 5 octet 12, code table 5.7).
type IEEE struct {
	// Precision 位于第 12 八位组，码表 5.7：1=32 位，2=64 位，3=128 位
	// / octet 12, code table 5.7: 1=32bit, 2=64bit, 3=128bit
	Precision uint8
}

// ieeeDecode 实现 GRIB2 模板 5.4（网格点数据 - IEEE 浮点）：仅支持精度 1
// （binary32），逐点按大端 32 位读取（value = math.Float32frombits(BE32)），
// 共 numValues 个，且要求 len(data) 恰好等于 numValues×4。精度 2/3
// （64/128 位）以 ErrUnsupportedValue 拒绝。
//
// ieeeDecode implements GRIB2 template 5.4 (grid point data - IEEE floating
// point). Only precision 1 (binary32) is supported:
// value = math.Float32frombits(BE32), numValues of them, with len(data)
// required to be exactly numValues×4. Precisions 2/3 (64/128-bit) are
// rejected with ErrUnsupportedValue.
func ieeeDecode(p IEEE, data []byte, numValues int) ([]float32, error) {
	if p.Precision != 1 {
		return nil, &ErrUnsupportedValue{Template: TemplateIEEE, Field: "precision", Value: int64(p.Precision)}
	}
	if len(data) != numValues*4 {
		return nil, fmt.Errorf("pack: template 5.4: data length %d != numValues×4 = %d", len(data), numValues*4)
	}
	out := make([]float32, numValues)
	r := octetio.New(data)
	for i := 0; i < numValues; i++ {
		u, err := r.U32(i * 4) // cannot fail: the length was validated above
		// 不会失败：长度已在上面校验过 / cannot fail: the length was validated above
		if err != nil {
			return nil, err
		}
		out[i] = math.Float32frombits(u)
	}
	return out, nil
}
