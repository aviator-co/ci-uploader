package capture

import (
	"bytes"
	"testing"
)

func TestTailKeepsTheEnd(t *testing.T) {
	var next byte
	for limit := 1; limit <= 40; limit++ {
		tail := NewTail(limit)
		var all []byte
		for _, size := range []int{0, 1, 3, limit - 1, limit, 7, limit + 1, 2 * limit, 5} {
			p := make([]byte, max(size, 0))
			for i := range p {
				p[i] = next
				next++
			}
			all = append(all, p...)
			if n, err := tail.Write(p); n != len(p) || err != nil {
				t.Fatalf("Write = %d, %v", n, err)
			}
			got, total := tail.Bytes()
			want := all[max(0, len(all)-limit):]
			if !bytes.Equal(got, want) || total != int64(len(all)) {
				t.Fatalf("limit %d: got %v (total %d), want %v (total %d)", limit, got, total, want, len(all))
			}
		}
	}
}
