package grid

import (
	"fmt"
	"math"

	"github.com/knoweather/simple-grib/internal/octetio"
	"github.com/knoweather/simple-grib/spec"
)

// missingU32 is the WMO "missing" sentinel for unsigned 4-byte fields.
const missingU32 = uint32(0xFFFFFFFF)

// missingU8 is the "missing" sentinel for unsigned 1-byte fields (the value
// ecCodes encodes for its can_be_missing fields).
const missingU8 = uint8(0xFF)

// maxGridPoints caps the number of points a section 3 may declare. Real
// grids stay far below (a 0.1-degree global grid is about 6.5e6 points).
// The bound keeps Ni×Nj from overflowing int and keeps Point()/Lats() from
// making multi-GB allocations at the behest of a corrupt or malicious file.
// 2^30 also fits inside a 32-bit int.
const maxGridPoints = 1 << 30

// maxGaussianN bounds the Gaussian parameter N (parallels from pole to
// equator). Building the parallel list is O((2N)²): Newton iteration for
// each of the 2N Legendre roots, each evaluation O(2N) via the recurrence —
// so an unbounded N is a CPU/memory denial-of-service. Real Gaussian grids
// top out around N=1280 (ECMWF O1280); 32768 is about 25x that, so real
// grids pass while the worst case stays contained.
const maxGaussianN = 1 << 15

// checkProduct returns a×b when both factors and the product stay within
// maxGridPoints, failing loudly otherwise. Checking the factors first
// guarantees the product cannot overflow int.
func checkProduct(what string, a, b int) (int, error) {
	if a < 0 || b < 0 || int64(a) > maxGridPoints || int64(b) > maxGridPoints {
		return 0, fmt.Errorf("grid: %s: %d × %d exceeds the supported maximum of %d points", what, a, b, maxGridPoints)
	}
	p := int64(a) * int64(b)
	if p > maxGridPoints {
		return 0, fmt.Errorf("grid: %s: %d × %d = %d exceeds the supported maximum of %d points", what, a, b, p, maxGridPoints)
	}
	return int(p), nil
}

// checkSum returns the total of ns when every entry and the running sum stay
// within maxGridPoints, failing loudly otherwise.
func checkSum(what string, ns []int) (int, error) {
	total := int64(0)
	for _, n := range ns {
		if n < 0 || int64(n) > maxGridPoints || total > maxGridPoints-int64(n) {
			return 0, fmt.Errorf("grid: %s: point list exceeds the supported maximum of %d points", what, maxGridPoints)
		}
		total += int64(n)
	}
	return int(total), nil
}

// defKind classifies the coordinate generator selected for a Definition.
type defKind int

const (
	kindNone      defKind = iota // metadata only; Point/Lats/Lons -> ErrNotImplemented
	kindRegular                  // template 3.0 latitude/longitude
	kindReduced                  // template 3.0 quasi-regular (point-count list)
	kindGaussian                 // templates 3.40/3.41
	kindProjected                // templates 3.20 polar stereographic / 3.30 Lambert conformal
)

// Definition is a parsed section 3 grid definition. New succeeds for any
// template number: templates without a coordinate generator yield usable
// metadata, and their Point/Lats/Lons report ErrNotImplemented{Template}.
//
// Definition is not safe for concurrent use: Point/Lats/Lons fill lazy
// caches (points, stretchedLons) on first call. To share one across
// goroutines, serialize coordinate access, or give each goroutine its own
// Definition (e.g. re-parse the section bytes with New). The metadata
// accessors are read-only and safe.
type Definition struct {
	sectionData []byte // complete section bytes, from octet 1 on

	edition        int // 1 = GRIB1 GDS, 2 = GRIB2 section 3 (templateNumber semantics)
	templateNumber int // GRIB2: octets 13-14; GRIB1: GDS octet 6 data type
	source         int // octet 6 (table 3.0)
	declared       int // octets 7-10 (authoritative; 0 = not encoded)
	walk           *spec.Walk

	// ni/nj are parsed and cached at construction. GRIB1 and GRIB2 keep them
	// in completely different octets; the cache makes the accessors
	// edition-independent (see NewGRIB1).
	ni, nj int

	kind defKind

	// shared lat/lon base fields (templates 3.0-3.41), in degrees
	shape     int     // shape of earth, table 3.2
	radiusRaw uint32  // octets 17-20
	radiusScl uint8   // octet 16 (unsigned[1], per ecCodes shape_of_the_earth.def)
	majorRaw  uint32  // octets 22-25
	majorScl  uint8   // octet 21
	minorRaw  uint32  // octets 27-30
	minorScl  uint8   // octet 26
	La1, Lo1  float64 // degrees
	La2, Lo2  float64 // degrees
	Di, Dj    float64 // degrees (unsigned magnitudes; direction from the scan mode)
	resFlags  uint8
	scanMode  uint8

	// hasCorners records that La1/Lo1/La2/Lo2 were decoded from a template
	// that carries them (GRIB2 3.0-3.3/3.40-3.41, GRIB1 types 0/4/10).
	// Bounds() reports ok=false otherwise, so a zero-valued La1/Lo1 of a
	// projection template is never mistaken for an encoded corner.
	hasCorners bool

	// reducedGauss marks the metadata-only reduced Gaussian case: GRIB2
	// template 3.40/3.41 with Ni or Nj encoded as missing. Coordinates are
	// withheld (kindNone); Bounds/GaussianN/NumPointsDeclared carry the
	// metadata. See IsReducedGaussian.
	reducedGauss bool

	// quasi-regular (kindReduced)
	reduced *reducedParams

	// projected templates (kindProjected): 3.20/3.30
	proj *projectedParams

	// Gaussian (kindGaussian)
	gaussN     int // template field N (octets 68-71): parallels pole-to-equator; order = 2N
	gaussStart int // sub-area window: index of the first row in the full parallel list
	gaussJDir  int // +1/-1: j walk direction across the window

	// rotation (3.1/3.3/3.41), degrees; zero value = unrotated
	spLat, spLon, rotAngle float64
	rotated                bool

	// stretching (3.2/3.3): factor r and the stretching pole. The pole is
	// parsed as metadata; the geometric series is applied column by column
	// along i (see lonForColumn).
	stretchR      float64
	stretched     bool
	stretchedLons []float64 // lazy cache of stretchedCoords(Lo1, iDir·Di, r, Ni)

	points []Point // lazy cache of Point(i) results
}

