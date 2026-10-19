package grid

import (
	"math"
	"testing"
)

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// --- Gauss-Legendre nodes (§5.10) -----------------------------------------

func TestLegendreRootsAnalytic(t *testing.T) {
	cases := []struct {
		n    int
		want []float64 // descending
	}{
		{1, []float64{0}},
		{2, []float64{1 / math.Sqrt(3), -1 / math.Sqrt(3)}},
		{3, []float64{math.Sqrt(3.0 / 5), 0, -math.Sqrt(3.0 / 5)}},
		// P_4 roots: 0, ±sqrt((15±2√30)/35) → known values below
		{4, []float64{math.Sqrt((30 + 4*math.Sqrt(30)) / 70), math.Sqrt((30 - 4*math.Sqrt(30)) / 70),
			-math.Sqrt((30 - 4*math.Sqrt(30)) / 70), -math.Sqrt((30 + 4*math.Sqrt(30)) / 70)}},
	}
	for _, c := range cases {
		got, err := legendreRoots(c.n)
		if err != nil {
			t.Fatalf("n=%d: %v", c.n, err)
		}
		for i, w := range c.want {
			if !approx(got[i], w, 1e-12) {
				t.Errorf("n=%d root[%d] = %v, want %v", c.n, i, got[i], w)
			}
		}
	}
	if _, err := legendreRoots(0); err == nil {
		t.Error("n=0: expected error")
	}
}

// TestLegendreQuadratureExactness is an independent correctness check: with
// the true nodes, Σ w_i·x^k must equal ∫₋₁¹ x^k dx = 2/(k+1) for every
// k < 2n (Gauss-Legendre exactness).
func TestLegendreQuadratureExactness(t *testing.T) {
	for _, n := range []int{2, 3, 5, 8} {
		roots, err := legendreRoots(n)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		for k := 0; k < 2*n; k++ {
			sum := 0.0
			for _, x := range roots {
				pn, pnm1 := legendrePair(n, x)
				dp := float64(n) * (x*pn - pnm1) / (x*x - 1)
				w := 2 / ((1 - x*x) * dp * dp)
				sum += w * math.Pow(x, float64(k))
			}
			want := 0.0
			if k%2 == 0 {
				want = 2.0 / float64(k+1)
			}
			if !approx(sum, want, 1e-10) {
				t.Errorf("n=%d k=%d: quadrature = %v, want ∫x^k = %v", n, k, sum, want)
			}
		}
	}
}

func TestGaussianColatitudes(t *testing.T) {
	th, err := gaussianColatitudes(2)
	if err != nil {
		t.Fatal(err)
	}
	// n=2: x = ±1/√3 → θ = arccos(±1/√3), north to south
	want := []float64{math.Acos(1 / math.Sqrt(3)), math.Acos(-1 / math.Sqrt(3))}
	for i := range want {
		if !approx(th[i], want[i], 1e-12) {
			t.Errorf("θ[%d] = %v, want %v", i, th[i], want[i])
		}
	}
	// symmetry: θ_i + θ_{n-1-i} = π
	th4, err := gaussianColatitudes(4)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if !approx(th4[i]+th4[3-i], math.Pi, 1e-12) {
			t.Errorf("θ[%d]+θ[%d] = %v, want π", i, 3-i, th4[i]+th4[3-i])
		}
	}
}

// --- Spherical rotation (§5.8) --------------------------------------------

func TestSphericalRotateRoundTrip(t *testing.T) {
	lats := []float64{-89, -45, 0, 30, 60, 89}
	lons := []float64{-179, -90, 0, 45, 120, 179}
	refs := [][3]float64{
		{20, 40, 15}, {90, 0, 45}, {0, 0, 0}, {-45, -130, -123}, {90, 180, 7}, {-70, 170, 179.5},
	}
	for _, r := range refs {
		for _, la := range lats {
			for _, lo := range lons {
				la2, lo2 := sphericalRotate(la, lo, r[0], r[1], r[2])
				ba, bo := sphericalRotate(la2, lo2, r[0], r[1], -r[2])
				if !approx(ba, la, 1e-9) || !approx(bo, lo, 1e-9) {
					t.Errorf("round trip ref=%v: (%v,%v) → (%v,%v) → (%v,%v)", r, la, lo, la2, lo2, ba, bo)
				}
			}
		}
	}
}

