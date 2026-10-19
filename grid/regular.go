package grid

import "fmt"

// ErrNotImplemented reports a grid definition template this package cannot
// generate coordinates for. New/NewGRIB1 still return usable metadata for
// such templates; Point/Lats/Lons report this error.
type ErrNotImplemented struct{ Template int }

func (e *ErrNotImplemented) Error() string {
	return fmt.Sprintf("grid: grid definition template %d not implemented", e.Template)
}

// Point is one geographic coordinate in degrees; longitude normalized to
// [-180,180).
type Point struct {
	Lat float64
	Lon float64
}

// Scan-mode flag bits (flag table 3.4; WMO bit 1 = the MSB).
const (
	scanNegI     uint8 = 128           // +i scans negatively
	scanPosJ     uint8 = 64            // +j scans positively (0 -> negative)
	scanJFast    uint8 = 32            // 1 = consecutive values step along j (j fastest); 0 = i fastest
	scanFirstRev uint8 = 16            // adjacent rows alternate direction (full serpentine; implemented by Definition.regularPoints)
	scanOffsets  uint8 = 8 | 4 | 2 | 1 // odd/even line offsets — unsupported
)

// regularParams is a fully parsed template 3.0 latitude/longitude grid:
// angles in degrees; Di/Dj are magnitudes, with the scan direction carried
// by the scan-mode bits; Ni/Nj are the point counts.
type regularParams struct {
	La1, Lo1 float64 // first grid point (octets 47-54)
	Di, Dj   float64
	Ni, Nj   int
	ScanMode uint8
}

// points returns all Ni×Nj grid points in data (scan) order. It supports the
// core scan bits 128/64/32 plus the bit-16 full serpentine on i-fastest
// grids (adjacent rows reverse the i direction — the same reading as
// Definition.regularPoints). The odd/even offset bits (8/4/2/1) and the
// serpentine + j-fastest combination fail loud.
func (p *regularParams) points() ([]Point, error) {
	if p.Ni < 1 || p.Nj < 1 {
		return nil, fmt.Errorf("grid: Ni=%d Nj=%d, want >= 1", p.Ni, p.Nj)
	}
	if m := p.ScanMode & scanOffsets; m != 0 {
		return nil, fmt.Errorf("grid: scan mode %d sets odd/even line offset bit(s) %d, not supported in P1", p.ScanMode, m)
	}
	serpentine := p.ScanMode&scanFirstRev != 0
	if serpentine && p.ScanMode&scanJFast != 0 {
		return nil, fmt.Errorf("grid: scan mode %d: first-line reversal combined with j-fastest is not supported in P1", p.ScanMode)
	}

	iDir := 1.0
	if p.ScanMode&scanNegI != 0 {
		iDir = -1
	}
	jDir := 1.0
	if p.ScanMode&scanPosJ == 0 {
		jDir = -1
	}

	out := make([]Point, 0, p.Ni*p.Nj)
	add := func(i, j int) {
		out = append(out, Point{
			Lat: p.La1 + float64(j)*jDir*p.Dj,
			Lon: normalizeLon(p.Lo1 + float64(i)*iDir*p.Di),
		})
	}
	if p.ScanMode&scanJFast != 0 {
		for i := 0; i < p.Ni; i++ { // j fastest: consecutive values step along j
			for j := 0; j < p.Nj; j++ {
				add(i, j)
			}
		}
		return out, nil
	}
	for j := 0; j < p.Nj; j++ { // i fastest (standard): Nj rows × Ni points per row
		reverse := serpentine && j%2 == 1 // full serpentine: odd rows reverse
		for k := 0; k < p.Ni; k++ {
			i := k
			if reverse {
				i = p.Ni - 1 - k
			}
			add(i, j)
		}
	}
	return out, nil
}

// reducedParams describes a quasi-regular (reduced) latitude/longitude grid:
// one dimension has a different point count per row/column. Ni/Nj hold the
// fixed dimension's count (0 = missing); Pl holds the per-row/column counts
// of the varying dimension.
type reducedParams struct {
	La1, Lo1, La2, Lo2 float64 // degrees
	Ni, Nj             int     // fixed count of the known dimension; 0 = missing
	Pl                 []int   // per-row/column counts along the varying dimension
}

// points returns all grid points in the canonical order: when Ni varies
// (Nj fixed) latitude circles run row-major with Pl[j] longitudes per row;
// when Nj varies (Ni fixed) meridians run column-major. Any scan-mode
// reordering of the data order is applied by the Definition layer.
func (p *reducedParams) points() ([]Point, error) {
	switch {
	case p.Nj > 0 && p.Ni == 0:
		if len(p.Pl) != p.Nj {
			return nil, fmt.Errorf("grid: reduced grid: point list length %d != Nj %d", len(p.Pl), p.Nj)
		}
		total := 0
		for _, n := range p.Pl {
			total += n
		}
		out := make([]Point, 0, total)
		for j := 0; j < p.Nj; j++ {
			lat := lineCoord(p.La1, p.La2, p.Nj, j)
			n := p.Pl[j]
			for k := 0; k < n; k++ {
				out = append(out, Point{Lat: lat, Lon: normalizeLon(lineCoord(p.Lo1, p.Lo2, n, k))})
			}
		}
		return out, nil
	case p.Ni > 0 && p.Nj == 0:
		if len(p.Pl) != p.Ni {
			return nil, fmt.Errorf("grid: reduced grid: point list length %d != Ni %d", len(p.Pl), p.Ni)
		}
		total := 0
		for _, n := range p.Pl {
			total += n
		}
		out := make([]Point, 0, total)
		for i := 0; i < p.Ni; i++ {
			lon := normalizeLon(lineCoord(p.Lo1, p.Lo2, p.Ni, i))
			n := p.Pl[i]
			for k := 0; k < n; k++ {
				out = append(out, Point{Lat: lineCoord(p.La1, p.La2, n, k), Lon: lon})
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("grid: reduced grid: exactly one of Ni/Nj must be the fixed count (Ni=%d Nj=%d)", p.Ni, p.Nj)
	}
}

// lineCoord gives the k-th (k = 0..n−1) coordinate of n equally spaced
// points from a to b; n <= 1 yields a.
func lineCoord(a, b float64, n, k int) float64 {
	if n <= 1 {
		return a
	}
	return a + (b-a)*float64(k)/float64(n-1)
}
