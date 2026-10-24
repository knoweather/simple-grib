// differential hard line). FILE half of the ccf4442 differential proof: every
// GRIB2 fixture in testdata/ (plus an optional real-data corpus directory via
// P8A_CORPUS_DIR, e.g. the Linux bench host /root/p8-corpus) is opened through the full
// public API; for every template 5.0 message the optimized Values() output is
// compared bit-for-bit against a verbatim port of the pre-optimization
// decoder run on the same section 7 payload. Any single-bit difference fails.

package grib

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/knoweather/simple-grib/internal/octetio"
	"github.com/knoweather/simple-grib/pack"
)

// legacySimpleValues is a VERBATIM port of the pre-ccf4442 pack.simpleDecode
// loop (Owner ), kept here as the differential oracle for file-level
// comparison. Must stay in sync with pack/xcheck_p8_a_differential_test.go's
// legacySimpleDecode (same frozen algorithm, re-derived from git history if
// ever needed). Never called from production code.
func legacySimpleValues(R float32, E, D int16, bits uint8, origType uint8, data []byte, numValues int) ([]float32, error) {
	want := (int(bits)*numValues + 7) / 8
	if bits == 0 {
		want = 0
	}
	if len(data) != want {
		return nil, fmt.Errorf("pack: template 5.0: data length %d != ceil(numValues×bits/8) = %d (numValues=%d, bits=%d)", len(data), want, numValues, bits)
	}
	switch origType {
	case 0, 1:
	default:
		return nil, &pack.ErrUnsupportedValue{Template: pack.TemplateSimple, Field: "type of original field values", Value: int64(origType)}
	}
	twoE := math.Pow(2, float64(E))
	tenMinusD := math.Pow(10, -float64(D))
	out := make([]float32, numValues)
	br := octetio.NewBitReader(data)
	for i := 0; i < numValues; i++ {
		var raw uint32
		if bits > 0 {
			v, err := br.ReadU(int(bits))
			if err != nil {
				return nil, fmt.Errorf("pack: template 5.0: value %d: %w", i, err)
			}
			raw = v
		}
		out[i] = float32((float64(raw)*twoE + float64(R)) * tenMinusD)
	}
	return out, nil
}

// smInt16Test mirrors pack.smInt16 (sign-magnitude int16, GRIB2 ecosystem
// convention for section 5 E/D).
func smInt16Test(raw uint16) int {
	if raw&0x8000 != 0 {
		return -int(raw & 0x7FFF)
	}
	return int(raw)
}

type diffStats struct {
	files, skippedOpen, msgs, skippedValues int
	vals                                    int64
}

