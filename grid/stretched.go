package grid

import "math"

// stretchedCoords 返回被“拉伸”维度上的 n 个坐标（模板 3.2/3.3，）：
//
//	x_k = x_0 + D × (r^k − 1)/(r − 1),  k = 0..n−1
//
// r 是（无量纲）拉伸因子；当 r == 1（在 1e-12 之内）时该维度退化为等间距
// x_0 + D×k。x_0/D/r 已由调用方解析为物理单位（缩放值按 §5.3 解码）。
func stretchedCoords(x0, d, r float64, n int) []float64 {
	out := make([]float64, n)
	if math.Abs(r-1) < 1e-12 {
		for k := 0; k < n; k++ {
			out[k] = x0 + d*float64(k)
		}
		return out
	}
	for k := 0; k < n; k++ {
		out[k] = x0 + d*(math.Pow(r, float64(k))-1)/(r-1)
	}
	return out
}
