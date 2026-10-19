package gribw1

import "github.com/knoweather/simple-grib/grib1"

import (
	"fmt"
	"math"
)

// PDS 段 1 octet 8 的标志位（与 grib1 的 pdsFlagGDS/BMS 同定义）。
const (
	pdsFlagGDS = 0x80 // 位 1：有段 2（GDS）
	pdsFlagBMS = 0x40 // 位 2：有段 3（BMS）
)

// PDS 段 1 的长度选择：28 字节固定部分 + 可选的 12 字节保留区（octets 29-40）
// + 可选的本地定义（octet 41 起，本包只写 1 字节号）。ecCodes 对"无附加内容"的
// PDS 一律写 28。
const (
	pdsBaseLen      = 28
	pdsReservedFrom = 29 // octets 29-40 保留
	pdsMaxLen       = 41 // octet 41 = 本地定义号（本包最多写到这里）
)

// encodePDS 组装段 1。PDS octet 8 的标志按实际写出的段置位（有 GDS 必有 0x80；
// 有位图段才有 0x40 —— Encode 在"全 true 位图"时会省掉段 3，标志据此为 0）。
func (p *Params) encodePDS(pl plan) ([]byte, error) {
	length := pdsBaseLen
	if len(p.Octets29To40) > 0 {
		length = pdsBaseLen + len(p.Octets29To40)
	}

	flags := uint8(pdsFlagGDS) // 本包总是写 GDS
	if p.Bitmap != nil && !allTrue(p.Bitmap) {
		flags |= pdsFlagBMS
	}

	b := make([]byte, length)
	putU24(b[0:], length)
	b[3] = byte(p.TableVersion) // octet 4
	b[4] = byte(p.Centre)       // octet 5
	b[5] = byte(p.GeneratingProcess)
	b[6] = 255 // octet 7：网格由段 2 定义
	b[7] = flags
	b[8] = byte(p.Parameter) // octet 9
	b[9] = byte(p.LevelType) // octet 10

	// 层值：层组类型两个 1 字节，其余一个 2 字节。
	if grib1.IsLayerRangeLevelType(p.LevelType) {
		b[10] = byte(p.LevelTop)    // octet 11 = 上界
		b[11] = byte(p.LevelBottom) // octet 12 = 下界
	} else {
		putU16(b[10:], p.LevelTop) // octets 11-12
	}

	// 参考时间：octets 13-17 = 世纪内年份/月/日/时/分，octet 25 = 世纪。
	// 世纪换算与 grib1 解码器互为逆运算：century = year/100 + 1。
	t := p.ReferenceTime.UTC()
	if t.Year() < 1 || t.Year() > 9999 {
		return nil, fmt.Errorf("gribw1: 参考时间年份 %d 超出 GRIB1 表示范围（1..9999）", t.Year())
	}
	year := t.Year()
	b[12] = byte(year % 100)     // octet 13
	b[13] = byte(int(t.Month())) // octet 14
	b[14] = byte(t.Day())        // octet 15
	b[15] = byte(t.Hour())       // octet 16
	b[16] = byte(t.Minute())     // octet 17
	b[24] = byte(year/100 + 1)   // octet 25：世纪（21 世纪 = 21）

	b[17] = byte(p.TimeUnit)           // octet 18
	b[18] = byte(p.P1)                 // octet 19
	b[19] = byte(p.P2)                 // octet 20
	b[20] = byte(p.TimeRangeIndicator) // octet 21
	putU16(b[21:], 0)                  // octets 22-23：参与平均的个数（本包不写气候平均）
	b[23] = 0                          // octet 24：缺测个数
	b[25] = byte(p.SubCentre)          // octet 26
	putSmag16(b[26:], pl.d)            // octets 27-28：与段 4 同一个 D

	copy(b[pdsReservedFrom-1:], p.Octets29To40)
	return b, nil
}