// New parses sectionData: the complete section 3 bytes, from octet 1 on,
// including the 4-byte length prefix. The common header is: octet 6 = source
// of grid definition (table 3.0), octets 7-10 = numberOfDataPoints (U32,
// authoritative), octet 11 = bytes per point-list entry, octet 12 = point
// list interpretation (table 3.11), octets 13-14 = grid definition template
// number; template content starts at octet 15 (the layout ecCodes's section
// definition files follow as well).
func New(sectionData []byte) (*Definition, error) {
	if len(sectionData) < 15 {
		return nil, fmt.Errorf("grid: section 3 too short: %d bytes, want >= 15", len(sectionData))
	}
	if sectionData[4] != 3 {
		return nil, fmt.Errorf("grid: section number octet = %d, want 3", sectionData[4])
	}
	r := octetio.New(sectionData)
	// The 4-byte length prefix must match the bytes actually passed in.
	// gribw.Build trusts section 3 as-is, so an unchecked prefix would let
	// hand-written sections through with a size inconsistent with their
	// content — exactly the class of file ecCodes rejects.
	slen, err := r.U32(0)
	if err != nil {
		return nil, err
	}
	if int(slen) != len(sectionData) {
		return nil, fmt.Errorf("grid: section 3 length field is %d, section has %d bytes", slen, len(sectionData))
	}
	tmplNo, _ := r.U16(12)  // octets 13-14
	src, _ := r.U8(5)       // octet 6
	declared, _ := r.U32(6) // octets 7-10

	g := &Definition{
		sectionData:    sectionData,
		edition:        2,
		templateNumber: int(tmplNo),
		source:         int(src),
		declared:       int(declared),
	}

	// Walk the template fields when spec knows the template. (Gaussian grids
	// store 40-43 in octets 13-14; 340 is not a legal value.)
	if def, ok := spec.Template(3, int(tmplNo)); ok {
		w, err := spec.NewWalk(def, sectionData)
		if err != nil {
			return nil, fmt.Errorf("grid: template %d walk: %w", tmplNo, err)
		}
		g.walk = w
	}

	if err := g.resolve(); err != nil {
		return nil, err
	}
	// Unimplemented templates (kindNone) do not parse Ni/Nj, but octets
	// 31-34/35-38 still hold Ni/Nj for most templates, and the metadata
	// accessors have always read them there — keep that behavior.
	if g.kind == kindNone {
		if v, err := g.u32(30); err == nil && v != missingU32 {
			g.ni = int(v)
		}
		if v, err := g.u32(34); err == nil && v != missingU32 {
			g.nj = int(v)
		}
	}
	if err := g.validateCounts(); err != nil {
		return nil, err
	}
	return g, nil
}

// validateCounts enforces the point-count caps after resolve(), so
// NumPoints() can never overflow and Point()/Lats()/Lons() are never asked
// for a file-controlled unbounded allocation.
func (g *Definition) validateCounts() error {
	switch g.kind {
	case kindRegular, kindGaussian, kindProjected:
		ni, nj := g.Ni(), g.Nj()
		if ni < 1 || nj < 1 {
			return nil // the coordinate generator reports the precise error
		}
		_, err := checkProduct(fmt.Sprintf("template %d", g.templateNumber), ni, nj)
		return err
	case kindReduced:
		_, err := checkSum("quasi-regular grid", g.reduced.Pl)
		return err
	default:
		return nil // metadata-only templates carry no point table
	}
}

