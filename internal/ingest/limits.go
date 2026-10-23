package ingest

import (
	"fmt"
	"io"
)

// Limits on what one export may make the importer hold in memory. Real exports stay far below
// them; they stop a crafted file from exhausting the server's RAM.
const (
	maxTokenBytes  = 16 << 20  // one tag with its attributes, text node, comment or directive
	maxChildren    = 10_000    // metadata entries, events or statistics kept per element
	maxRouteFiles  = 64        // GPX files referenced by one workout
	maxGPXBytes    = 256 << 20 // uncompressed size of one GPX file
	maxRoutePoints = 2_000_000 // points stored for one workout (23 days at one per second)
)

var errTokenTooLarge = fmt.Errorf("an element, text or comment is larger than %d MB", maxTokenBytes>>20)

// tokenGuard fails once the decoder reads more than maxTokenBytes past the end of the last
// complete token (give or take the decoder's read buffer): encoding/xml buffers a whole token in
// memory, whatever its size. The parser sets mark (Decoder.InputOffset) after every token.
type tokenGuard struct {
	r    io.Reader
	read int64 // bytes handed to the decoder
	mark int64
}

func (g *tokenGuard) Read(b []byte) (int, error) {
	if g.read-g.mark > maxTokenBytes {
		return 0, errTokenTooLarge
	}
	n, err := g.r.Read(b)
	g.read += int64(n)
	return n, err
}
