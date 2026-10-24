// File.ValuesAll: every message of every repo fixture (strict) and optional
// real-data corpus (P8A_CORPUS_DIR, non-strict) must decode to the identical
// float32 bits — and the identical error, where a message fails — under
// concurrent decoding as under the sequential Messages()+Values() walk.

package grib

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type seqOutcome struct {
	vals []float32
	err  error
}

// sequentialDecode walks the file the classic way and records per-message
// outcomes up to (and including) the first failure.
func sequentialDecode(t *testing.T, f *File) []seqOutcome {
	t.Helper()
	var out []seqOutcome
	for m, err := range f.Messages() {
		if err != nil {
			out = append(out, seqOutcome{err: err})
			break
		}
		vals, verr := m.Values()
		out = append(out, seqOutcome{vals: vals, err: verr})
		if verr != nil {
			break // sequential iteration semantics: stop at first failure
		}
	}
	return out
}

func compareOutcomes(t *testing.T, label string, seq []seqOutcome, par [][]float32, perr error) {
	t.Helper()
	// Find the first failing index in the sequential walk.
	firstFail := -1
	for i := range seq {
		if seq[i].err != nil {
			firstFail = i
			break
		}
	}
	if firstFail >= 0 {
		// Parallel must report exactly that error (unwrapped, byte-identical).
		if perr == nil || perr.Error() != seq[firstFail].err.Error() {
			t.Fatalf("%s: parallel error diverges at %d:\n seq: %v\n par: %v", label, firstFail, seq[firstFail].err, perr)
		}
		return
	}
	if perr != nil {
		t.Fatalf("%s: unexpected parallel error: %v", label, perr)
	}
	if len(par) != len(seq) {
		t.Fatalf("%s: message count %d vs sequential %d", label, len(par), len(seq))
	}
	for i := range seq {
		if len(par[i]) != len(seq[i].vals) {
			t.Fatalf("%s msg %d: length %d vs %d", label, i, len(par[i]), len(seq[i].vals))
		}
		for k := range par[i] {
			if math.Float32bits(par[i][k]) != math.Float32bits(seq[i].vals[k]) {
				t.Fatalf("%s msg %d value %d: bit divergence seq=%v (0x%08X) par=%v (0x%08X)",
					label, i, k, seq[i].vals[k], math.Float32bits(seq[i].vals[k]), par[i][k], math.Float32bits(par[i][k]))
			}
		}
	}
}

// parallelDecode runs ValuesAll with the given worker count.
func parallelDecode(t *testing.T, f *File, workers ...int) ([][]float32, error) {
	t.Helper()
	return f.ValuesAll(workers...)
}

// TestValuesAllArgValidation pins the fail-loud contract for malformed
// variadic usage: more than one argument is a programming error.
func TestValuesAllArgValidation(t *testing.T) {
	f, err := Open("testdata/ecc-xcheck/poscorners.grib2")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	if _, err := f.ValuesAll(2, 3); err == nil {
		t.Fatal("ValuesAll(2,3): expected error for multiple arguments, got nil")
	}
	// Default (no args) must equal explicit workers=1.
	a, aerr := f.ValuesAll()
	b, berr := f.ValuesAll(1)
	if (aerr == nil) != (berr == nil) || (aerr != nil && aerr.Error() != berr.Error()) {
		t.Fatalf("default vs workers=1 error divergence: %v / %v", aerr, berr)
	}
	if aerr == nil {
		for i := range a {
			if len(a[i]) != len(b[i]) {
				t.Fatalf("msg %d length divergence default=%d one=%d", i, len(a[i]), len(b[i]))
			}
		}
	}
}

func testParallelParity(t *testing.T, dir string, label string, strict bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if strict {
			if runtime.GOOS == "js" {
				t.Skipf("%s: js/wasm directory support depends on the host: %v", label, err)
			}
			t.Fatalf("%s: read %s: %v", label, dir, err)
		}
		return
	}
	files := 0
	msgs := 0
	vals := int64(0)
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() {
			testParallelParity(t, path, label+"/"+e.Name(), strict)
			continue
		}
		f, oerr := Open(path)
		if oerr != nil {
			continue // non-GRIB artifacts / negative fixtures: not a parity case
		}
		files++
		seq := sequentialDecode(t, f)
		n := 0
		for _, s := range seq {
			if s.err == nil {
				n++
				msgs++
				vals += int64(len(s.vals))
			}
		}
		workers := runtime.GOMAXPROCS(0)
		if workers < 2 {
			workers = 2 // exercise the concurrent path even on a single CPU
		}
		for _, w := range []int{0, 1, workers, n + 3} { // 0/1 = sequential default; clamp-down included
			var par [][]float32
			var perr error
			if w == 0 {
				par, perr = parallelDecode(t, f) // no-arg default path
			} else {
				par, perr = parallelDecode(t, f, w)
			}
			compareOutcomes(t, label+" "+e.Name()+" (workers="+strconv.Itoa(w)+")", seq, par, perr)
		}
		f.Close()
	}
	t.Logf("%s: %d files, %d decoded messages, %d values parity-checked (4 worker configs each)", label, files, msgs, vals)
}

// TestA_P8A_ParallelValuesBitExact is the parity proof for ValuesAll: repo
// fixtures strictly; set P8A_CORPUS_DIR (comma-separated) to additionally sweep
// real-data corpora (the Linux bench host: /root/p8-corpus,/root/p7b).
func TestA_P8A_ParallelValuesBitExact(t *testing.T) {
	testParallelParity(t, "testdata", "repo", true)
	if dirs := os.Getenv("P8A_CORPUS_DIR"); dirs != "" {
		for _, d := range strings.Split(dirs, ",") {
			testParallelParity(t, d, "corpus", false)
		}
	}
}
