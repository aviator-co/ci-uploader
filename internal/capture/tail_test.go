package capture

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

func TestTailKeepsTheEnd(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 200 {
		limit := 1 + r.IntN(50)
		tail := NewTail(limit)
		var all []byte
		for range r.IntN(20) {
			p := make([]byte, r.IntN(2*limit))
			for i := range p {
				p[i] = byte(r.IntN(256))
			}
			all = append(all, p...)
			if n, err := tail.Write(p); n != len(p) || err != nil {
				t.Fatalf("Write = %d, %v", n, err)
			}
		}
		got, total := tail.Bytes()
		want := all[max(0, len(all)-limit):]
		if !bytes.Equal(got, want) || total != int64(len(all)) {
			t.Fatalf("limit %d: got %d bytes (total %d), want %d (total %d)", limit, len(got), total, len(want), len(all))
		}
	}
}
