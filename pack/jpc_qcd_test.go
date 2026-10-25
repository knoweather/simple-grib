package pack

import (
	"errors"
	"testing"
)

// TestParseQCXStyles pins the three quantization styles: no quantization
// (one exponent byte per band), scalar derived (one shared step size) and
// scalar explicit (one step size per band). Style 3 is undefined and must fail
// loudly.
func TestParseQCXStyles(t *testing.T) {
	t.Run("no quantization", func(t *testing.T) {
		q, err := parseQCX([]byte{0x00, 0x40})
		if err != nil {
			t.Fatalf("parseQCX: %v", err)
		}
		if q.qntsty != jpcQcxNoqnt || len(q.expns) != 1 || q.expns[0] != 8 {
			t.Fatalf("quant = %+v, want style 0 with expn 8", q)
		}
	})
	t.Run("scalar derived", func(t *testing.T) {
		q, err := parseQCX([]byte{0x41, 0x40, 0x00}) // guard 2, style 1
		if err != nil {
			t.Fatalf("parseQCX: %v", err)
		}
		if q.qntsty != 1 || q.numguard != 2 || len(q.expns) != 1 || q.expns[0] != 8 || q.mants[0] != 0 {
			t.Fatalf("quant = %+v, want style 1 expn 8 mant 0 guard 2", q)
		}
	})
	t.Run("scalar explicit", func(t *testing.T) {
		q, err := parseQCX([]byte{0x02, 0x40, 0x00, 0x7A, 0x00})
		if err != nil {
			t.Fatalf("parseQCX: %v", err)
		}
		if q.qntsty != 2 || len(q.expns) != 2 || q.expns[0] != 8 || q.mants[0] != 0 ||
			q.expns[1] != 15 || q.mants[1] != 0x200 {
			t.Fatalf("quant = %+v, want two steps (8,0) and (15,0x200)", q)
		}
	})
	t.Run("undefined style", func(t *testing.T) {
		_, err := parseQCX([]byte{0x03, 0x40, 0x00})
		var uns *ErrJPCUnsupported
		if err == nil || !errors.As(err, &uns) {
			t.Fatalf("err = %v, want ErrJPCUnsupported", err)
		}
	})
	t.Run("odd explicit payload", func(t *testing.T) {
		_, err := parseQCX([]byte{0x02, 0x40, 0x00, 0x40})
		var mal *ErrJPCMalformed
		if err == nil || !errors.As(err, &mal) {
			t.Fatalf("err = %v, want ErrJPCMalformed", err)
		}
	})
}
