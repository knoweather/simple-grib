package octetio

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

func TestReaderLen(t *testing.T) {
	if got := New(nil).Len(); got != 0 {
		t.Errorf("empty Len() = %d, want 0", got)
	}
	if got := New([]byte{1, 2, 3}).Len(); got != 3 {
		t.Errorf("Len() = %d, want 3", got)
	}
}

func TestReaderU8(t *testing.T) {
	r := New([]byte{0x00, 0x7F, 0x80, 0xFF})
	cases := []struct {
		off  int
		want uint8
	}{
		{0, 0x00}, {1, 0x7F}, {2, 0x80}, {3, 0xFF},
	}
	for _, c := range cases {
		got, err := r.U8(c.off)
		if err != nil {
			t.Errorf("U8(%d) error: %v", c.off, err)
			continue
		}
		if got != c.want {
			t.Errorf("U8(%d) = 0x%02X, want 0x%02X", c.off, got, c.want)
		}
	}
	for _, off := range []int{-1, 4, 5, 1000} {
		if _, err := r.U8(off); err == nil {
			t.Errorf("U8(%d): expected error, got nil", off)
		}
	}
	if _, err := New(nil).U8(0); err == nil {
		t.Error("U8 on empty buffer: expected error, got nil")
	}
}

func TestReaderU16(t *testing.T) {
	r := New([]byte{0x12, 0x34, 0x56})
	got, err := r.U16(0)
	if err != nil || got != 0x1234 {
		t.Errorf("U16(0) = 0x%04X, %v; want 0x1234 (big-endian)", got, err)
	}
	got, err = r.U16(1)
	if err != nil || got != 0x3456 {
		t.Errorf("U16(1) = 0x%04X, %v; want 0x3456", got, err)
	}
	for _, off := range []int{-2, -1, 2, 3} {
		if _, err := r.U16(off); err == nil {
			t.Errorf("U16(%d): expected error, got nil", off)
		}
	}
}

