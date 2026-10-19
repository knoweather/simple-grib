package grid

import (
	"fmt"
	"math"
)

// legendrePair 用三项递推同时计算勒让德多项式 P_n(x) 与 P_{n-1}(x)。
// 递推式：P_k(x) = ((2k-1)·x·P_{k-1}(x) - (k-1)·P_{k-2}(x)) / k，
// 单次调用耗时 O(n)。n=0 时返回 (1, 0)，此时 P_{-1} 无定义也不会被使用。
//
// legendrePair computes P_n(x) and P_{n-1}(x) simultaneously via the
// three-term recurrence P_k(x) = ((2k-1)·x·P_{k-1}(x) -
// (k-1)·P_{k-2}(x)) / k, costing O(n) per call. For n == 0 it returns
// (1, 0); P_{-1} is undefined and never used.
func legendrePair(n int, x float64) (pn, pnm1 float64) {
	if n == 0 {
		return 1, 0 // P_0 = 1；P_{-1} 无定义也不使用 / P_0 = 1; P_{-1} is undefined and unused
	}
	p0 := 1.0 // P_0
	p1 := x   // P_1
	for k := 2; k <= n; k++ {
		p0, p1 = p1, (float64(2*k-1)*x*p1-float64(k-1)*p0)/float64(k)
	}
	return p1, p0 // p1 = P_n，p0 = P_{n-1} / p1 = P_n, p0 = P_{n-1}
}

// legendreRoots 返回勒让德多项式 P_n 在 [-1,1] 上的 n 个根，按降序排列
// （最大者在前）。Newton 迭代从标准渐近初始值 x ≈ cos(π(i+3/4)/(n+1/2))
// 出发；导数 P'_n 复用同一递推对求值（P'_n(x) = n(x·P_n(x) -
// P_{n-1}(x)) / (x²-1)），因此每次迭代代价为 O(n)，最多迭代 100 次，
// 收敛判据为 |Δx| < 1e-15。
//
// legendreRoots returns the n roots of the Legendre polynomial P_n on
// [-1,1], in descending order (largest first). Newton iteration starts
// from the standard asymptotic guess x ≈ cos(π(i+3/4)/(n+1/2)); the
// derivative P'_n(x) = n(x·P_n(x) - P_{n-1}(x)) / (x²-1) reuses the same
// recurrence pair, so each iteration costs O(n). Iteration stops at
// |Δx| < 1e-15 or after 100 steps.
func legendreRoots(n int) ([]float64, error) {
	if n < 1 {
		return nil, fmt.Errorf("grid: legendreRoots: n = %d, want >= 1", n)
	}
	roots := make([]float64, n)
	for i := 0; i < n; i++ {
		x := math.Cos(math.Pi * (float64(i) + 0.75) / (float64(n) + 0.5))
		for iter := 0; iter < 100; iter++ {
			pn, pnm1 := legendrePair(n, x)
			dp := float64(n) * (x*pn - pnm1) / (x*x - 1) // P'_n(x)（导数公式）/ P'_n(x), the derivative
			dx := pn / dp
			x -= dx
			if math.Abs(dx) < 1e-15 {
				break
			}
		}
		roots[i] = x
	}
	return roots, nil
}

// gaussianColatitudes 把 P_n 的根 x_i 转换为 n 个余纬 θ_i = arccos(x_i)
// （单位：弧度），按从北到南排序（θ 从 0 递增到 π）。高斯网格（GRIB2
// 模板 3.40/3.41）的纬圈正是取这些余纬。传入 n < 1 时错误由
// legendreRoots 产生并原样向上传递。
//
// gaussianColatitudes converts the roots x_i of P_n into the n
// colatitudes θ_i = arccos(x_i) in radians, ordered north to south
// (θ grows from 0 to π). Gaussian grids (GRIB2 templates 3.40/3.41) place
// their parallels at these colatitudes. An n < 1 error comes from
// legendreRoots and is propagated unchanged.
func gaussianColatitudes(n int) ([]float64, error) {
	roots, err := legendreRoots(n)
	if err != nil {
		return nil, err
	}
	th := make([]float64, n)
	for i, x := range roots {
		th[i] = math.Acos(x) // 根为降序 → θ 递增（北→南）/ roots descend → θ ascends (north→south)
	}
	return th, nil
}
