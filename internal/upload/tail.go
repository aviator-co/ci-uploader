package upload

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// Tail returns the last n bytes of text, starting at a line, with a note on
// how much was cut.
func Tail(text []byte, n int) []byte {
	return tail(text, n, int64(len(text)))
}

// tail is Tail for text that is itself the end of total bytes.
func tail(text []byte, n int, total int64) []byte {
	if int64(len(text)) >= total && len(text) <= n {
		return text
	}
	cut := text[max(0, len(text)-n):]
	if i := bytes.IndexByte(cut, '\n'); i >= 0 && i < len(cut)-1 {
		cut = cut[i+1:]
	}
	note := fmt.Sprintf("[aviator-ci: kept the last %d of %d bytes]\n", len(cut), total)
	return append([]byte(note), cut...)
}

// ReadTail reads at most the last LogTailBytes of the file at path, and
// returns the file's size.
func ReadTail(path string) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := info.Size()
	if size > LogTailBytes {
		if _, err := f.Seek(size-LogTailBytes, io.SeekStart); err != nil {
			return nil, 0, err
		}
	}
	data, err := io.ReadAll(f)
	return data, size, err
}
