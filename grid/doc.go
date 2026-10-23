// Package grid 实现 GRIB2 第 3 节（网格定义）模板：解码模板字段并计算
// 每个网格点的地理坐标。
//
// 支持完整坐标生成的模板：
//
//   - 3.0 经纬度网格，含准规则（减缩）变体——此时 Ni/Nj 之一被替换为
//     逐行/逐列的点数列表
//   - 3.1/3.3 旋转、3.2/3.3 拉伸经纬度网格（3.3 同时包含两者）
//   - 3.20 极射赤面投影、3.30 Lambert 等角圆锥投影
//   - 3.40/3.41 Gaussian 网格；平行圈纬度取自 Legendre 多项式 P_2N 的根，
//     支持子区域窗口。区域减缩高斯变体（reduced Gaussian，Ni 或 Nj 编码为
//     missing）目前只提供元数据：编码角点走 Bounds()、高斯参数 N 走
//     GaussianN()、点数走 NumPointsDeclared()（NumPoints() 为 0，
//     Point/Lats/Lons 返回 ErrNotImplemented）；真实 ECMWF 区域 reduced_gg
//     文件因此不再让整个 GRIB 文件读取失败（IsReducedGaussian 可识别）
//
// 没有坐标生成器的模板（3.10 Mercator、拉伸 Gaussian 族 3.42/3.43、
// 区域减缩高斯 3.40/3.41，以及更新的模板）仍会解析为 Definition，
// 元数据访问器可用；对它们调用 Point/Lats/Lons 会返回 ErrNotImplemented。
//
// NewGRIB1 从 GRIB1 第 2 节（GDS）构建同一个 Definition 类型：
// 它与 GRIB2 第 3 节只在字节布局上有别，几何语义相同。
//
// Package grid implements the GRIB2 section 3 (grid definition) templates:
// it decodes the template fields and computes the geographic coordinates of
// every grid point.
//
// Templates with full coordinate support:
//
//   - 3.0 latitude/longitude, including the quasi-regular (reduced) variant
//     where one of Ni/Nj is replaced by a per-row/column point-count list
//   - 3.1/3.3 rotated, 3.2/3.3 stretched latitude/longitude (both combined
//     in 3.3)
//   - 3.20 polar stereographic and 3.30 Lambert conformal conic projections
//   - 3.40/3.41 Gaussian grids; the parallel latitudes come from the roots
//     of the Legendre polynomial P_2N, sub-area windows supported. The
//     regional reduced Gaussian variant (Ni or Nj encoded as missing) is
//     metadata only for now: encoded corners via Bounds(), the Gaussian
//     parameter via GaussianN(), the point count via NumPointsDeclared()
//     (NumPoints() is 0 and Point/Lats/Lons report ErrNotImplemented), so
//     real ECMWF regional reduced_gg files no longer make the whole GRIB
//     file unreadable (IsReducedGaussian identifies the case)
//
// Templates without a coordinate generator (3.10 Mercator, the stretched
// Gaussian family 3.42/3.43, the regional reduced Gaussian 3.40/3.41, and
// anything newer) still parse into a Definition with usable metadata
// accessors; Point/Lats/Lons report ErrNotImplemented for them.
//
// NewGRIB1 builds the same Definition type from a GRIB1 section 2 (GDS),
// which only differs from GRIB2 section 3 in byte layout, not geometry.
//
// A Definition is not safe for concurrent use: Point/Lats/Lons fill a lazy
// cache on first call. Share one across goroutines only with external
// serialization, or give each goroutine its own Definition (e.g. by
// re-parsing the section bytes). The metadata accessors are read-only and
// always safe.
package grid