// resolve parses the template-specific fields and selects a coordinate
// generator. Unknown templates stay at kindNone (metadata only).
func (g *Definition) resolve() error {
	tmpl := g.templateNumber
	switch tmpl {
	case 0, 1, 2, 3, 40, 41: // 42/43 (stretched Gaussian) are metadata only
		if err := g.parseLatLonBase(); err != nil {
			return err
		}
		gauss := tmpl == 40 || tmpl == 41
		// Quasi-regular detection follows WMO Note 15 and ecCodes: exactly one
		// of Ni/Nj is all-ones ("missing"). Flag table 3.3 reserves bits 1-2,
		// so the decision is made on the missing count, not on resFlags bits
		// (resFlags&0x01 would read a reserved position).
		niRaw, _ := g.u32(30)
		njRaw, _ := g.u32(34)
		quasi := niRaw == missingU32 || njRaw == missingU32
		switch {
		case quasi:
			if gauss {
				// Reduced Gaussian (WMO Note 15 / ecCodes gridType
				// "reduced_gg"): exactly one of Ni/Nj is all-ones and the
				// real geometry lives in a per-row/column point-count list.
				// In the real ECMWF files (reduced_gg) Ni is the missing
				// field and Nj is the number of latitude circles in the
				// region; the list holds the point count of each circle.
				//
				// Coordinates are withheld in this stage (kindNone: Point/
				// Lats/Lons report ErrNotImplemented) because the enumerate
				// is genuinely not implemented: the row latitudes cannot come
				// from La1->La2 interpolation, they must be the Gaussian
				// parallels selected by N (octets 68-71) through
				// resolveGaussianWindow, and every row carries its own point
				// count from the point list — neither fits the Ni×Nj
				// generator this package has. Keeping the definition
				// metadata-only is deliberate: a real regional reduced
				// Gaussian file must not make the whole GRIB file unreadable
				// (callers enumerate messages and read the first message's
				// grid), and the encoded metadata — template number, Ni/Nj,
				// N, the corners (Bounds) and the declared point count
				// (NumPointsDeclared) — is exactly what ecCodes reports for
				// these files.
				//
				// No Gaussian window/consistency validation happens on this
				// branch, on purpose. resolveGaussianWindow needs
				// O((2N)^2) root finding, requires La1 to sit on a parallel
				// within 1e-6 deg, and requires the Nj-row window to fit in
				// 2N; real producers store the corners rounded to the file's
				// angle unit, so any of those checks can fail on a perfectly
				// readable file. Geometry we never enumerate must not gate
				// metadata. On the coordinate path (kindGaussian below) the
				// same checks stay fail-loud, unchanged.
				if n, err := g.u32(67); err == nil && n != missingU32 && n != 0 && n <= maxGaussianN {
					g.gaussN = int(n)
				}
				g.kind = kindNone
				g.reducedGauss = true
				break
			}
			if err := g.parseReduced(); err != nil {
				return err
			}
			g.kind = kindReduced
		case gauss:
			g.kind = kindGaussian
			n, err := g.u32(67) // octets 68-71: N (parallels pole-to-equator)
			if err != nil {
				return err
			}
			if n == missingU32 || n == 0 {
				return fmt.Errorf("grid: template %d: N (octets 68-71) missing or zero, Note 87 requires it", tmpl)
			}
			if int(n) > maxGaussianN {
				return fmt.Errorf("grid: template %d: N=%d exceeds the supported maximum %d", tmpl, n, maxGaussianN)
			}
			g.gaussN = int(n)
			if g.Nj() > 2*g.gaussN {
				return fmt.Errorf("grid: template %d: Nj=%d exceeds total parallels 2N=%d", tmpl, g.Nj(), 2*g.gaussN)
			}
			if err := g.resolveGaussianWindow(); err != nil {
				return err
			}
		default:
			g.kind = kindRegular
		}

		// Rotation / stretching suffix fields (all angles per the scaled-unit
		// rules below).
		unit := g.angleUnit()
		switch tmpl {
		case 1, 41: // rotated lat/lon / rotated Gaussian
			spLat, err := g.scaledAngle(72, unit) // octets 73-76
			if err != nil {
				return err
			}
			spLon, err := g.scaledAngle(76, unit) // octets 77-80
			if err != nil {
				return err
			}
			angle, err := g.floatAngle(80) // octets 81-84 (F32 degrees, the ecCodes convention)
			if err != nil {
				return err
			}
			g.spLat, g.spLon, g.rotAngle = spLat, spLon, angle
			if tmpl == 1 {
				g.rotated = true
			}
			// For template 3.41 the rotation fields stay metadata only: the
			// ecCodes Gaussian iterator (Gaussian.cc) never unrotates Gaussian
			// coordinates — a t41 sample with a nonzero rotation pole reads
			// back in the unrotated frame, and a zero-parameter grid gives
			// the identity.
		case 2: // stretched lat/lon
			if _, err := g.scaledAngle(72, unit); err != nil { // stretching pole latitude (metadata)
				return err
			}
			if _, err := g.scaledAngle(76, unit); err != nil { // stretching pole longitude (metadata)
				return err
			}
			raw, err := g.u32(80) // octets 81-84: stretching factor, scaled by 1e6
			if err != nil {
				return err
			}
			g.stretchR = float64(raw) / 1e6
			if g.stretchR <= 0 {
				return fmt.Errorf("grid: template 2: stretching factor %g must be > 0", g.stretchR)
			}
			g.stretched = true
		case 3: // stretched and rotated lat/lon
			spLat, err := g.scaledAngle(72, unit)
			if err != nil {
				return err
			}
			spLon, err := g.scaledAngle(76, unit)
			if err != nil {
				return err
			}
			angle, err := g.floatAngle(80) // octets 81-84 (F32 degrees, the ecCodes convention)
			if err != nil {
				return err
			}
			g.spLat, g.spLon, g.rotAngle = spLat, spLon, angle
			g.rotated = true
			if _, err := g.scaledAngle(84, unit); err != nil { // stretching pole latitude (metadata)
				return err
			}
			if _, err := g.scaledAngle(88, unit); err != nil { // stretching pole longitude (metadata)
				return err
			}
			raw, err := g.u32(92) // octets 93-96: stretching factor
			if err != nil {
				return err
			}
			g.stretchR = float64(raw) / 1e6
			if g.stretchR <= 0 {
				return fmt.Errorf("grid: template 3: stretching factor %g must be > 0", g.stretchR)
			}
			g.stretched = true
		}
		return nil
	case 20, 30: // polar stereographic / Lambert conformal
		if err := g.parseProjected(); err != nil {
			return err
		}
		g.kind = kindProjected
		return nil
	default:
		// Unimplemented template: metadata only.
		return nil
	}
}

