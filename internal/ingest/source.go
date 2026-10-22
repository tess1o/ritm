package ingest

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type source struct {
	r     io.Reader
	total int64 // uncompressed size of export.xml
	zip   *zip.Reader
	dir   string // folder of export.xml inside the zip, with trailing slash
	close func() error
}

// openSource opens export.zip (locating the export.xml entry, PAR-1) or a bare export.xml.
func openSource(path string) (*source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		f.Close()
		return nil, fmt.Errorf("file too small to be an export")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	if !bytes.Equal(magic, []byte("PK\x03\x04")) {
		return &source{r: f, total: fi.Size(), close: f.Close}, nil
	}
	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("open zip: %w", err)
	}
	entry := findExportXML(zr)
	if entry == nil {
		f.Close()
		return nil, errors.New("no export.xml found in zip")
	}
	rc, err := entry.Open()
	if err != nil {
		f.Close()
		return nil, err
	}
	dir := ""
	if i := strings.LastIndex(entry.Name, "/"); i >= 0 {
		dir = entry.Name[:i+1]
	}
	return &source{r: rc, total: int64(entry.UncompressedSize64), zip: zr, dir: dir,
		close: func() error { return errors.Join(rc.Close(), f.Close()) }}, nil
}

func findExportXML(zr *zip.Reader) *zip.File {
	var best *zip.File
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if strings.HasPrefix(name, "__macosx/") || f.FileInfo().IsDir() {
			continue
		}
		if name == "export.xml" || strings.HasSuffix(name, "/export.xml") {
			// prefer the shallowest match
			if best == nil || strings.Count(f.Name, "/") < strings.Count(best.Name, "/") {
				best = f
			}
		}
	}
	return best
}