func TestReaderU32(t *testing.T) {
	r := New([]byte{0x12, 0x34, 0x56, 0x78})
	got, err := r.U32(0)
	if err != nil || got != 0x12345678 {
		t.Errorf("U32(0) = 0x%08X, %v; want 0x12345678 (big-endian)", got, err)
	}
	for _, off := range []int{-1, 1, 2, 3, 4} {
		if _, err := r.U32(off); err == nil {
			t.Errorf("U32(%d): expected error, got nil", off)
		}
	}
	// max uint32 round-trip
	m := New([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	if v, err := m.U32(0); err != nil || v != math.MaxUint32 {
		t.Errorf("U32 all-ones = %v, %v; want MaxUint32", v, err)
	}
}

func TestReaderF32(t *testing.T) {
	cases := []struct {
		bits uint32
		want float32
	}{
		{0x3FC00000, 1.5},
		{0xBF000000, -0.5},
		{0x00000000, 0},
		{0x7F800000, float32(math.Inf(1))},
		{0xFF800000, float32(math.Inf(-1))},
		{0x7FC00000, float32(math.NaN())},
	}
	for _, c := range cases {
		buf := []byte{
			byte(c.bits >> 24), byte(c.bits >> 16),
			byte(c.bits >> 8), byte(c.bits),
		}
		got, err := New(buf).F32(0)
		if err != nil {
			t.Errorf("F32(bits=0x%08X): %v", c.bits, err)
			continue
		}
		if math.IsNaN(float64(c.want)) {
			if !math.IsNaN(float64(got)) {
				t.Errorf("F32(0x%08X) = %v, want NaN", c.bits, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("F32(0x%08X) = %v, want %v", c.bits, got, c.want)
		}
	}
	if _, err := New([]byte{1, 2, 3}).F32(0); err == nil {
		t.Error("F32 with 3-byte buffer: expected error, got nil")
	}
}

func TestReaderErrRangeFields(t *testing.T) {
	r := New([]byte{1, 2, 3})
	_, err := r.U32(2)
	var er *ErrRange
	if !errors.As(err, &er) {
		t.Fatalf("expected *ErrRange, got %T (%v)", err, err)
	}
	if er.Off != 2 || er.Size != 4 || er.Len != 3 {
		t.Errorf("ErrRange = %+v, want Off=2 Size=4 Len=3", er)
	}
	if _, err := r.U16(-1); !errors.As(err, &er) {
		t.Fatalf("negative offset: expected *ErrRange, got %T", err)
	}
	if er.Off != -1 || er.Size != 2 {
		t.Errorf("ErrRange(neg) = %+v, want Off=-1 Size=2", er)
	}
}

func TestBitsMSBFirst(t *testing.T) {
	b := NewBits([]byte{0b10000001})
	if b.N() != 8 {
		t.Fatalf("N() = %d, want 8", b.N())
	}
	for i := 0; i < 8; i++ {
		want := i == 0 || i == 7
		if got := b.Bit(i); got != want {
			t.Errorf("Bit(%d) = %v, want %v (byte 0b10000001)", i, got, want)
		}
	}

	// two bytes: 0xFF 0x0F → bits 0..7 and 12..15 set, 8..11 clear
	b2 := NewBits([]byte{0xFF, 0x0F})
	for i := 0; i < 16; i++ {
		want := i < 8 || i >= 12
		if got := b2.Bit(i); got != want {
			t.Errorf("Bit(%d) = %v, want %v (bytes FF 0F)", i, got, want)
		}
	}
}

func TestBitsOutOfRange(t *testing.T) {
	b := NewBits([]byte{0xFF})
	for _, i := range []int{-1, -100, 8, 9, 1 << 20} {
		if b.Bit(i) {
			t.Errorf("Bit(%d) = true, want false (out of range)", i)
		}
	}
	e := NewBits(nil)
	if e.N() != 0 || e.Bit(0) {
		t.Error("empty Bits: N()!=0 or Bit(0)=true")
	}
}

func TestBitsPopcount(t *testing.T) {
	b := NewBits([]byte{0b10110000})
	cases := []struct {
		n    int
		want int
	}{
		{8, 3}, // all bits: 1011 → 3
		{4, 3}, // top nibble 1011 → 3
		{2, 1}, // 10 → 1
		{0, 0},
		{-5, 0},  // clamped to 0
		{100, 3}, // clamped to N=8
	}
	for _, c := range cases {
		if got := b.Popcount(c.n); got != c.want {
			t.Errorf("Popcount(%d) = %d, want %d", c.n, got, c.want)
		}
	}

	b2 := NewBits([]byte{0xFF, 0x0F})
	if got := b2.Popcount(12); got != 8 {
		t.Errorf("Popcount(12) = %d, want 8 (bits 8..11 of 0x0F are clear)", got)
	}
	if got := b2.Popcount(16); got != 12 {
		t.Errorf("Popcount(16) = %d, want 12 (4 clear in second byte)", got)
	}
	if got := NewBits(nil).Popcount(5); got != 0 {
		t.Errorf("empty Popcount(5) = %d, want 0", got)
	}
}

// TestNoPanicSweep exercises every offset on buffers of length 0..16 and must
// never panic: out-of-range reads must return errors, not crash.
func TestNoPanicSweep(t *testing.T) {
	for l := 0; l <= 16; l++ {
		buf := make([]byte, l)
		r := New(buf)
		bits := NewBits(buf)
		for off := -3; off <= l+3; off++ {
			_, _ = r.U8(off)
			_, _ = r.U16(off)
			_, _ = r.U32(off)
			_, _ = r.F32(off)
		}
		for i := -3; i <= l*8+3; i++ {
			_ = bits.Bit(i)
		}
		_ = bits.Popcount(-1)
		_ = bits.Popcount(l*8 + 7)
	}
}

// TestNoPanicRandom is a fixed-seed pseudo-fuzz for the same guarantee.
func TestNoPanicRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for n := 0; n < 2000; n++ {
		l := rng.Intn(32)
		buf := make([]byte, l)
		rng.Read(buf)
		r := New(buf)
		bits := NewBits(buf)
		off := rng.Intn(40) - 10
		_, _ = r.U8(off)
		_, _ = r.U16(off)
		_, _ = r.U32(off)
		_, _ = r.F32(off)
		i := rng.Intn(l*8+20) - 10
		_ = bits.Bit(i)
		_ = bits.Popcount(i)
	}
}

func TestBitReader(t *testing.T) {
	// 20-bit stream: values 0xABCDE, 0x12, 0 (padded to 3 bytes = 24 bits)
	data := []byte{0xAB, 0xCD, 0xE0} // 10101011 11001101 11100000 | 00000000 pad
	br := NewBitReader(data)
	if br.Remaining() != 24 {
		t.Fatalf("Remaining = %d, want 24", br.Remaining())
	}
	v, err := br.ReadU(20)
	if err != nil {
		t.Fatal(err)
	}
	if v != 0xABCDE {
		t.Fatalf("ReadU(20) = %#x, want 0xABCDE", v)
	}
	v, err = br.ReadU(4)
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 { // pad bits
		t.Fatalf("ReadU(4) = %#x, want 0", v)
	}
	if br.Remaining() != 0 {
		t.Fatalf("Remaining = %d, want 0", br.Remaining())
	}
	if _, err := br.ReadU(1); err == nil {
		t.Fatal("ReadU past end must fail")
	}
	if _, err := br.ReadU(33); err == nil {
		t.Fatal("ReadU(33) must fail")
	}
	v, err = NewBitReader(nil).ReadU(0)
	if err != nil || v != 0 {
		t.Fatalf("ReadU(0) on empty = (%#x, %v), want (0, nil)", v, err)
	}
}

func TestBitReader32(t *testing.T) {
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01} // 32-bit value + 8 stray bits
	br := NewBitReader(data)
	v, err := br.ReadU(32)
	if err != nil || v != 0xDEADBEEF {
		t.Fatalf("ReadU(32) = (%#x, %v), want (0xDEADBEEF, nil)", v, err)
	}
	if br.Remaining() != 8 {
		t.Fatalf("Remaining = %d, want 8", br.Remaining())
	}
}