// parseLatLonBase reads the base fields shared by the latitude/longitude
// family (templates 3.0-3.3, 3.40-3.43). All offsets are 0-based from the
// start of sectionData; angles come out in degrees (per the
// basicAngle/subdivisions unit), and Di/Dj go through the default-value
// algorithm for absent increments.
func (g *Definition) parseLatLonBase() error {
	unit := g.angleUnit()

	shape, err := g.u8(14) // octet 15
	if err != nil {
		return err
	}
	g.shape = int(shape)
	rs, _ := g.u8(15)
	g.radiusScl = rs
	g.radiusRaw, _ = g.u32(16)
	ms, _ := g.u8(20)
	g.majorScl = ms
	g.majorRaw, _ = g.u32(21)
	ns, _ := g.u8(25)
	g.minorScl = ns
	g.minorRaw, _ = g.u32(26)

	ni, err := g.u32(30) // octets 31-34
	if err != nil {
		return err
	}
	nj, err := g.u32(34) // octets 35-38
	if err != nil {
		return err
	}
	if ni != missingU32 {
		g.ni = int(ni)
	}
	if nj != missingU32 {
		g.nj = int(nj)
	}

	g.La1, err = g.scaledAngle(46, unit) // octets 47-50
	if err != nil {
		return err
	}
	g.Lo1, err = g.scaledAngle(50, unit) // octets 51-54
	if err != nil {
		return err
	}
	rf, err := g.u8(54) // octet 55: resFlags (flag table 3.3)
	if err != nil {
		return err
	}
	g.resFlags = rf
	g.La2, err = g.scaledAngle(55, unit) // octets 56-59
	if err != nil {
		return err
	}
	g.Lo2, err = g.scaledAngle(59, unit) // octets 60-63
	if err != nil {
		return err
	}
	// All four encoded corners are decoded: Bounds() is available from here
	// on (for every template that reaches this function).
	g.hasCorners = true
	diRaw, err := g.u32(63) // octets 64-67
	if err != nil {
		return err
	}
	djRaw, err := g.u32(67) // octets 68-71 (Dj for 3.0-3.3; N for 3.40/3.41)
	if err != nil {
		return err
	}
	sm, err := g.u8(71) // octet 72: scanMode (flag table 3.4)
	if err != nil {
		return err
	}
	g.scanMode = sm

	// Di/Dj go through the default-value algorithm for absent increments. On
	// Gaussian templates the field in octets 68-71 is N rather than Dj, so
	// Dj is derived from La1/La2. A stored (non-all-ones) increment is
	// authoritative; the span check below cross-checks it instead of
	// silently preferring the derived value. Flag table 3.3 bits 3/4
	// ("increment given") are advisory only: compliant files agree with the
	// field presence, but nothing here forces them (sloppy producers exist).
	// All-ones raw values mean missing.
	//
	// Di/Dj are unsigned (WMO template 3.0 Note 5: "recommended to use
	// unsigned direction increments"; ecCodes declares them unsigned[4] in
	// template.3.latlon.def). Scan direction is carried by the scan mode,
	// not by a sign. A "negative" increment in two's-complement or
	// sign-magnitude form is therefore a discouraged encoding: it decodes
	// into a huge positive value and is rejected by the span check below
	// rather than silently misplacing the grid.
	diGiven := diRaw != missingU32
	djGiven := djRaw != missingU32
	if tmplIsGaussian(g.templateNumber) {
		djGiven = false
	}

	nii, nji := countOrZero(ni), countOrZero(nj)
	quasiCount := ni == missingU32 || nj == missingU32
	if diGiven {
		g.Di = float64(diRaw) * unit
	} else if nii > 1 {
		g.Di = math.Abs(g.Lo2-g.Lo1) / float64(nii-1) // default for a missing increment
	} else if quasiCount {
		// Quasi-regular grids place rows/columns from the corner span plus the
		// point-count list (reducedPoints) and never use Di/Dj, so a missing
		// increment is not an error for them. Keeping this branch error-free is
		// what makes the metadata fallback unconditional: on 64-bit ints the
		// all-ones sentinel happened to survive as a nonsensical ~1e-9 deg
		// increment, but on 32-bit ints int(0xFFFFFFFF) is -1 and the
		// derivation below would reject a readable file (reduced Gaussian
		// templates have a missing Ni or Nj and usually a missing Di).
		g.Di = 0
	} else {
		return fmt.Errorf("grid: template %d: i increment missing and Ni=%d, cannot derive Di", g.templateNumber, nii)
	}
	if djGiven {
		g.Dj = float64(djRaw) * unit
	} else if nji > 1 {
		g.Dj = math.Abs(g.La2-g.La1) / float64(nji-1) // default for a missing increment
	} else if quasiCount {
		g.Dj = 0 // see the Di branch above
	} else {
		return fmt.Errorf("grid: template %d: j increment missing and Nj=%d, cannot derive Dj", g.templateNumber, nji)
	}

	// Fail loud when the file stores both Di/Dj and Lo1/Lo2 (La1/La2): the
	// stored increment must reproduce the corner span, |Ni-1|*Di == |Lo2-Lo1|
	// (for longitude the 360-degree wrap is an accepted alternative;
	// tolerance 1e-6 degrees). Self-consistent files never trip this.
	// Gaussian grids get extra tolerance: the stored Di is a quantized echo
	// of the real spacing (template 3.40 defines it as 360/Ni; the ecCodes
	// T32 gg sample stores 2.813 instead of 2.8125 — coarser by 1e-3 degrees
	// than the file's own angle unit). Per-step rounding is at most 5e-4
	// degrees and accumulates over Ni-1 steps, hence the tolerance covers
	// it; a genuine contradiction (Di off by more) still fails loud.
	if diGiven && nii > 1 && ni != missingU32 {
		span := math.Abs(g.Lo2 - g.Lo1)
		wantSpan := float64(nii-1) * math.Abs(g.Di)
		tol := 1e-6
		if tmplIsGaussian(g.templateNumber) {
			tol += float64(nii-1) * 5e-4
		}
		if math.Abs(span-wantSpan) > tol && math.Abs((360-span)-wantSpan) > tol {
			return fmt.Errorf("grid: template %d: stored Di=%g deg contradicts Lo1/Lo2 span (|Ni-1|*Di=%g, |Lo2-Lo1|=%g or wrapped %g)",
				g.templateNumber, g.Di, wantSpan, span, 360-span)
		}
	}
	if djGiven && nji > 1 && nj != missingU32 {
		span := math.Abs(g.La2 - g.La1)
		wantSpan := float64(nji-1) * math.Abs(g.Dj)
		if math.Abs(span-wantSpan) > 1e-6 {
			return fmt.Errorf("grid: template %d: stored Dj=%g deg contradicts La1/La2 span (|Nj-1|*Dj=%g, |La2-La1|=%g)",
				g.templateNumber, g.Dj, wantSpan, span)
		}
	}
	return nil
}

// parseReduced reads the per-row/column point-count list and fills
// reducedParams. The list starts right after the scan mode (octet 73) for
// template 3.0, but after the rotation/stretching fields for 3.1/3.2
// (octets 73-84, so from octet 85) and 3.3 (octets 73-96, so from octet 97),
// per the WMO template layouts.
func (g *Definition) parseReduced() error {
	width := int(g.sectionData[10]) // octet 11: numberOfOctetsForNumberOfPoints
	if width < 1 || width > 4 {
		return fmt.Errorf("grid: quasi-regular grid: point list element width %d, want 1..4", width)
	}
	listStart := map[int]int{0: 72, 1: 84, 2: 84, 3: 96}[g.templateNumber] // 0-based offset
	if listStart == 0 {
		return fmt.Errorf("grid: quasi-regular grid: template %d point list position unknown", g.templateNumber)
	}
	if listStart > len(g.sectionData) {
		return fmt.Errorf("grid: quasi-regular grid: template %d needs at least %d bytes before the point list, section has %d",
			g.templateNumber, listStart, len(g.sectionData))
	}
	list := g.sectionData[listStart:]
	if len(list)%width != 0 {
		return fmt.Errorf("grid: quasi-regular grid: point list length %d not a multiple of element width %d", len(list), width)
	}
	n := len(list) / width
	pl := make([]int, n)
	for i := 0; i < n; i++ {
		var v uint64
		for b := 0; b < width; b++ {
			v = v<<8 | uint64(list[i*width+b])
		}
		pl[i] = int(v)
	}

	ni, err := g.u32(30)
	if err != nil {
		return err
	}
	nj, err := g.u32(34)
	if err != nil {
		return err
	}
	rp := &reducedParams{La1: g.La1, Lo1: g.Lo1, La2: g.La2, Lo2: g.Lo2}
	switch {
	case ni == missingU32 && nj != missingU32: // Ni varies: the count list is per row
		rp.Nj = int(nj)
		if len(pl) != rp.Nj {
			return fmt.Errorf("grid: quasi-regular grid: point list length %d != Nj %d", len(pl), rp.Nj)
		}
	case nj == missingU32 && ni != missingU32: // Nj varies: the count list is per column
		rp.Ni = int(ni)
		if len(pl) != rp.Ni {
			return fmt.Errorf("grid: quasi-regular grid: point list length %d != Ni %d", len(pl), rp.Ni)
		}
	default:
		return fmt.Errorf("grid: quasi-regular grid: exactly one of Ni/Nj must be missing (Ni=%d Nj=%d)", ni, nj)
	}
	rp.Pl = pl
	g.reduced = rp
	return nil
}

