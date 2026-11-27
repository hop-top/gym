package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tent is one tar entry to build.
type tent struct {
	name     string
	typ      byte
	body     string
	mode     int64
	linkname string
	// size, when set, declares that many zero bytes instead of body.
	size int64
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// buildTarGz writes a .tar.gz of ents into dir and returns its path.
func buildTarGz(t *testing.T, dir, name string, ents []tent) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range ents {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		size := int64(len(e.body))
		if e.size > 0 {
			size = e.size
		}
		h := &tar.Header{Name: e.name, Typeflag: typ, Mode: mode, Linkname: e.linkname}
		if typ == tar.TypeReg {
			h.Size = size
		}
		if typ == tar.TypeXGlobalHeader {
			h = &tar.Header{Typeflag: typ, Name: "pax_global_header", PAXRecords: map[string]string{"comment": "0123"}}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if e.size > 0 {
				if _, err := io.CopyN(tw, zeroReader{}, e.size); err != nil {
					t.Fatal(err)
				}
			} else if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// zent is one zip entry to build; mode holds unix permission bits plus
// 0o120000 for a symlink.
type zent struct {
	name string
	body string
	mode uint32
}

func fileMode(m uint32) fs.FileMode {
	mode := fs.FileMode(m & 0o777)
	if m&0o170000 == 0o120000 {
		mode |= fs.ModeSymlink
	}
	return mode
}

func buildZip(t *testing.T, dir, name string, ents []zent) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range ents {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(fileMode(e.mode))
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func asArchiveError(err error, target **ArchiveError) bool { return errors.As(err, target) }

// patchZipSizes rewrites the uncompressed-size field of every local file
// header and central-directory entry to n.
func patchZipSizes(b []byte, n uint32) {
	for i := 0; i+30 < len(b); i++ {
		switch string(b[i : i+4]) {
		case "PK\x03\x04":
			binary.LittleEndian.PutUint32(b[i+22:], n)
		case "PK\x01\x02":
			binary.LittleEndian.PutUint32(b[i+24:], n)
		}
	}
}

func skillEnts(prefix string) []tent {
	return []tent{
		{name: prefix + "SKILL.md", body: "---\nname: s\ndescription: d\n---\nbody\n"},
		{name: prefix + "scripts/run.sh", body: "#!/bin/sh\n", mode: 0o755},
		{name: prefix + "ref/a.txt", body: "a\n"},
	}
}

func writeTree(t *testing.T, root string, ents []tent) {
	t.Helper()
	for _, e := range ents {
		p := filepath.Join(root, strings.TrimPrefix(e.name, "./"))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if e.mode&0o100 != 0 {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(e.body), mode); err != nil {
			t.Fatal(err)
		}
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