// encodeGDS 组装段 2：类型 0（规则经纬）或类型 4（高斯，GaussianN > 0）。
func (p *Params) encodeGDS() ([]byte, error) {
	length := 32
	if p.GaussianN > 0 {
		// 类型 4 在 32 字节基础上追加 4 字节保留（ecCodes 同款布局），
		// octets 26-27 放高斯 N 而不是 Dj。
		length = 36
	}
	b := make([]byte, length)
	putU24(b[0:], length)
	b[3] = 0   // octet 4：NV = 0（不写垂直坐标参数）
	b[4] = 255 // octet 5：PV = 255（不写点数列表）
	if p.GaussianN > 0 {
		b[5] = 4 // octet 6：类型 4 = 高斯经纬
	} else {
		b[5] = 0 // octet 6：类型 0 = 规则经纬
	}
	putU16(b[6:], p.Ni)                // octets 7-8
	putU16(b[8:], p.Nj)                // octets 9-10
	putSmag24(b[10:], p.La1)           // octets 11-13
	putSmag24(b[13:], p.Lo1)           // octets 14-16
	b[16] = 0x80                       // octet 17：分辨率标志（给出 Di/Dj 增量）
	putSmag24(b[17:], p.La2)           // octets 18-20
	putSmag24(b[20:], p.Lo2)           // octets 21-23
	putU16(b[23:], int(p.Di*1000+0.5)) // octets 24-25（Di 无符号，1/1000 度）
	if p.GaussianN > 0 {
		putU16(b[25:], p.GaussianN) // octets 26-27：高斯 N（不是 Dj）
	} else {
		putU16(b[25:], int(p.Dj*1000+0.5)) // octets 26-27：Dj
	}
	b[27] = p.ScanMode // octet 28
	return b, nil
}

// encodeBMS 组装"就地定义"的位图段（表引用 0）。octet 4 = 段末未用位数。
func encodeBMS(present []bool) []byte {
	bodyBits := (len(present) + 7) / 8
	unused := bodyBits*8 - len(present)
	total := 6 + bodyBits
	b := make([]byte, total)
	putU24(b[0:], total)
	b[3] = byte(unused) // octet 4
	// b[4:6] = 表引用 = 0（就地定义）
	for i, ok := range present {
		if ok {
			b[6+i/8] |= 0x80 >> uint(i%8)
		}
	}
	return b
}

// ---- 小工具（与 grib1 解码侧的定义互逆） ----

func putU24(b []byte, v int) {
	_ = b[2]
	b[0], b[1], b[2] = byte(v>>16), byte(v>>8), byte(v)
}

func putU16(b []byte, v int) {
	_ = b[1]
	b[0], b[1] = byte(v>>8), byte(v)
}

// putSmag16 写 2 字节符号-幅度整数（PDS 的十进制比例因子、BDS 的二进制比例因子）。
// 符号位是 bit 15（0x8000），不是 bit 7 —— 低字节第 8 位在 2 字节字段里是数据位。
func putSmag16(b []byte, v int) {
	_ = b[1]
	mag := v
	sign := byte(0)
	if mag < 0 {
		mag, sign = -mag, 0x80
	}
	w := uint16(mag&0x7FFF) | uint16(sign)<<8
	b[0], b[1] = byte(w>>8), byte(w)
}

// putSmag24 写 3 字节符号-幅度整数（经纬度，单位 1/1000 度）。
// 符号位是 bit 23、低 23 位是幅值 —— **不是**补码：GRIB1 的 signed[3]
// 全生态（含 ecCodes grib_decode_signed_long）都按符号-幅度读，写补码会被
// 读成一个巨大的负值。
func putSmag24(b []byte, deg float64) {
	_ = b[2]
	v := int(math.Round(deg * 1000))
	mag := v
	if mag < 0 {
		mag = -mag
	}
	w := mag & 0x7FFFFF
	if v < 0 {
		w |= 0x800000
	}
	b[0], b[1], b[2] = byte(w>>16), byte(w>>8), byte(w)
}
