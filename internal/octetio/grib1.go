package octetio

// 本文件是 GRIB edition 1 专用的标量编码。放在 octetio 而不是 grib1 包里，是因为
// grid 包解析 GRIB1 的 GDS 时也要用它们，而 grid 不能反向依赖 grib1（grib1 依赖
// grid 来生成坐标，反过来会成环）。
//
// 这些函数都接收已经切好的定长字节（调用方负责长度），长度不足时返回零值 ——
// 调用方的上界检查在更外层做（grib1.reader 的 octet 编号检查、grid 的段长检查）。

// IBMFloat32 把 4 字节的 IBM 单精度浮点解成 float64。
//
// GRIB1 的 BDS 参考值 R 与旋转网格的旋转角用它；GRIB2 已全部改用 IEEE，所以这是
// GRIB1 独有的编码。
//
// 布局：
//
//	bit  31      符号（1 = 负）
//	bits 30-24   指数，余 64，基 16
//	bits 23-0    尾数，视作 0.fraction（真实值 = mantissa / 2^24）
//
//	value = (-1)^sign × (mantissa / 2^24) × 16^(exponent − 64)
//
// 基是 **16** 而不是 2、尾数是纯小数 —— 这两点是 GRIB1 里最容易写错的地方。
// 32 位模式全部合法（没有 NaN / Inf），尾数为 0 时结果就是 0。
func IBMFloat32(b []byte) float64 {
	if len(b) < 4 {
		return 0
	}
	mantissa := uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	if mantissa == 0 {
		return 0
	}
	v := float64(mantissa) / (1 << 24) * pow16(int(b[0]&0x7F)-64)
	if b[0]&0x80 != 0 {
		return -v
	}
	return v
}

// pow16 返回 16^n（n ∈ [-64, 63]，全部落在 float64 的正常范围内）。
// 用平方-乘代替 math.Pow：这里是热路径（每个 GRIB1 消息至少一次），而且指数是整数。
func pow16(n int) float64 {
	if n == 0 {
		return 1
	}
	neg := n < 0
	if neg {
		n = -n
	}
	v, base := 1.0, 16.0
	for n > 0 {
		if n&1 == 1 {
			v *= base
		}
		base *= base
		n >>= 1
	}
	if neg {
		return 1 / v
	}
	return v
}

// Smag16 把 2 字节符号-幅度有符号整数解成 int：bit 15 = 符号，低 15 位 = 幅值。
//
// GRIB1 的 `signed[2]`（二进制/十进制比例因子）用它。整个 GRIB 生态都是符号-幅度，
// 这是 ecCodes `grib_decode_signed_long` 的实锤行为，也与本库 GRIB2 侧对 `signed[N]`
// 的口径一致。用补码读会把负数读成大正数。
func Smag16(b []byte) int {
	if len(b) < 2 {
		return 0
	}
	v := uint16(b[0])<<8 | uint16(b[1])
	if v&0x8000 != 0 {
		return -int(v & 0x7FFF)
	}
	return int(v)
}

// Smag24 把 3 字节符号-幅度有符号整数解成 int：bit 23 = 符号，低 23 位 = 幅值。
//
// GRIB1 的 `signed[3]`（经纬度、标准纬线、旋转极点）用它，单位 1/1000 度
// （ecCodes 的 grib1divider = 1000）。
func Smag24(b []byte) int {
	if len(b) < 3 {
		return 0
	}
	v := uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
	if v&0x800000 != 0 {
		return -int(v & 0x7FFFFF)
	}
	return int(v)
}

// U24 读 3 字节无符号大端整数（GRIB1 的段长度与 Dx/Dy 用它）。
func U24(b []byte) uint32 {
	if len(b) < 3 {
		return 0
	}
	return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
}

// U16At 读 b[off:off+2] 的大端无符号整数（0 基偏移）。越界返回 0 —— 调用方负责
// 先做段长检查（grid 与 grib1 都在进入字段读取前校验过最小长度）。
func U16At(b []byte, off int) uint16 {
	if off < 0 || off+2 > len(b) {
		return 0
	}
	return uint16(b[off])<<8 | uint16(b[off+1])
}
