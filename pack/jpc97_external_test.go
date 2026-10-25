package pack

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestJPCLossy97External decodes codestreams produced by OpenJPEG (via Pillow)
// with the irreversible 9/7 transform and requires an exact match with
// OpenJPEG's own pixels. The fixtures cover no DWT, three decomposition levels,
// odd dimensions and 16-bit precision; they are documented in
// testdata/jpc97/README.md.
func TestJPCLossy97External(t *testing.T) {
	cases := []struct {
		name string
		bits uint8
	}{
		{"grad_16x12_l1", 8},
		{"grad_16x12_l3", 8},
		{"rand_17x13_l3", 8},
		{"gray16_16x12_l3", 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "testdata", "jpc97", tc.name+".j2k"))
			if err != nil {
				t.Fatalf("read codestream: %v", err)
			}
			txt, err := os.ReadFile(filepath.Join("..", "testdata", "jpc97", tc.name+".txt"))
			if err != nil {
				t.Fatalf("read expected samples: %v", err)
			}
			var want []float32
			for _, s := range strings.Fields(string(txt)) {
				v, err := strconv.ParseUint(s, 10, 32)
				if err != nil {
					t.Fatalf("bad expected sample %q: %v", s, err)
				}
				want = append(want, float32(v))
			}

			sec5 := sec5_40(uint32(len(want)), 0, 0, 0, tc.bits, 0, 1)
			got, err := Unpack(&Context{
				TemplateNumber: 40,
				Fields:         walk540(t, sec5),
				Data:           data,
				NumValues:      len(want),
			})
			if err != nil {
				t.Fatalf("Unpack: %v", err)
			}
			if len(got) != len(want) {
				t.Fatalf("got %d samples, want %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("sample[%d] = %v, want %v", i, got[i], want[i])
				}
			}
		})
	}
}

// TestJPCRejectsUnknownCompressionType keeps the fail-loud contract for the
// compression-type octet: 0 (lossless) and 1 (lossy) are valid, anything else
// is not.
func TestJPCRejectsUnknownCompressionType(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "testdata", "jpc97", "grad_16x12_l1.j2k"))
	if err != nil {
		t.Fatalf("read codestream: %v", err)
	}
	sec5 := sec5_40(192, 0, 0, 0, 8, 0, 2)
	_, err = Unpack(&Context{
		TemplateNumber: 40,
		Fields:         walk540(t, sec5),
		Data:           data,
		NumValues:      192,
	})
	var uns *ErrJPCUnsupported
	if err == nil || !asUnsupported(err, &uns) {
		t.Fatalf("err = %v, want ErrJPCUnsupported", err)
	}
}