// ---- Field readers ---------------------------------------------------------

func (g *Definition) u8(off int) (uint8, error) {
	if off+1 > len(g.sectionData) {
		return 0, fmt.Errorf("grid: section 3 too short for octet %d", off+1)
	}
	return g.sectionData[off], nil
}

func (g *Definition) u32(off int) (uint32, error) {
	if off+4 > len(g.sectionData) {
		return 0, fmt.Errorf("grid: section 3 too short for octets %d-%d", off+1, off+4)
	}
	r := octetio.New(g.sectionData)
	v, err := r.U32(off)
	if err != nil {
		return 0, err
	}
	return v, nil
}

// countOrZero maps a cardinal field whose all-ones value is the WMO "missing"
// sentinel to 0, passing any other value through. Counts used in arithmetic
// (the missing-increment default algorithm) must never see the sentinel:
// int(0xFFFFFFFF) is 4294967295 on 64-bit ints — a nonsense count — and -1 on
// 32-bit ones, where it used to turn a readable quasi-regular grid into an
// error.
func countOrZero(v uint32) int {
	if v == missingU32 {
		return 0
	}
	return int(v)
}

// angleUnit returns the angular unit for scaled-angle fields: the degrees
// per stored unit from basicAngle (octets 39-42) divided by subdivisions
// (octets 43-46). Defaults per the specification: a basicAngle of 0 treats
// the numerator as 1, and missing/zero subdivisions give 1e6 (microdegrees).
func (g *Definition) angleUnit() float64 {
	ba, err := g.u32(38) // octets 39-42: basicAngle
	if err != nil {
		return 1e-6
	}
	sub, err := g.u32(42) // octets 43-46: subdivisions
	if err != nil {
		return 1e-6
	}
	num := float64(ba)
	if num == 0 || ba == missingU32 {
		num = 1 // a basicAngle of 0 counts as 1
	}
	den := float64(sub)
	if sub == 0 || sub == missingU32 {
		den = 1e6 // missing/zero subdivisions -> microdegrees
	}
	return num / den
}

// scaledAngle decodes a 4-byte scaled angle (in degrees) at 0-based offset
// off.
//
// Convention: sign-magnitude — bit 31 is the sign, the low 31 bits the
// magnitude. This is the de facto standard of every real GRIB2
// implementation: NCEP g2c/wgrib2 (src/int8.c:int4, an explicit
// sign-magnitude branch) and ECMWF ecCodes 2.41.0 (grib_set writes -45e6 as
// 0x82AEA540 and reads it back). A two's-complement byte pattern decodes to
// a magnitude of about 2^31 microdegrees under this reading, which the
// existing range/span checks reject — no real file uses that encoding.
//
// All-ones is the missing sentinel, checked on the unsigned value before
// the sign is examined.
func (g *Definition) scaledAngle(off int, unit float64) (float64, error) {
	raw, err := g.u32(off)
	if err != nil {
		return 0, err
	}
	if raw == missingU32 {
		return 0, fmt.Errorf("grid: scaled angle at octets %d-%d is missing (all ones)", off+1, off+4)
	}
	return float64(smagInt32(raw)) * unit, nil
}

// smagInt32 decodes a sign-magnitude int32: bit 31 = sign, low 31 bits =
// magnitude. (0x80000000 decodes to -0 == 0; the two's-complement minimum
// has no sign-magnitude counterpart.)
func smagInt32(raw uint32) int32 {
	if raw&0x80000000 != 0 {
		return -int32(raw & 0x7FFFFFFF)
	}
	return int32(raw)
}

// floatAngle reads a projection rotation angle stored as IEEE float32
// degrees. The WMO CSV defines no scaled form for this field (no Note
// reference); ecCodes stores it as ieeefloat (template.3.rotation.def:
// "ieeefloat angleOfRotation"), and this package follows the same
// convention.
func (g *Definition) floatAngle(off int) (float64, error) {
	raw, err := g.u32(off)
	if err != nil {
		return 0, err
	}
	return float64(math.Float32frombits(uint32(raw))), nil
}

// ---- Accessors ------------------------------------------------------------

func (g *Definition) TemplateNumber() int { return g.templateNumber }

func (g *Definition) SourceOfGridDefinition() int { return g.source }

func (g *Definition) NumPointsDeclared() int { return g.declared }

// Bounds returns the encoded grid corners in degrees, in the order
// (lat1, lon1, lat2, lon2) — the first and last grid point of the grid
// definition, decoded straight from the template fields with no scan-mode or
// quasi-regular processing. It is the semantic equivalent of ecCodes'
// latitudeOfFirstGridPointInDegrees, longitudeOfFirstGridPointInDegrees,
// latitudeOfLastGridPointInDegrees and longitudeOfLastGridPointInDegrees.
//
// ok is false when the template carries no corner fields at all: the
// projections (3.20/3.30, which encode a projection origin instead), the
// stretched Gaussian family 3.42/3.43 and templates this package does not
// decode. The corner values are also parsed for GRIB1 types 0/4/10 (NewGRIB1).
//
// Bounds is the metadata path for grids whose coordinates are not
// enumerated. For a regional reduced Gaussian grid (see IsReducedGaussian)
// the encoded corners are all the geometry the file encodes without the
// point list, so a caller can still report the lat/lon box — the same values
// ecCodes gives for these files.
func (g *Definition) Bounds() (lat1, lon1, lat2, lon2 float64, ok bool) {
	if !g.hasCorners {
		return 0, 0, 0, 0, false
	}
	return g.La1, g.Lo1, g.La2, g.Lo2, true
}

