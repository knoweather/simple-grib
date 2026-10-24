package pack

import (
	"os"
	"runtime"
	"testing"

	"github.com/knoweather/simple-grib/spec"
)

// FuzzPNG feeds arbitrary bytes to the 5.41 decoder. The parser promises to
// fail loudly instead of panicking on malformed input; this keeps that promise
// honest for the PNG path (IHDR, zlib stream, filters, sub-byte packing).
func FuzzPNG(f *testing.F) {
	if runtime.GOOS == "js" {
		f.Skip("fuzzing runs on the host toolchain only")
	}
	if b, err := os.ReadFile("../testdata/png-xcheck/depth8.png"); err == nil {
		f.Add(b)
	}
	f.Add([]byte("\x89PNG\r\n\x1a\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		def, ok := spec.Template(5, TemplatePNG)
		if !ok {
			return
		}
		sec5 := pngSec5(t, 35, 0, 0, 0, 8, 0)
		w, err := spec.NewWalk(def, sec5)
		if err != nil {
			return
		}
		_, _ = Unpack(&Context{
			TemplateNumber: TemplatePNG,
			Fields:         w,
			Data:           data,
			NumValues:      35,
		})
	})
}
