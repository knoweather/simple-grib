package grid

import "math"

// sphericalRotate 把地理点 (lat, lon) 绕穿过 (refLat, refLon) 的轴旋转
// angleDeg 度，返回旋转后的地理坐标（各量均为度）。这是单位球面上的刚体
// 旋转（Rodrigues 公式），因此参考点本身保持不动。逆变换即以
// -angleDeg 调用 sphericalRotate。
//
// sphericalRotate rotates the geographic point (lat, lon) by angleDeg
// about the axis through (refLat, refLon) and returns the new geographic
// coordinates (all values in degrees). It is a rigid rotation of the unit
// sphere (Rodrigues' formula), so the reference point itself stays fixed.
// The inverse is sphericalRotate with -angleDeg.
func sphericalRotate(lat, lon, refLat, refLon, angleDeg float64) (float64, float64) {
	ax, ay, az := toUnitVec(refLat, refLon) // 旋转轴
	vx, vy, vz := toUnitVec(lat, lon)       // 待旋转的点

	c := math.Cos(rad(angleDeg))
	s := math.Sin(rad(angleDeg))

	// Rodrigues 公式：v' = v·cosθ + (a×v)·sinθ + a·(a·v)·(1−cosθ)
	// Rodrigues' formula: v' = v·cosθ + (a×v)·sinθ + a·(a·v)·(1−cosθ)
	crossX := ay*vz - az*vy
	crossY := az*vx - ax*vz
	crossZ := ax*vy - ay*vx
	dot := ax*vx + ay*vy + az*vz

	nx := vx*c + crossX*s + ax*dot*(1-c)
	ny := vy*c + crossY*s + ay*dot*(1-c)
	nz := vz*c + crossZ*s + az*dot*(1-c)

	return fromUnitVec(nx, ny, nz)
}

// rad 把角度转换为弧度
// rad converts degrees to radians.
func rad(d float64) float64 { return d * math.Pi / 180 }

// toUnitVec 把地理坐标（度）转换为单位笛卡尔向量：x 指向 (0°,0°)，
// y 指向 (0°,90°E)，z 指向北极。
//
// toUnitVec converts a geographic position (lat, lon in degrees) to a
// unit Cartesian vector: x toward (0°,0°), y toward (0°,90°E), z toward
// the north pole.
func toUnitVec(lat, lon float64) (x, y, z float64) {
	φ, λ := rad(lat), rad(lon)
	cosφ := math.Cos(φ)
	return cosφ * math.Cos(λ), cosφ * math.Sin(λ), math.Sin(φ)
}

// fromUnitVec 把（近似单位长的）笛卡尔向量转回地理纬度/经度（度），
// 经度归一化到 [-180, 180)。z 先做 clamp 再取 Asin，避免浮点误差把
// 参数推出 [-1,1]。
//
// fromUnitVec converts a (nearly unit) Cartesian vector back to geographic
// latitude/longitude in degrees; the longitude is normalized to
// [-180, 180). z is clamped before Asin so floating-point error cannot
// push the argument outside [-1,1].
func fromUnitVec(x, y, z float64) (lat, lon float64) {
	lat = math.Asin(clamp(z, -1, 1)) * 180 / math.Pi
	lon = math.Atan2(y, x) * 180 / math.Pi
	return lat, normalizeLon(lon)
}

// clamp 把 v 限制在 [lo, hi] 区间内
// clamp restricts v to the [lo, hi] interval.
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// normalizeLon 把经度（度）映射到 [-180, 180)：先对 360 取模，负值补
// 360，最后整体平移 -180。
//
// normalizeLon maps a longitude (degrees) onto [-180, 180): reduce modulo
// 360, shift negative results up by 360, then shift down by 180.
func normalizeLon(lon float64) float64 {
	l := math.Mod(lon+180, 360)
	if l < 0 {
		l += 360
	}
	return l - 180
}

// unrotate 把旋转网格坐标系中的点映射回真实地理坐标（GRIB2 模板
// 3.1/3.3/3.41）。算法逐字复刻 ecCodes 的 grib_geography.cc 中的
// unrotate（承自旧 ecKit 的 RotateGrid::unrotate，Tag 2015.11.0）：先
// 平移旋转使投影南极 (spLat, spLon) 落到真实南极，再从经度中减去
// angleOfRot。ecCodes 还会把结果舍入到 6 位小数；本实现保留全精度
// （调用方自行按容差比较）。
//
// unrotate maps a point from the rotated grid frame into true geographic
// coordinates (GRIB2 templates 3.1/3.3/3.41). It replicates the ecCodes
// grib_geography.cc unrotate verbatim ("inherited from the old ecKit
// RotateGrid::unrotate, Tag 2015.11.0"): the point is moved so that the
// projected south pole (spLat, spLon) falls onto the true south pole, then
// angleOfRot is subtracted from the longitude. ecCodes additionally rounds
// the result to 6 decimals; this implementation keeps full precision
// (callers compare with a tolerance).
func unrotate(lat, lon, angleOfRot, spLat, spLon float64) (float64, float64) {
	latr, lonr := rad(lat), rad(lon)
	xd := math.Cos(lonr) * math.Cos(latr)
	yd := math.Sin(lonr) * math.Cos(latr)
	zd := math.Sin(latr)

	t := rad(-(90 + spLat))
	o := rad(-spLon)
	sinT, cosT := math.Sin(t), math.Cos(t)
	sinO, cosO := math.Sin(o), math.Cos(o)

	x := cosT*cosO*xd + sinO*yd + sinT*cosO*zd
	y := -cosT*sinO*xd + cosO*yd - sinT*sinO*zd
	z := -sinT*xd + cosT*zd

	nl := math.Asin(clamp(z, -1, 1)) * 180 / math.Pi
	no := math.Atan2(y, x) * 180 / math.Pi
	return nl, normalizeLon(no - angleOfRot)
}

// rotateFrame 是 unrotate 的精确逆变换（矩阵取转置，再把旋转角加回去）：
// 把真实地理坐标映射回旋转网格坐标系。往返测试用它做闭合校验。
//
// rotateFrame is the exact inverse of unrotate (transpose of the matrix,
// plus adding the rotation angle back): it maps true geographic
// coordinates into the rotated grid frame. The round-trip tests use it for
// closure checks.
func rotateFrame(lat, lon, angleOfRot, spLat, spLon float64) (float64, float64) {
	lon += angleOfRot // 抵消 unrotate 的 "lon -= angleOfRot"
	// the inverse of unrotate's "lon -= angleOfRot"
	latr, lonr := rad(lat), rad(lon)
	xd := math.Cos(lonr) * math.Cos(latr)
	yd := math.Sin(lonr) * math.Cos(latr)
	zd := math.Sin(latr)

	t := rad(-(90 + spLat))
	o := rad(-spLon)
	sinT, cosT := math.Sin(t), math.Cos(t)
	sinO, cosO := math.Sin(o), math.Cos(o)

	// unrotate 矩阵的转置（该矩阵正交，转置即逆）
	// transpose of the unrotate matrix (it is orthogonal)
	x := cosT*cosO*xd - cosT*sinO*yd - sinT*zd
	y := sinO*xd + cosO*yd
	z := sinT*cosO*xd - sinT*sinO*yd + cosT*zd

	nl := math.Asin(clamp(z, -1, 1)) * 180 / math.Pi
	no := math.Atan2(y, x) * 180 / math.Pi
	return nl, normalizeLon(no)
}