func TestSphericalRotateKnownCases(t *testing.T) {
	// Axis through the north pole: rotation is a pure longitude shift.
	la, lo := sphericalRotate(45, 20, 90, 0, 45)
	if !approx(la, 45, 1e-9) || !approx(lo, 65, 1e-9) {
		t.Errorf("pole axis: got (%v,%v), want (45,65)", la, lo)
	}
	// The reference point lies on the axis and is fixed.
	la, lo = sphericalRotate(37, -123, 37, -123, 80)
	if !approx(la, 37, 1e-9) || !approx(lo, -123, 1e-9) {
		t.Errorf("ref point: got (%v,%v), want (37,-123)", la, lo)
	}
	// Zero angle is the identity.
	la, lo = sphericalRotate(-12.5, 160.25, 10, -20, 0)
	if !approx(la, -12.5, 1e-9) || !approx(lo, 160.25, 1e-9) {
		t.Errorf("zero angle: got (%v,%v)", la, lo)
	}
}

// --- Stretched coordinates (§5.9) ----------------------------------------
// BOUNDARY: eccodes 2.49.0 has no stretched-latlon iterator — all tests in
// this block are 无外部对拍 (no external cross-check); expectations derive
// from the §5.9 formula dual-source reading.--

func TestStretchedCoords(t *testing.T) {
	got := stretchedCoords(0, 1, 1.05, 4)
	// hand-computed: (1.05^k − 1)/0.05 → 0, 1, 2.05, 3.1525
	want := []float64{0, 1, 2.05, 3.1525}
	for i := range want {
		if !approx(got[i], want[i], 1e-9) {
			t.Errorf("k=%d: got %v, want %v", i, got[i], want[i])
		}
	}

	got = stretchedCoords(10, 2, 1, 4) // r == 1 → equal spacing
	want = []float64{10, 12, 14, 16}
	for i := range want {
		if !approx(got[i], want[i], 1e-12) {
			t.Errorf("r=1 k=%d: got %v, want %v", i, got[i], want[i])
		}
	}

	got = stretchedCoords(0, 1, 1+1e-13, 3) // near-1 → degenerate branch
	for i, w := range []float64{0, 1, 2} {
		if !approx(got[i], w, 1e-9) {
			t.Errorf("r≈1 k=%d: got %v, want %v", i, got[i], w)
		}
	}

	got = stretchedCoords(-180, 2, 1.1, 6) // westward stretch
	// k=5: -180 + 2×(1.1^5−1)/0.1 = -180 + 2×6.1051 = -167.7898
	if !approx(got[5], -167.7898, 1e-9) {
		t.Errorf("westward k=5: got %v, want -167.7898", got[5])
	}
}

// --- Regular lat/lon scan ordering (§5.2/§5.5) ------------------------------

func checkPoint(t *testing.T, pts []Point, idx int, la, lo float64) {
	t.Helper()
	if !approx(pts[idx].Lat, la, 1e-9) || !approx(pts[idx].Lon, lo, 1e-9) {
		t.Errorf("pts[%d] = (%v,%v), want (%v,%v)", idx, pts[idx].Lat, pts[idx].Lon, la, lo)
	}
}

func TestRegularPointsGlobalFromNorthPole(t *testing.T) {
	// Classic global grid: start at (90,-180), i fastest, +i, −j.
	p := regularParams{La1: 90, Lo1: -180, Di: 2, Dj: 2, Ni: 180, Nj: 91, ScanMode: 0}
	pts, err := p.points()
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 180*91 {
		t.Fatalf("len = %d, want %d", len(pts), 180*91)
	}
	checkPoint(t, pts, 0, 90, -180)
	checkPoint(t, pts, 1, 90, -178)
	checkPoint(t, pts, 179, 90, 178)
	checkPoint(t, pts, 180, 88, -180)
	checkPoint(t, pts, len(pts)-1, -90, 178)
}

