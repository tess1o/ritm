package ingest

import (
	"bufio"
	"bytes"
	"io"
	"sync/atomic"
)

// headPeek bounds how far into the stream the DOCTYPE block is searched for.
const headPeek = 1 << 20

// NewCleanReader drops the inline <!DOCTYPE … ]> block found near the top of export.xml and
// removes control characters that are invalid in XML 1.0 (PAR-2). It never buffers more than headPeek.
func NewCleanReader(r io.Reader) io.Reader {
	br := bufio.NewReaderSize(r, headPeek)
	head, _ := br.Peek(headPeek)
	var prefix []byte
	if start := bytes.Index(head, []byte("<!DOCTYPE")); start >= 0 {
		if end := doctypeEnd(head, start); end > 0 {
			prefix = append([]byte(nil), head[:start]...)
			if _, err := br.Discard(end); err != nil {
				return &ctrlFilter{r: io.MultiReader(bytes.NewReader(nil), br)}
			}
		}
	}
	return &ctrlFilter{r: io.MultiReader(bytes.NewReader(prefix), br)}
}

// doctypeEnd returns the offset just past the DOCTYPE declaration starting at start, or -1.
func doctypeEnd(b []byte, start int) int {
	depth := 0
	for i := start + len("<!DOCTYPE"); i < len(b); i++ {
		switch b[i] {
		case '[':
			depth++
		case ']':
			depth--
		case '>':
			if depth <= 0 {
				return i + 1
			}
		}
	}
	return -1
}

type ctrlFilter struct{ r io.Reader }

func (f *ctrlFilter) Read(p []byte) (int, error) {
	for {
		n, err := f.r.Read(p)
		w := 0
		for i := 0; i < n; i++ {
			c := p[i]
			if c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
				continue
			}
			p[w] = c
			w++
		}
		if w > 0 || err != nil || n == 0 {
			return w, err
		}
	}
}

// countingReader tracks consumed bytes for progress reporting (PRG-1).
type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}
