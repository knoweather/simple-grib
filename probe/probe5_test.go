package probe

import (
	"encoding/binary"
	"testing"
)

func TestProbeTrailingBytesDeferred(t *testing.T) {
	msg := assemble(0, [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0), sec7([]byte{1})})
	msg = append(msg, 1, 2, 3, 4, 5)
	binary.BigEndian.PutUint64(msg[8:16], uint64(len(msg)))
	f, err := openBytes(msg)
	t.Logf("Open with 5 trailing bytes -> %v", err)
	if f == nil {
		return
	}
	defer f.Close()
	n, _ := f.NumMessages()
	t.Logf("NumMessages = %d", n)
	m, err := f.Message(0)
	t.Logf("Message(0) -> %v", err)
	if m != nil {
		v, err := m.Values()
		t.Logf("Values -> %v err=%v", v, err)
	}
}
