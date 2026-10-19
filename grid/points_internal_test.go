// 网格坐标生成单元测试：regularParams.points / reducedParams.points 的仿射
// 公式、lineCoord、normalizeLon。这些是「坐标生成」正确性最关键的部分，
// 之前多是经 grid.New 集成覆盖；这里直接喂参数、对拍每一点，防止扫描模式
// 或准规则点表的计算悄悄出错。

package grid

import "testing"

// TestRegularPoints 锁死正则经纬度网格的仿射坐标公式与数据顺序（i 最快，
// j 负向扫描）。expected(i) 用与 points() 完全相同的公式离线计算，逐点比对。
func TestRegularPoints(t *testing.T) {
	p := &regularParams{La1: 30, Lo1: 100, Di: 10, Dj: 10, Ni: 7, Nj: 4, ScanMode: 0}
	pts, err := p.points()
	if err != nil {
		t.Fatalf("points: %v", err)
	}
	if len(pts) != 28 {
		t.Fatalf("len = %d, want 28 (Ni×Nj)", len(pts))
	}
	// 离线公式：i 最快，jDir=-1，iDir=+1。
	expLat := func(j int) float64 { return 30 - float64(j)*10 }
	expLon := func(k int) float64 { return normalizeLon(100 + float64(k)*10) }
	for j := 0; j < 4; j++ {
		for k := 0; k < 7; k++ {
			i := j*7 + k
			want := Point{Lat: expLat(j), Lon: expLon(k)}
			if pts[i] != want {
				t.Errorf("point[%d] (j=%d k=%d) = %+v, want %+v", i, j, k, pts[i], want)
			}
		}
	}
}

// TestRegularPointsScanModes 覆盖核心扫描位：0x80(i 负向)、0x40(j 正向)、
// 0x20(j 最快) 下首几点应落在哪里。统一校验 point[0]（j=0,k=0）、point[1]
// （j=0,k=1）与 point[7]（i 最快时为 j=1,k=0；j 最快时为 i=1,j=3）。
func TestRegularPointsScanModes(t *testing.T) {
	base := regularParams{La1: 30, Lo1: 100, Di: 10, Dj: 10, Ni: 7, Nj: 4}
	cases := []struct {
		name    string
		scan    uint8
		first   Point // point[0]
		second  Point // point[1] (j=0,k=1)
		seventh Point // point[7]
	}{
		{"default (i-fast, j neg)", 0,
			Point{30, 100}, Point{30, 110}, Point{20, 100}}, // jDir=-1 → point[7] 纬度 -10
		{"posJ (0x40)", 0x40,
			Point{30, 100}, Point{30, 110}, Point{40, 100}}, // jDir=+1 → point[7] 纬度 +10
		{"negI (0x80)", 0x80,
			Point{30, 100}, Point{30, 90}, Point{20, 100}}, // iDir=-1 → point[1] 经度 -10
		{"jFast (0x20)", 0x20,
			Point{30, 100}, Point{20, 100}, Point{0, 110}}, // j 最快：point[1]=j=1, point[7]=i=1,j=3
	}
	for _, c := range cases {
		p := base
		p.ScanMode = c.scan
		pts, err := p.points()
		if err != nil {
			t.Fatalf("%s: points: %v", c.name, err)
		}
		if pts[0] != c.first {
			t.Errorf("%s: point[0] = %+v, want %+v", c.name, pts[0], c.first)
		}
		if pts[1] != c.second {
			t.Errorf("%s: point[1] = %+v, want %+v", c.name, pts[1], c.second)
		}
		if pts[7] != c.seventh {
			t.Errorf("%s: point[7] = %+v, want %+v", c.name, pts[7], c.seventh)
		}
	}
}

// TestRegularPointsBadScan 不支持的扫描位（奇/偶行偏移）必须失败即报错。
func TestRegularPointsBadScan(t *testing.T) {
	p := regularParams{La1: 0, Lo1: 0, Di: 1, Dj: 1, Ni: 2, Nj: 2, ScanMode: 0x01}
	if _, err := p.points(); err == nil {
		t.Error("odd/even offset bit set: want error, got nil")
	}
}