func TestRegularPointsStartSouth(t *testing.T) {
	p := regularParams{La1: -90, Lo1: -180, Di: 2, Dj: 2, Ni: 180, Nj: 91, ScanMode: scanPosJ}
	pts, err := p.points()
	if err != nil {
		t.Fatal(err)
	}
	checkPoint(t, pts, 0, -90, -180)
	checkPoint(t, pts, 180, -88, -180)
	checkPoint(t, pts, len(pts)-1, 90, 178)
}

func TestRegularPointsNegativeIScan(t *testing.T) {
	p := regularParams{La1: 0, Lo1: 10, Di: 5, Dj: 10, Ni: 4, Nj: 2, ScanMode: scanNegI | scanPosJ}
	pts, err := p.points()
	if err != nil {
		t.Fatal(err)
	}
	checkPoint(t, pts, 0, 0, 10)
	checkPoint(t, pts, 1, 0, 5)
	checkPoint(t, pts, 3, 0, -5)
	checkPoint(t, pts, 4, 10, 10)
}

func TestRegularPointsJFastest(t *testing.T) {
	p := regularParams{La1: 0, Lo1: 0, Di: 10, Dj: 5, Ni: 3, Nj: 2, ScanMode: scanPosJ | scanJFast}
	pts, err := p.points()
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 6 {
		t.Fatalf("len = %d, want 6", len(pts))
	}
	checkPoint(t, pts, 0, 0, 0)
	checkPoint(t, pts, 1, 5, 0) // j steps first
	checkPoint(t, pts, 2, 0, 10)
	checkPoint(t, pts, 5, 5, 20)
}

func TestRegularPointsLongitudeNormalization(t *testing.T) {
	p := regularParams{La1: 0, Lo1: 350, Di: 10, Dj: 10, Ni: 4, Nj: 1, ScanMode: scanPosJ}
	pts, err := p.points()
	if err != nil {
		t.Fatal(err)
	}
	checkPoint(t, pts, 0, 0, -10) // 350 → -10
	checkPoint(t, pts, 1, 0, 0)   // 360 → 0
	checkPoint(t, pts, 2, 0, 10)
}

// TestRegularParamsSerpentine asserts the synthetic bit16 row-alternation
// reading on the regularParams unit path (row 0 base +i, row 1 reversed,
// row 2 base again). BOUNDARY: eccodes has no vector exercising this path
// through this unit — 无外部对拍 (no external cross-check); the live
// Definition path is covered by TestDefinitionSerpentine and golden tests.
func TestRegularParamsSerpentine(t *testing.T) {
	// scan = bit16 + bit64(+j): serpentine affects row order only, j stays +.
	p := regularParams{La1: 0, Lo1: 0, Di: 10, Dj: 5, Ni: 3, Nj: 3, ScanMode: scanFirstRev | scanPosJ}
	pts, err := p.points()
	if err != nil {
		t.Fatalf("points: %v", err)
	}
	want := [][]float64{
		{0, 10, 20}, // row 0: +i
		{20, 10, 0}, // row 1: reversed
		{0, 10, 20}, // row 2: base again (full serpentine)
	}
	for j := 0; j < 3; j++ {
		for k := 0; k < 3; k++ {
			idx := j*3 + k
			if got := pts[idx].Lon; got != want[j][k] {
				t.Errorf("row %d pos %d: lon = %v, want %v", j, k, got, want[j][k])
			}
			if got := pts[idx].Lat; got != float64(j)*5 {
				t.Errorf("row %d pos %d: lat = %v, want %v", j, k, got, float64(j)*5)
			}
		}
	}
}