// IsReducedGaussian reports whether the definition is a reduced Gaussian grid
// this package can currently describe as metadata only: GRIB2 template 3.40 or
// 3.41 with Ni or Nj encoded as missing (WMO Note 15; ecCodes gridType
// "reduced_gg"). Such a definition parses without error, reports its encoded
// corners through Bounds and its Gaussian parameter through GaussianN, and
// Point/Lats/Lons report ErrNotImplemented.
//
// Convention for these definitions, because the coordinate generator is
// withheld: NumPoints() returns 0 (no point table can be built without the
// per-row point counts and the N parallels), while NumPointsDeclared() still
// returns the count encoded in section 3 octets 7-10 (e.g. 62644 or 8505906),
// which is exactly ecCodes' numberOfPoints. Callers that need the grid size
// must use NumPointsDeclared() for a reduced Gaussian grid.
//
// Quasi-regular grids whose point-count list this package does enumerate
// (the 3.0-3.3 family) report IsQuasiRegular() instead, and never report
// true here: this flag covers the GRIB2 templates 3.40/3.41 only.
func (g *Definition) IsReducedGaussian() bool { return g.reducedGauss }

// GaussianN returns the Gaussian parameter N of a template 3.40/3.41 grid:
// the number of parallels from the pole to the equator, so the full grid has
// 2N rows. N is read from octets 68-71 and is available both for enumerated
// Gaussian grids and for metadata-only reduced Gaussian grids
// (IsReducedGaussian).
//
// It returns 0 when N is not usable: the definition is not a 3.40/3.41 grid,
// the field is missing (all-ones) or zero (Note 87 requires N, so such a file
// is malformed), or the encoded value exceeds the supported maximum
// maxGaussianN (2^15) — building a latitude list is O((2N)^2), so an
// unbounded value is never handed out.
func (g *Definition) GaussianN() int { return g.gaussN }

func (g *Definition) ShapeOfEarth() int { return g.shape }

// Radius returns the (effective) earth radius in metres. Fixed shapes return
// their documented radius; shapes with producer-supplied dimensions decode
// the scaled fields (shape 1: radius in metres, octets 17-20; shapes 3/7:
// semi-major axis in km / metres). Oblate shapes return the semi-major
// axis. A missing or unusable producer-supplied dimension returns NaN —
// failing loudly the same way as an unknown shape.
//
// Scale factors are unsigned[1] per ecCodes template.3.shape_of_the_earth.def
// (`unsigned[1] scaleFactorOfRadiusOfSphericalEarth = missing() :
// can_be_missing`); 0xFF is the missing sentinel. (Decoding them as
// two's-complement int8 would be wrong for byte values >= 128.)
func (g *Definition) Radius() float64 {
	switch g.shape {
	case 0:
		return 6367470
	case 1:
		return earthDimension(g.radiusRaw, g.radiusScl, 1)
	case 2:
		return 6378160
	case 3:
		return earthDimension(g.majorRaw, g.majorScl, 1e3) // km -> m
	case 4, 5:
		return 6378137
	case 6:
		return 6371229
	case 7:
		return earthDimension(g.majorRaw, g.majorScl, 1)
	case 8:
		return 6371200
	case 9: // Airy / OSGB 1936 semi-major axis
		return 6377563.396
	case 10: // WGS84: same semi-major axis as the WGS84 ellipsoid
		return 6378137
	case 11:
		return 695990000
	default:
		return math.NaN() // fail loud as "missing": an unknown shape has no radius
	}
}

// earthDimension decodes a producer-supplied earth dimension:
// raw × 10^(-scale) × unit, where a 0xFF scale and an all-ones raw are
// treated as the WMO/ecCodes "missing" sentinels (result NaN).
func earthDimension(raw uint32, scale uint8, unit float64) float64 {
	if scale == missingU8 || raw == missingU32 {
		return math.NaN()
	}
	return float64(raw) * math.Pow(10, -float64(scale)) * unit
}

func (g *Definition) IsQuasiRegular() bool { return g.kind == kindReduced }

// Ni/Nj return the encoded counts; a missing (all-ones) field reads as 0.
// Both are parsed and cached at construction, so the same accessors work for
// GRIB1 (different octet positions) and GRIB2.
func (g *Definition) Ni() int { return g.ni }

func (g *Definition) Nj() int { return g.nj }

// ScanMode returns the scan-mode byte (GRIB1 GDS octet 28 and GRIB2 flag
// table 3.4 bits 1-3 carry the same meaning). The GRIB1 writer needs it
// verbatim to copy grids.
func (g *Definition) ScanMode() uint8 { return g.scanMode }

// NumPoints returns the actual grid point count: regular Ni×Nj,
// quasi-regular sum(PL), Gaussian Ni×Nj, projected (3.20/3.30) Ni×Nj;
// unimplemented templates give 0. The count has been validated in New()
// against maxGridPoints, so it neither overflows nor is unbounded.
//
// A metadata-only reduced Gaussian grid (IsReducedGaussian) also returns 0:
// its point table cannot be enumerated at this stage. For those the real
// count is NumPointsDeclared() (section 3 octets 7-10).
func (g *Definition) NumPoints() int {
	switch g.kind {
	case kindRegular, kindGaussian, kindProjected:
		return g.Ni() * g.Nj()
	case kindReduced:
		total := 0
		for _, n := range g.reduced.Pl {
			total += n
		}
		return total
	default:
		return 0
	}
}

// Point returns the i-th grid point in data (scan) order, in degrees
// (lat/lon). The full table is generated and cached on first use.
// Unimplemented templates return ErrNotImplemented{Template}.
func (g *Definition) Point(i int) (float64, float64, error) {
	if err := g.ensurePoints(); err != nil {
		return 0, 0, err
	}
	if i < 0 || i >= len(g.points) {
		return 0, 0, fmt.Errorf("grid: point index %d out of range [0,%d)", i, len(g.points))
	}
	return g.points[i].Lat, g.points[i].Lon, nil
}

