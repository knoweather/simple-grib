//
// bitsPerValue = 0 in template 5.40. The board ruling is two-branch: an empty
// section 7 means every value is the reference value (the packed sample index is
// zero), while a non-empty section 7 contradicts the declaration and must be
// reported rather than decoded or ignored. The oracle for the first branch is
// eccodes, which returns a constant field equal to referenceValue for these
// messages.

package pack

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/knoweather/simple-grib/internal/octetio"
	"github.com/knoweather/simple-grib/spec"
)

// sec5_40 builds a template 5.40 section 5 image: R at octets 12-15, exponent
// at 16-17, divisor at 18-19, bits per value at 20, original value type at 21,
// compression type at 22 and point code at 23.
func sec5_40(numValues uint32, R float32, E, D int16, bits, origType, compressionType byte) []byte {
	b := make([]byte, 24)
	binary.BigEndian.PutUint32(b[0:4], uint32(len(b)))
	b[4] = 5
	binary.BigEndian.PutUint32(b[5:9], numValues)
	binary.BigEndian.PutUint16(b[9:11], 40)
	binary.BigEndian.PutUint32(b[11:15], math.Float32bits(R))
	binary.BigEndian.PutUint16(b[15:17], uint16(E))
	binary.BigEndian.PutUint16(b[17:19], uint16(D))
	b[19] = bits
	b[20] = origType
	b[21] = compressionType
	b[22] = 0
	return b
}

func walk540(t *testing.T, sec5 []byte) *spec.Walk {
	t.Helper()
	def, ok := spec.Template(5, 40)
	if !ok {
		t.Fatal("spec has no def for template 5.40")
	}
	w, err := spec.NewWalk(def, sec5)
	if err != nil {
		t.Fatalf("NewWalk: %v", err)
	}
	if w.Len() < 7 {
		t.Fatalf("walk len %d, want at least 7 fields", w.Len())
	}
	if uint8(w.At(3).U) != sec5[19] {
		t.Fatalf("walk field 3 = %d, want bitsPerValue %d", w.At(3).U, sec5[19])
	}
	return w
}

// TestJPCBitsZeroEmptyData is the first branch: bitsPerValue = 0 with no data
// section decodes to the reference value everywhere, including a non-zero
// reference value.
func TestJPCBitsZeroEmptyData(t *testing.T) {
	cases := []struct {
		name string
		n    int
		R    float32
		E, D int16
		want float32
	}{
		{"zero reference", 12, 0, 0, 0, 0},
		// 192.70213317871094 is the reference value carried by the real CMA
		// messages that declare bitsPerValue = 0; eccodes reports every value
		// as exactly this number.
		{"cma reference value", 360, 192.70213317871094, 0, 0, 192.70213317871094},
		{"scaled reference", 8, 1.5, 3, 2, 0.015},
		{"negative reference", 5, -4.25, 0, 1, -0.425},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sec5 := sec5_40(uint32(c.n), c.R, c.E, c.D, 0, 0, 0)
			ctx := &Context{
				TemplateNumber: TemplateJPEG2000,
				Fields:         walk540(t, sec5),
				Data:           nil,
				NumValues:      c.n,
			}
			got, err := Unpack(ctx)
			if err != nil {
				t.Fatalf("Unpack: %v", err)
			}
			if len(got) != c.n {
				t.Fatalf("got %d values, want %d", len(got), c.n)
			}
			for k, v := range got {
				if v != c.want {
					t.Fatalf("got[%d] = %v, want %v", k, v, c.want)
				}
			}
		})
	}
}

// TestJPCBitsZeroWithData is the second branch: a data section next to
// bitsPerValue = 0 is inconsistent and must be reported.
func TestJPCBitsZeroWithData(t *testing.T) {
	sec5 := sec5_40(12, 1.0, 0, 0, 0, 0, 0)
	ctx := &Context{
		TemplateNumber: TemplateJPEG2000,
		Fields:         walk540(t, sec5),
		Data:           []byte{0xff, 0x4f, 0x00, 0x0b, 0x00, 0x01},
		NumValues:      12,
	}
	_, err := Unpack(ctx)
	var mal *ErrJPCMalformed
	if err == nil || !errors.As(err, &mal) {
		t.Fatalf("err = %v, want ErrJPCMalformed", err)
	}
}

// TestJPCBitsZeroWithBitmap checks that the constant field is expanded to the
// post-bitmap value count, not to the grid point count.
func TestJPCBitsZeroWithBitmap(t *testing.T) {
	sec5 := sec5_40(3, 7.5, 0, 0, 0, 0, 0)
	// Four grid points, three of them valid.
	bits := []byte{0b0111}
	ctx := &Context{
		TemplateNumber: TemplateJPEG2000,
		Fields:         walk540(t, sec5),
		Data:           nil,
		NumValues:      3,
		Bitmap:         octetio.NewBits(bits),
		GridPoints:     4,
	}
	got, err := Unpack(ctx)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d values, want 3", len(got))
	}
	for k, v := range got {
		if v != 7.5 {
			t.Fatalf("got[%d] = %v, want 7.5", k, v)
		}
	}
}

// TestJPCBitsNonZeroStillDecodes guards the other side of the branch: the same
// fixture that decodes with bitsPerValue = 14 must not take the constant path.
func TestJPCBitsNonZeroStillDecodes(t *testing.T) {
	sec5 := sec5_40(4, 0, 0, 0, 14, 0, 0)
	ctx := &Context{
		TemplateNumber: TemplateJPEG2000,
		Fields:         walk540(t, sec5),
		Data:           nil,
		NumValues:      4,
	}
	_, err := Unpack(ctx)
	if err == nil {
		t.Fatal("expected an error for an empty data section with bitsPerValue = 14")
	}
	var mal *ErrJPCMalformed
	if errors.As(err, &mal) && mal.Reason == "bitsPerValue = 0 with a 0-byte data section" {
		t.Fatalf("bitsPerValue = 14 took the bitsPerValue = 0 branch: %v", err)
	}
}
