package grib

import (
	"bytes"
	"os"
	"runtime"
	"testing"
)

// FuzzOpen feeds arbitrary bytes to the reader. The parser promises to fail
// loudly instead of panicking on malformed input, and a fuzz target is the
// cheapest way to keep that promise honest. Seeds are two real (tiny) GRIB2
// files from testdata; the mutation corpus is not committed.
func FuzzOpen(f *testing.F) {
	if runtime.GOOS == "js" {
		f.Skip("fuzzing runs on the host toolchain only (js/wasm has no coverage instrumentation)")
	}
	for _, name := range []string{
		"testdata/p6-a-xcheck/p6a_ll_1xn.grib2",
		"testdata/p6-b-xcheck/d3_simple_constant.grib2",
	} {
		if b, err := os.ReadFile(name); err == nil {
			f.Add(b)
		}
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := OpenReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return // malformed input must be an error, not a panic
		}
		defer file.Close()

		n, err := file.NumMessages()
		if err != nil || n == 0 || n > 64 {
			return
		}
		for m, err := range file.Messages() {
			if err != nil {
				return
			}
			// Metadata only: decoding values from a mutated message can ask
			// for an enormous grid, which is a resource question rather than
			// a correctness one.
			_, _ = m.Product()
			_, _ = m.Identification()
			_, _ = m.Grid()
			_, _ = m.DataRep()
			_, _ = m.MarsLabelling()
		}
	})
}