// Lats/Lons return per-row latitudes / per-column longitudes for regular and
// Gaussian grids; quasi-regular grids return an error (their columns/rows are
// not separable) and templates without a coordinate generator — including a
// metadata-only reduced Gaussian, see IsReducedGaussian — report
// ErrNotImplemented{Template}, the same error Point returns.
// Projected grids (3.20/3.30) have no separable row/column structure:
// Lats()/Lons() return the full per-point arrays in the specification data
// order (length Ni×Nj, longitudes in [0,360), the ecCodes convention).
func (g *Definition) Lats() ([]float64, error) {
	switch g.kind {
	case kindRegular, kindGaussian:
		n := g.Nj()
		out := make([]float64, n)
		for j := 0; j < n; j++ {
			out[j] = g.rowLat(j)
		}
		return out, nil
	case kindProjected:
		if err := g.ensurePoints(); err != nil {
			return nil, err
		}
		out := make([]float64, len(g.points))
		for i, pt := range g.points {
			out[i] = pt.Lat
		}
		return out, nil
	default:
		if g.kind == kindNone {
			return nil, &ErrNotImplemented{Template: g.templateNumber}
		}
		return nil, fmt.Errorf("grid: Lats not available for template %d (kind %v)", g.templateNumber, g.kind)
	}
}

func (g *Definition) Lons() ([]float64, error) {
	switch g.kind {
	case kindRegular, kindGaussian:
		n := g.Ni()
		out := make([]float64, n)
		for i := 0; i < n; i++ {
			out[i] = normalizeLon(g.lonForColumn(i))
		}
		return out, nil
	case kindProjected:
		if err := g.ensurePoints(); err != nil {
			return nil, err
		}
		out := make([]float64, len(g.points))
		for i, pt := range g.points {
			out[i] = pt.Lon
		}
		return out, nil
	default:
		if g.kind == kindNone {
			return nil, &ErrNotImplemented{Template: g.templateNumber}
		}
		return nil, fmt.Errorf("grid: Lons not available for template %d (kind %v)", g.templateNumber, g.kind)
	}
}

// Walk returns the spec.Walk over the template fields, or nil when the
// template number has no definition in the spec package.
func (g *Definition) Walk() *spec.Walk { return g.walk }

// ---- Coordinate generation ------------------------------------------------

func tmplIsGaussian(tmpl int) bool { return tmpl == 40 || tmpl == 41 }

// ensurePoints generates and caches the full point table in data (scan)
// order, applying stretching (along i) and rotation where applicable.
func (g *Definition) ensurePoints() error {
	if g.points != nil {
		return nil
	}
	var base []Point
	var err error
	switch g.kind {
	case kindRegular:
		base, err = g.regularPoints()
	case kindReduced:
		// Quasi-regular base coordinates are placed uniformly, which is
		// inconsistent with the geometric stretching series, and no reference
		// implementation verifies the rotated combination. Fail loud at
		// coordinate time; metadata stays readable.
		if g.rotated && g.stretched {
			return fmt.Errorf("grid: quasi-regular grid with rotation and stretching: coordinates not implemented (fail-loud rather than guessing)")
		}
		if g.rotated {
			return fmt.Errorf("grid: quasi-regular grid with rotation: coordinates not implemented (fail-loud rather than guessing)")
		}
		if g.stretched {
			return fmt.Errorf("grid: quasi-regular grid with stretching: coordinates not implemented (fail-loud rather than guessing)")
		}
		base, err = g.reducedPoints()
	case kindGaussian:
		base, err = g.gaussianPoints()
	case kindProjected:
		// Projected coordinates are filled in specification order with no
		// rotation/stretch/scan-mode post-processing.
		base, err = g.projectedPoints()
	default:
		return &ErrNotImplemented{Template: g.templateNumber}
	}
	if err != nil {
		return err
	}
	if g.rotated {
		for i := range base {
			base[i].Lat, base[i].Lon = unrotate(base[i].Lat, base[i].Lon, g.rotAngle, g.spLat, g.spLon)
		}
	}
	g.points = base
	return nil
}

// regularPoints generates the template 3.0 scan-ordered table. Bit 16 of the
// scan mode (full serpentine) is supported for i-fastest grids: every row
// reverses the i direction relative to the previous one. The odd/even offset
// bits and the serpentine + j-fastest combination fail loud.
func (g *Definition) regularPoints() ([]Point, error) {
	ni, nj := g.Ni(), g.Nj()
	if ni < 1 || nj < 1 {
		return nil, fmt.Errorf("grid: Ni=%d Nj=%d, want >= 1", ni, nj)
	}
	if m := g.scanMode & scanOffsets; m != 0 {
		return nil, fmt.Errorf("grid: scan mode %d sets odd/even line offset bit(s) %d, not supported", g.scanMode, m)
	}
	serpentine := g.scanMode&scanFirstRev != 0
	if serpentine && g.scanMode&scanJFast != 0 {
		return nil, fmt.Errorf("grid: scan mode %d: first-line reversal combined with j-fastest is not supported", g.scanMode)
	}

	jDir := 1.0
	if g.scanMode&scanPosJ == 0 {
		jDir = -1
	}

	out := make([]Point, 0, ni*nj)
	add := func(i, j int) {
		out = append(out, Point{
			Lat: g.La1 + float64(j)*jDir*g.Dj,
			Lon: normalizeLon(g.lonForColumn(i)),
		})
	}
	if g.scanMode&scanJFast != 0 {
		for i := 0; i < ni; i++ { // j fastest
			for j := 0; j < nj; j++ {
				add(i, j)
			}
		}
		return out, nil
	}
	for j := 0; j < nj; j++ { // i fastest (standard)
		iStep := 1
		if serpentine && j%2 == 1 {
			iStep = -1 // full serpentine: odd rows reverse
		}
		for k := 0; k < ni; k++ {
			var i int
			if iStep > 0 {
				i = k
			} else {
				i = ni - 1 - k
			}
			add(i, j)
		}
	}
	return out, nil
}

