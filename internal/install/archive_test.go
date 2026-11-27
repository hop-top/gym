package install

import (
	"archive/tar"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stage(t *testing.T, archive string) (Staged, error) {
	t.Helper()
	return StageArchive(context.Background(), archive, nil)
}

func wantArchiveInvalid(t *testing.T, archive, contains string) {
	t.Helper()
	st, err := stage(t, archive)
	if err == nil {
		st.Cleanup()
		t.Fatalf("%s: accepted", filepath.Base(archive))
	}
	var ae *ArchiveError
	if !asArchiveError(err, &ae) || !strings.HasPrefix(err.Error(), "archive-invalid: ") || !strings.Contains(err.Error(), contains) {
		t.Fatalf("%s: err = %v, want archive-invalid containing %q", filepath.Base(archive), err, contains)
	}
}

// D11: hostile archives are rejected before anything is written outside
// the extraction directory.
func TestD11HostileTarGz(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		ents []tent
		want string
	}{
		{"traversal", []tent{{name: "../escaped", body: "x"}}, "path traversal"},
		{"traversal-nested", []tent{{name: "a/../../escaped", body: "x"}}, "path traversal"},
		{"absolute", []tent{{name: "/tmp/escaped", body: "x"}}, "absolute path"},
		{"symlink", []tent{{name: "ln", typ: tar.TypeSymlink, linkname: "/etc/passwd"}}, "symlink entry"},
		{"hardlink", []tent{{name: "a", body: "x"}, {name: "hl", typ: tar.TypeLink, linkname: "a"}}, "hardlink entry"},
		{"char-device", []tent{{name: "dev", typ: tar.TypeChar}}, "device or special entry"},
		{"fifo", []tent{{name: "ff", typ: tar.TypeFifo}}, "device or special entry"},
		{"duplicate", []tent{{name: "a", body: "1"}, {name: "a", body: "2"}}, "duplicate path"},
		{"duplicate-case", []tent{{name: "A", body: "1"}, {name: "a", body: "2"}}, "duplicate path"},
		{"file-then-dir", []tent{{name: "a", body: "1"}, {name: "a/b", body: "2"}}, "lies below the file"},
		{"backslash", []tent{{name: `a\..\b`, body: "1"}}, "invalid characters"},
		{"empty-name", []tent{{name: "", body: "1"}}, "empty name"},
	}
	for _, c := range cases {
		wantArchiveInvalid(t, buildTarGz(t, dir, c.name+".tar.gz", c.ents), c.want)
	}
	if _, err := os.Lstat(filepath.Join(dir, "escaped")); err == nil {
		t.Fatal("a hostile archive wrote outside its extraction directory")
	}
}

func TestD11HostileZip(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		ents []zent
		want string
	}{
		{"traversal", []zent{{name: "../escaped", body: "x"}}, "path traversal"},
		{"absolute", []zent{{name: "/tmp/escaped", body: "x"}}, "absolute path"},
		{"symlink", []zent{{name: "ln", body: "/etc/passwd", mode: 0o120777}}, "symlink entry"},
		{"duplicate", []zent{{name: "a", body: "1"}, {name: "a", body: "2"}}, "duplicate path"},
		{"backslash", []zent{{name: `a\b`, body: "1"}}, "invalid characters"},
	}
	for _, c := range cases {
		wantArchiveInvalid(t, buildZip(t, dir, c.name+".zip", c.ents), c.want)
	}
	if _, err := os.Lstat(filepath.Join(dir, "escaped")); err == nil {
		t.Fatal("wrote outside the extraction directory")
	}
}

func TestD11EntryCountLimit(t *testing.T) {
	dir := t.TempDir()
	ents := make([]tent, 0, MaxArchiveEntries+1)
	for i := 0; i <= MaxArchiveEntries; i++ {
		ents = append(ents, tent{name: fmt.Sprintf("f/%d", i)})
	}
	wantArchiveInvalid(t, buildTarGz(t, dir, "many.tar.gz", ents), "more than 10000 entries")
	st, err := stage(t, buildTarGz(t, dir, "limit.tar.gz", ents[:MaxArchiveEntries]))
	if err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	st.Cleanup()
}

// A highly compressible archive whose declared size passes the limit is
// refused from the header, before a byte is extracted.
func TestD11SizeBombTar(t *testing.T) {
	dir := t.TempDir()
	p := buildTarGz(t, dir, "bomb.tar.gz", []tent{{name: "big", size: MaxArchiveBytes + 1}})
	if fi, _ := os.Stat(p); fi.Size() > 8<<20 {
		t.Fatalf("test archive is %d bytes, not a compressed bomb", fi.Size())
	}
	wantArchiveInvalid(t, p, "expanded size exceeds 256 MiB")
	// refused from the header: nothing was created, not even a partial file
	dest := filepath.Join(t.TempDir(), "dest")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ExtractArchive(p, kindTarGz, dest); err == nil {
		t.Fatal("bomb extracted")
	}
	if ents, _ := os.ReadDir(dest); len(ents) != 0 {
		t.Fatalf("bomb wrote %d entries before it was refused", len(ents))
	}
	p = buildTarGz(t, dir, "bomb2.tar.gz", []tent{{name: "a", size: MaxArchiveBytes/2 + 1}, {name: "b", size: MaxArchiveBytes/2 + 1}})
	wantArchiveInvalid(t, p, "expanded size exceeds 256 MiB")
}

