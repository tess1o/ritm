package ingest

import "os"

func createFile(p string) (*os.File, error) { return os.Create(p) }

func writeString(p, s string) error { return os.WriteFile(p, []byte(s), 0o644) }