// TestReducedPointsRows 锁死「Nj 固定、Ni 变化」的准规则网格：沿纬圈行主序，
// 每行 Pl[j] 个经度，纬度按行等距。经度范围取 0..10，避免归一到 [-180,180)
// 的环绕把真值掩盖掉。
func TestReducedPointsRows(t *testing.T) {
	p := &reducedParams{La1: 0, La2: 30, Lo1: 0, Lo2: 10, Nj: 4, Pl: []int{3, 4, 4, 5}}
	pts, err := p.points()
	if err != nil {
		t.Fatalf("points: %v", err)
	}
	if len(pts) != 16 {
		t.Fatalf("len = %d, want 16 (sum of Pl)", len(pts))
	}
	// 首点：j=0 第一行，纬度 0，经度 0。
	if pts[0] != (Point{0, 0}) {
		t.Errorf("point[0] = %+v, want {0 0}", pts[0])
	}
	// 第二行首点：纬度 lineCoord(0,30,4,1)=10，经度 0。
	if pts[3] != (Point{10, 0}) {
		t.Errorf("point[3] = %+v, want {10 0}", pts[3])
	}
	// 末点：j=3 第四行，纬度 30，经度 lineCoord(0,10,5,4)=10。
	if pts[15] != (Point{30, 10}) {
		t.Errorf("point[15] = %+v, want {30 10}", pts[15])
	}
}

// TestReducedPointsCols 锁死「Ni 固定、Nj 变化」的准规则网格：沿经度列主序。
func TestReducedPointsCols(t *testing.T) {
	p := &reducedParams{La1: 0, La2: 30, Lo1: 0, Lo2: 120, Ni: 3, Pl: []int{2, 3, 4}}
	pts, err := p.points()
	if err != nil {
		t.Fatalf("points: %v", err)
	}
	if len(pts) != 9 {
		t.Fatalf("len = %d, want 9 (sum of Pl)", len(pts))
	}
	if pts[0] != (Point{0, 0}) {
		t.Errorf("point[0] = %+v, want {0 0}", pts[0])
	}
	// 第二列首点：经度 lineCoord(0,120,3,1)=60，纬度 0。
	if pts[2] != (Point{0, 60}) {
		t.Errorf("point[2] = %+v, want {0 60}", pts[2])
	}
	// 第三列首点：经度 lineCoord(0,120,3,2)=120，纬度 0。
	if pts[5] != (Point{0, 120}) {
		t.Errorf("point[5] = %+v, want {0 120}", pts[5])
	}
}

// TestLineCoord 等距线性坐标：n<=1 退化成 a；否则 a + (b-a)·k/(n-1)。
func TestLineCoord(t *testing.T) {
	cases := []struct {
		a, b float64
		n, k int
		want float64
	}{
		{0, 10, 5, 0, 0},
		{0, 10, 5, 4, 10},
		{0, 10, 5, 2, 5},
		{0, 10, 1, 0, 0},  // n<=1 退化
		{0, 10, 1, 99, 0}, // n<=1 无论 k 都是 a
		{-90, 90, 3, 1, 0},
	}
	for _, c := range cases {
		if got := lineCoord(c.a, c.b, c.n, c.k); got != c.want {
			t.Errorf("lineCoord(%v,%v,%d,%d) = %v, want %v", c.a, c.b, c.n, c.k, got, c.want)
		}
	}
}

// TestNormalizeLon 经度归一化到 [-180, 180)。
func TestNormalizeLon(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0, 0},
		{180, -180},
		{-180, -180},
		{190, -170},
		{-190, 170},
		{360, 0},
		{350, -10},
	}
	for _, c := range cases {
		if got := normalizeLon(c.in); got != c.want {
			t.Errorf("normalizeLon(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
