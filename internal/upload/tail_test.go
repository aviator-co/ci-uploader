package upload

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailStartsAtALine(t *testing.T) {
	got := string(Tail([]byte("first line\nsecond line\nthird\n"), 15))
	want := "[aviator-ci: kept the last 6 of 29 bytes]\nthird\n"
	if got != want {
		t.Errorf("Tail = %q, want %q", got, want)
	}
	if got := string(Tail([]byte("short\n"), 15)); got != "short\n" {
		t.Errorf("short text changed: %q", got)
	}
}

func TestReadTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.log")
	line := strings.Repeat("x", 99) + "\n"
	body := strings.Repeat(line, LogTailBytes/100+50) + "the failure\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	data, size, err := ReadTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(body)) || len(data) != LogTailBytes {
		t.Fatalf("read %d bytes of %d", len(data), size)
	}
	got := tail(data, LogTailBytes, size)
	if !bytes.HasSuffix(got, []byte("the failure\n")) {
		t.Error("lost the end of the log")
	}
	if !bytes.HasPrefix(got, []byte("[aviator-ci: kept the last ")) ||
		!bytes.Contains(got[:80], []byte(" of "+itoa(int64(len(body)))+" bytes]")) {
		t.Errorf("note = %q", got[:80])
	}
	if len(got) > LogTailBytes+100 {
		t.Errorf("read %d bytes", len(got))
	}
}