// A zip whose headers understate the size is caught by counting what is
// actually written.
func TestD11SizeBombZipLyingHeader(t *testing.T) {
	old := maxBytes
	maxBytes = 1 << 20
	defer func() { maxBytes = old }()
	dir := t.TempDir()
	p := buildZip(t, dir, "lie.zip", []zent{{name: "big", body: strings.Repeat("\x00", 4<<20)}})
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	patchZipSizes(b, 1)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := stage(t, p)
	if err == nil {
		st.Cleanup()
		t.Fatal("lying zip accepted")
	}
	if !strings.Contains(err.Error(), "archive-invalid") {
		t.Fatalf("err = %v", err)
	}
}

func TestD11SoleTopLevelDirectory(t *testing.T) {
	dir := t.TempDir()
	st, err := stage(t, buildTarGz(t, dir, "one.tar.gz", skillEnts("lint/")))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(st.Root) != "lint" || !fileExists(filepath.Join(st.Root, "SKILL.md")) {
		t.Fatalf("root = %s", st.Root)
	}
	st.Cleanup()

	// "./" entries and a pax global header do not change that.
	st, err = stage(t, buildTarGz(t, dir, "dot.tar.gz", append([]tent{{name: "./", typ: tar.TypeDir}, {name: "pax_global_header", typ: tar.TypeXGlobalHeader}}, skillEnts("./lint/")...)))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(st.Root) != "lint" {
		t.Fatalf("root = %s", st.Root)
	}
	st.Cleanup()

	// The archive root holds the skill directly.
	st, err = stage(t, buildTarGz(t, dir, "flat.tar.gz", skillEnts("")))
	if err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(st.Root, "SKILL.md")) {
		t.Fatalf("root = %s", st.Root)
	}
	st.Cleanup()

	// A root-level dotfile beside the directory: only an exactly-one-entry
	// root counts, so the root is the extraction root.
	st, err = stage(t, buildTarGz(t, dir, "dotfile.tar.gz", append(skillEnts("lint/"), tent{name: ".DS_Store", body: "x"})))
	if err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(st.Root, "lint", "SKILL.md")) || filepath.Base(st.Root) == "lint" {
		t.Fatalf("root = %s", st.Root)
	}
	st.Cleanup()
}

// D12: the digest of an archive is the digest of the same tree as a
// directory, for both formats, with and without a top-level directory.
func TestD12ArchiveDigestEqualsDirDigest(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	ents := skillEnts("")
	writeTree(t, tree, ents)
	want, err := Digest(tree)
	if err != nil {
		t.Fatal(err)
	}
	var flat, nested []zent
	for _, e := range ents {
		flat = append(flat, zent{name: e.name, body: e.body, mode: uint32(e.mode)})
		nested = append(nested, zent{name: "lint/" + e.name, body: e.body, mode: uint32(e.mode)})
	}
	archives := map[string]string{
		"tar.gz flat":       buildTarGz(t, dir, "flat.tar.gz", ents),
		"tgz":               buildTarGz(t, dir, "flat.tgz", ents),
		"tar.gz toplevel":   buildTarGz(t, dir, "top.tar.gz", skillEnts("lint/")),
		"zip toplevel":      buildZip(t, dir, "top.zip", nested),
		"zip flat":          buildZip(t, dir, "flat.zip", flat),
		"tar.gz upper-case": buildTarGz(t, dir, "UP.TAR.GZ", ents),
	}
	for name, a := range archives {
		st, err := stage(t, a)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := Digest(st.Root)
		st.Cleanup()
		if err != nil || got != want {
			t.Errorf("%s: digest %s (%v), want %s", name, got, err, want)
		}
	}
}

func TestIsArchiveSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "dir.zip"), 0o755); err != nil {
		t.Fatal(err)
	}
	for src, want := range map[string]bool{
		"/x/a.tar.gz": true, "a.TGZ": true, "file:///x/a.zip": true, "https://h/a.zip": true,
		"https://h/a.zip?sig=1": true, "https://h/a.git": false, "git@h:o/a.zip": false,
		"ssh://git@h/o/a.zip": false, "/x/a.tar": false, "/x/a": false, filepath.Join(dir, "dir.zip"): false,
	} {
		if got := IsArchiveSource(src); got != want {
			t.Errorf("IsArchiveSource(%q) = %v", src, got)
		}
	}
	if got := ArchiveBaseName("https://h/p/lint-1.0.tar.gz?x=1"); got != "lint-1.0" {
		t.Errorf("name = %q", got)
	}
	if got := ArchiveBaseName("/x/Lint.ZIP"); got != "Lint" {
		t.Errorf("name = %q", got)
	}
}

func TestArchiveNotAnArchive(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.tar.gz")
	if err := os.WriteFile(p, []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantArchiveInvalid(t, p, "not a gzip")
	p = filepath.Join(dir, "x.zip")
	if err := os.WriteFile(p, []byte("not zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantArchiveInvalid(t, p, "not a zip")
}

func TestStageSourceArchiveWithRefIsRefused(t *testing.T) {
	_, err := StageSource(context.Background(), "/x/a.tgz", "v1")
	if err == nil || !strings.Contains(err.Error(), "no #ref") {
		t.Fatalf("err = %v", err)
	}
}