func TestRegularPointsUnsupportedBits(t *testing.T) {
	// Offset bits and serpentine+j-fastest stay rejected; plain serpentine
	// (bit16 alone) is now implemented — see TestRegularParamsSerpentine.
	for _, sm := range []uint8{8, 4, 2, 1, scanFirstRev | scanJFast} {
		p := regularParams{La1: 0, Lo1: 0, Di: 1, Dj: 1, Ni: 2, Nj: 2, ScanMode: sm}
		if _, err := p.points(); err == nil {
			t.Errorf("scanMode=%d: expected error, got nil", sm)
		}
	}
	bad := []regularParams{
		{Ni: 0, Nj: 2},
		{Ni: 2, Nj: -1},
	}
	for _, p := range bad {
		if _, err := p.points(); err == nil {
			t.Errorf("Ni=%d Nj=%d: expected error, got nil", p.Ni, p.Nj)
		}
	}
}

// --- Reduced (quasi-regular) grid (§5.7) ------------------------------------

func TestReducedPointsNiVarying(t *testing.T) {
	// Nj=2 latitude circles fixed; Ni varies: row 0 has 4 points, row 1 has 3.
	p := reducedParams{La1: 90, Lo1: -180, La2: -90, Lo2: 180, Nj: 2, Pl: []int{4, 3}}
	pts, err := p.points()
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 7 {
		t.Fatalf("len = %d, want 7", len(pts))
	}
	// row 0: lat 90; lons -180, -60, 60, 180 (spacing 360/3)
	checkPoint(t, pts, 0, 90, -180)
	checkPoint(t, pts, 1, 90, -60)
	checkPoint(t, pts, 2, 90, 60)
	checkPoint(t, pts, 3, 90, -180)
	// row 1: lat -90; lons -180, 0, 180 (spacing 360/2)
	checkPoint(t, pts, 4, -90, -180)
	checkPoint(t, pts, 5, -90, 0)
	checkPoint(t, pts, 6, -90, -180)
}

func TestReducedPointsNjVarying(t *testing.T) {
	// Ni=2 longitude columns fixed; Nj varies: col 0 has 3 points, col 1 has 2.
	p := reducedParams{La1: -90, Lo1: 0, La2: 90, Lo2: 180, Ni: 2, Pl: []int{3, 2}}
	pts, err := p.points()
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 5 {
		t.Fatalf("len = %d, want 5", len(pts))
	}
	checkPoint(t, pts, 0, -90, 0)
	checkPoint(t, pts, 1, 0, 0) // (La2-La1)/(3-1) = 90 steps
	checkPoint(t, pts, 2, 90, 0)
	checkPoint(t, pts, 3, -90, -180)
	checkPoint(t, pts, 4, 90, -180) // (La2-La1)/(2-1) = 180 steps
}

func TestReducedPointsSingleLine(t *testing.T) {
	p := reducedParams{La1: 45, Lo1: 10, La2: 45, Lo2: 10, Nj: 1, Pl: []int{1}}
	pts, err := p.points()
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 1 {
		t.Fatalf("len = %d, want 1", len(pts))
	}
	checkPoint(t, pts, 0, 45, 10)
}

func TestReducedPointsErrors(t *testing.T) {
	bad := []reducedParams{
		{La1: 0, Lo1: 0, La2: 1, Lo2: 1, Ni: 2, Nj: 3, Pl: []int{2, 3}}, // both fixed
		{La1: 0, Lo1: 0, La2: 1, Lo2: 1, Pl: []int{2, 3}},               // both missing
		{La1: 0, Lo1: 0, La2: 1, Lo2: 1, Nj: 3, Pl: []int{2, 3}},        // len(Pl) != Nj
	}
	for _, p := range bad {
		if _, err := p.points(); err == nil {
			t.Errorf("Ni=%d Nj=%d Pl=%v: expected error, got nil", p.Ni, p.Nj, p.Pl)
		}
	}
}
