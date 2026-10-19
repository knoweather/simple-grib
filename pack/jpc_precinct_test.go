//
// Precinct geometry self-checks. The CMA corpus contains no codestream that
// declares precincts (all four anchors: Scod = 0x00), so the precinct path is
// proven here instead: the p-series fixtures must actually build a precinct
// grid with more than one precinct on at least one level, and the guard that
// rejects precinct exponents too small for the code-block group must fire.

package pack

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func precinctFixture(t *testing.T, name string) *jpcImage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "p7-3-j2k", name+".j2k"))
	if err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	img, err := parseJPC(data)
	if err != nil {
		t.Fatalf("%s: parseJPC: %v", name, err)
	}
	if img.cs.csty&jpcCoxPrt == 0 {
		t.Fatalf("%s: COD does not declare precincts (Scod = 0x%02x)", name, img.cs.csty)
	}
	return img
}

// TestJPCPrecinctGridIsBuilt asserts that the p series really exercises the
// precinct machinery: some resolution level must hold more than one precinct,
// the precincts of a band must carry their own code-block grids and tag trees,
// and across the series at least one band must code more than one precinct (so
// the precinct-major packet order is genuinely walked).
func TestJPCPrecinctGridIsBuilt(t *testing.T) {
	seriesBest := 0
	for _, name := range []string{"p01", "p02", "p03"} {
		t.Run(name, func(t *testing.T) {
			img := precinctFixture(t, name)
			w, h := img.xsiz, img.ysiz
			td := &jpcTileDecoder{img: img, tx0: 0, ty0: 0, w: w, h: h, data: make([]int32, w*h)}
			if err := td.buildGeometry(); err != nil {
				t.Fatalf("buildGeometry: %v", err)
			}
			multi := -1
			total := 0
			best := 0
			seen := map[*jpcTagTree]bool{}
			for i, lvl := range td.levels {
				if lvl == nil || lvl.bands == nil {
					continue
				}
				total += lvl.numprcs
				if lvl.numprcs > 1 && multi < 0 {
					multi = i
				}
				for _, b := range lvl.bands {
					if b == nil || len(b.prcs) != lvl.numprcs {
						t.Fatalf("%s: level %d declares %d precincts, band carries %d", name, i, lvl.numprcs, len(b.prcs))
					}
					nonEmpty := 0
					for _, prc := range b.prcs {
						if prc == nil || len(prc.cblks) == 0 {
							continue
						}
						nonEmpty++
						if prc.inclTree == nil || prc.numimsbTree == nil {
							t.Fatalf("%s: level %d precinct with %d code blocks has no tag trees", name, i, len(prc.cblks))
						}
						if seen[prc.inclTree] {
							t.Fatalf("%s: two precincts share one inclusion tag tree", name)
						}
						seen[prc.inclTree] = true
					}
					if nonEmpty > best {
						best = nonEmpty
					}
				}
			}
			if multi < 0 {
				t.Fatalf("%s: no level holds more than one precinct (%d levels, %d precincts): the precinct path is not exercised",
					name, len(td.levels), total)
			}
			if best < 1 {
				t.Fatalf("%s: no precinct carries any code block", name)
			}
			if best > seriesBest {
				seriesBest = best
			}
			t.Logf("%s: %dx%d image, %d levels, first multi-precinct level %d, %d precincts declared, max %d coded in one band",
				name, w, h, len(td.levels), multi, total, best)
		})
	}
	if seriesBest < 2 {
		t.Fatalf("no band in the precinct series codes more than one precinct (max %d): precinct-major packet order is untested", seriesBest)
	}
}

// TestJPCRejectsPrecinctExponentTooSmall checks the guard for precinct
// exponents that make the code-block group exponent negative at a finer
// resolution level.
func TestJPCRejectsPrecinctExponentTooSmall(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "testdata", "p7-3-j2k", "p01.j2k"))
	if err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	codOff := -1
	for i := 0; i+2 < len(data); i++ {
		if data[i] == 0xff && data[i+1] == 0x52 {
			codOff = i
			break
		}
	}
	if codOff < 0 {
		t.Fatal("COD marker not found in fixture")
	}
	out := append([]byte(nil), data...)
	// Precinct bytes start at payload index 10; the last one is the finest
	// level. Zeroing it asks for 1x1 precincts, which the reference cannot
	// halve into a code-block group.
	last := codOff + 4 + 15
	out[last] = 0x00
	_, _, _, _, err = jpcDecodeSamples(out)
	var mal *ErrJPCMalformed
	if err == nil || !errors.As(err, &mal) {
		t.Fatalf("err = %v, want ErrJPCMalformed for precinct exponent 0 at a finer level", err)
	}
}