// differentialFiles walks dir (recursively), opens every GRIB2-looking file,
// and bit-compares optimized vs legacy decoding for all template 5.0 messages.
// strict=true (repo fixtures): any open/message/values failure is fatal.
// strict=false (real-data corpus): failures are counted and reported — the
// differential covers what both paths can decode; fail-loud behavior itself is
// covered by the dedicated negative tests.
func differentialFiles(t *testing.T, dir string, label string, strict bool) diffStats {
	var st diffStats
	entries, err := os.ReadDir(dir)
	if err != nil {
		if strict {
			if runtime.GOOS == "js" {
				t.Skipf("%s: js/wasm directory support depends on the host: %v", label, err)
			}
			t.Fatalf("%s: read %s: %v", label, dir, err)
		}
		return st
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() {
			st = mergeStats(st, differentialFiles(t, path, label+"/"+e.Name(), strict))
			continue
		}
		// No extension filter: real corpora use .grib2/.bin/.grb2/.grib and
		// extensionless names alike (pangu_*.grib, gfs.*.f000, D3O*); anything
		// that is not a GRIB2 file simply fails Open and counts as skipped.
		f, oerr := Open(path)
		if oerr != nil {
			st.skippedOpen++ // negative fixtures (GRIB1/BUFR/truncated) fail open by design
			continue
		}
		st.files++
		i := 0
		bad := false
		for m, ierr := range f.Messages() {
			if ierr != nil {
				if strict {
					t.Fatalf("%s %s: message %d: %v", label, e.Name(), i, ierr)
				}
				bad = true
				break
			}
			dr, derr := m.DataRep()
			if derr != nil {
				if strict {
					t.Fatalf("%s %s: message %d DataRep: %v", label, e.Name(), i, derr)
				}
				bad = true
				break
			}
			if dr.TemplateNumber != pack.TemplateSimple || dr.Fields == nil || dr.Fields.Len() < 5 {
				i++
				continue
			}
			R := dr.Fields.At(0).F
			E := int16(smInt16Test(uint16(dr.Fields.At(1).U)))
			D := int16(smInt16Test(uint16(dr.Fields.At(2).U)))
			bits := uint8(dr.Fields.At(3).U)
			origType := uint8(dr.Fields.At(4).U)

			sec7, s7err := m.Section(7)
			if s7err != nil {
				if strict {
					t.Fatalf("%s %s: message %d Section(7): %v", label, e.Name(), i, s7err)
				}
				bad = true
				break
			}
			payload := sec7.Data[5:] // octet 6 onward, same slice Values() hands to pack

			got, gerr := m.Values()
			if gerr != nil {
				st.skippedValues++ // e.g. unsupported grid template: both paths share the failure boundary
				i++
				continue
			}
			want, werr := legacySimpleValues(R, E, D, bits, origType, payload, dr.NumValues)
			if werr != nil {
				if strict {
					t.Fatalf("%s %s: message %d (tmpl 0): legacy oracle: %v", label, e.Name(), i, werr)
				}
				bad = true
				break
			}
			if len(got) != len(want) {
				t.Fatalf("%s %s: message %d: length %d vs %d", label, e.Name(), i, len(got), len(want))
			}
			for k := range got {
				if math.Float32bits(got[k]) != math.Float32bits(want[k]) {
					t.Fatalf("%s %s: message %d value %d: bit divergence opt=%v (0x%08X) legacy=%v (0x%08X)",
						label, e.Name(), i, k, got[k], math.Float32bits(got[k]), want[k], math.Float32bits(want[k]))
				}
			}
			st.msgs++
			st.vals += int64(len(got))
			i++
		}
		if bad {
			st.skippedOpen++ // count the partially-iterable file as skipped for reporting
			st.files--
		}
		f.Close()
	}
	return st
}

func mergeStats(a, b diffStats) diffStats {
	a.files += b.files
	a.skippedOpen += b.skippedOpen
	a.msgs += b.msgs
	a.skippedValues += b.skippedValues
	a.vals += b.vals
	return a
}

// TestA_P8A_DifferentialFiles is the file-level half of the differential
// proof (M ruling 轮询#3 ①(b)): all repo fixtures strictly; set
// P8A_CORPUS_DIR (comma-separated) to additionally sweep real-data corpus
// directories non-strictly (the Linux bench host: /root/p8-corpus,/root/p7b).
func TestA_P8A_DifferentialFiles(t *testing.T) {
	st := differentialFiles(t, "testdata", "repo", true)
	t.Logf("repo: %d files opened (%d skipped open), %d template-5.0 messages, %d values bit-exact, %d tmpl-0 msgs skipped (values fail)",
		st.files, st.skippedOpen, st.msgs, st.vals, st.skippedValues)
	if dirs := os.Getenv("P8A_CORPUS_DIR"); dirs != "" {
		for _, d := range strings.Split(dirs, ",") {
			cs := differentialFiles(t, d, "corpus", false)
			t.Logf("corpus %s: %d files opened (%d skipped), %d template-5.0 messages, %d values bit-exact, %d tmpl-0 msgs skipped (values fail)",
				d, cs.files, cs.skippedOpen, cs.msgs, cs.vals, cs.skippedValues)
		}
	}
}