// reducedPoints generates the quasi-regular table. Data order advances from
// (La1, Lo1) toward (La2, Lo2) — the direction is carried by the corner
// fields, which the regular-grid span check keeps self-consistent, so this
// is the only reading. j-fastest is rejected.
func (g *Definition) reducedPoints() ([]Point, error) {
	if g.scanMode&scanJFast != 0 {
		return nil, fmt.Errorf("grid: quasi-regular grid with j-fastest scan mode %d is not supported", g.scanMode)
	}
	rp := g.reduced

	total := 0
	for _, n := range rp.Pl {
		total += n
	}
	out := make([]Point, 0, total)

	switch {
	case rp.Nj > 0 && rp.Ni == 0: // Ni varies: Nj rows with Pl[j] points each
		for j := 0; j < rp.Nj; j++ {
			lat := lineCoord(rp.La1, rp.La2, rp.Nj, j)
			n := rp.Pl[j]
			for k := 0; k < n; k++ {
				out = append(out, Point{Lat: lat, Lon: normalizeLon(lineCoord(rp.Lo1, rp.Lo2, n, k))})
			}
		}
	case rp.Ni > 0 && rp.Nj == 0: // Nj varies: Ni columns with Pl[i] points each
		for i := 0; i < rp.Ni; i++ {
			lon := normalizeLon(lineCoord(rp.Lo1, rp.Lo2, rp.Ni, i))
			n := rp.Pl[i]
			for k := 0; k < n; k++ {
				out = append(out, Point{Lat: lineCoord(rp.La1, rp.La2, n, k), Lon: lon})
			}
		}
	default:
		return nil, fmt.Errorf("grid: quasi-regular grid: inconsistent Ni/Nj")
	}
	return out, nil
}

// gaussianPoints generates the 3.40/3.41 table: the 2N parallels come from
// the roots of P_{2N}; the sub-area window of Nj rows is obtained by
// locating La1 in the full list (tolerance 1e-6 degrees) and stepping Nj
// rows in the j scan direction. Longitudes are evenly spaced as in 3.0.
func (g *Definition) gaussianPoints() ([]Point, error) {
	ni, nj := g.Ni(), g.Nj()
	if ni < 1 || nj < 1 {
		return nil, fmt.Errorf("grid: Ni=%d Nj=%d, want >= 1", ni, nj)
	}
	full, err := g.fullGaussianLats() // degrees, north to south (decreasing)
	if err != nil {
		return nil, err
	}

	out := make([]Point, 0, ni*nj)
	add := func(i, j int) {
		row := g.gaussStart + j*g.gaussJDir
		out = append(out, Point{
			Lat: full[row],
			Lon: normalizeLon(g.lonForColumn(i)),
		})
	}
	if g.scanMode&scanJFast != 0 {
		for i := 0; i < ni; i++ {
			for j := 0; j < nj; j++ {
				add(i, j)
			}
		}
		return out, nil
	}
	for j := 0; j < nj; j++ {
		for i := 0; i < ni; i++ {
			add(i, j)
		}
	}
	return out, nil
}

// rowLat/colLon give the unrotated, unstretched latitude of row j /
// longitude of column i (used by Lats/Lons and Point generation).
func (g *Definition) rowLat(j int) float64 {
	if g.kind == kindGaussian {
		full, err := g.fullGaussianLats()
		if err != nil {
			return math.NaN() // cannot fail after resolve; keep the accessors total
		}
		return full[g.gaussStart+j*g.gaussJDir]
	}
	jDir := 1.0
	if g.scanMode&scanPosJ == 0 {
		jDir = -1
	}
	return g.La1 + float64(j)*jDir*g.Dj
}

func (g *Definition) colLon(i int) float64 {
	iDir := 1.0
	if g.scanMode&scanNegI != 0 {
		iDir = -1
	}
	return g.Lo1 + float64(i)*iDir*g.Di
}

// fullGaussianLats returns all 2N Gaussian parallel latitudes in degrees,
// running north to south (the roots of P_{2N}).
func (g *Definition) fullGaussianLats() ([]float64, error) {
	colats, err := gaussianColatitudes(2 * g.gaussN)
	if err != nil {
		return nil, err
	}
	const deg = 180.0 / math.Pi
	out := make([]float64, len(colats))
	for i, th := range colats {
		out[i] = 90.0 - th*deg
	}
	return out, nil
}

// resolveGaussianWindow locates La1 in the full parallel list (tolerance
// 1e-6 degrees) and stores the sub-area window start/direction: the ecCodes
// Gaussian iterator finds La1 by binary search and walks Nj rows in the j
// direction. Global grids (Nj == 2N) use the whole list.
func (g *Definition) resolveGaussianWindow() error {
	full, err := g.fullGaussianLats()
	if err != nil {
		return err
	}
	nj := g.Nj()
	if nj == len(full) {
		g.gaussStart, g.gaussJDir = 0, 1
		return nil
	}
	const tol = 1e-6 // degrees
	found := -1
	for i, lat := range full {
		if math.Abs(lat-g.La1) <= tol {
			found = i
			break
		}
	}
	if found < 0 {
		return fmt.Errorf("grid: Gaussian sub-area: La1=%g not found among the %d parallels (tolerance %g deg)", g.La1, len(full), tol)
	}
	// The full list runs north to south, so a positive-j scan (latitudes
	// increasing) walks toward lower indices (northward), while
	// jScansPositively=0 walks index++ (southward) — matching ecCodes
	// Gaussian.cc. Mapping the latitude-space direction into index space
	// directly would reject standard northward windows.
	jDir := 1
	if g.scanMode&scanPosJ != 0 {
		jDir = -1
	}
	last := found + jDir*(nj-1)
	if last < 0 || last >= len(full) {
		return fmt.Errorf("grid: Gaussian sub-area: Nj=%d window from row %d overruns the %d parallels", nj, found, len(full))
	}
	g.gaussStart, g.gaussJDir = found, jDir
	return nil
}

func (g *Definition) lonForColumn(i int) float64 {
	if !g.stretched {
		// On full Gaussian grids (template 3.40) the longitude spacing is
		// defined as 360/Ni (Ni evenly spaced points per latitude circle);
		// the stored Di is just a quantized echo of it (T32: 2.813 vs
		// 2.8125), and using it would drift the last column by up to
		// (Ni-1)*unit/2. ecCodes computes i*360/Ni on its own gg samples.
		// Sub-area t41 keeps the stored Di: the full-grid Ni is not stored,
		// so dividing 360 by the window Ni would be wrong.
		if g.templateNumber == 40 {
			iDir := 1.0
			if g.scanMode&scanNegI != 0 {
				iDir = -1
			}
			return g.Lo1 + float64(i)*iDir*(360.0/float64(g.Ni()))
		}
		return g.colLon(i)
	}
	if g.stretchedLons == nil {
		iDir := 1.0
		if g.scanMode&scanNegI != 0 {
			iDir = -1
		}
		g.stretchedLons = stretchedCoords(g.Lo1, iDir*g.Di, g.stretchR, g.Ni())
	}
	return g.stretchedLons[i]
}
