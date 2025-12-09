package extract

import (
	"errors"
	"strings"
	"testing"

	"sift/internal/core"
)

// bom is the UTF-8 byte order mark some editors write at the start of a file.
var bom = string(rune(0xfeff))

// accented spells "cafe naive" with Latin-1 letters, as valid UTF-8 text.
var accented = "caf" + string(rune(0xe9)) + " na" + string(rune(0xef)) + "ve"

// ref builds a FileRef for a corpus-relative path.
func ref(rel string, size int64) core.FileRef {
	return core.FileRef{Rel: rel, Abs: "/corpus/" + rel, Size: size}
}

func TestExtractKindAndLanguage(t *testing.T) {
	cases := []struct {
		rel      string
		kind     string
		language string
		ext      string
	}{
		{"README.md", KindMarkdown, "", ".md"},
		{"docs/guide.MARKDOWN", KindMarkdown, "", ".markdown"},
		{"notes.txt", KindText, "", ".txt"},
		{"docs/spec.rst", KindText, "", ".rst"},
		{"src/main.go", KindSource, "go", ".go"},
		{"web/app.tsx", KindSource, "typescript", ".tsx"},
		{"deploy/site.yml", KindSource, "yaml", ".yml"},
		{"lib/util.hpp", KindSource, "cpp", ".hpp"},
		{"Makefile", KindOther, "", ""},
		{"archive.tar", KindOther, "", ".tar"},
	}
	for _, tc := range cases {
		t.Run(tc.rel, func(t *testing.T) {
			doc, err := Extract(ref(tc.rel, 8), []byte("alpha beta\n"))
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if doc.Kind != tc.kind {
				t.Errorf("Kind = %q, want %q", doc.Kind, tc.kind)
			}
			if doc.Language != tc.language {
				t.Errorf("Language = %q, want %q", doc.Language, tc.language)
			}
			if doc.Fields["kind"] != tc.kind || doc.Fields["language"] != tc.language {
				t.Errorf("Fields kind/language = %q/%q, want %q/%q",
					doc.Fields["kind"], doc.Fields["language"], tc.kind, tc.language)
			}
			if doc.Fields["ext"] != tc.ext {
				t.Errorf("Fields ext = %q, want %q", doc.Fields["ext"], tc.ext)
			}
		})
	}
}

func TestExtractIdentityFields(t *testing.T) {
	doc, err := Extract(ref("docs/deep/guide.md", 42), []byte("# Guide\n\nbody\n"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if doc.ID != core.DocID("docs/deep/guide.md") {
		t.Errorf("ID = %q, want docs/deep/guide.md", doc.ID)
	}
	if doc.Path != "docs/deep/guide.md" {
		t.Errorf("Path = %q, want docs/deep/guide.md", doc.Path)
	}
	if doc.Fields["dir"] != "docs/deep" {
		t.Errorf("dir = %q, want docs/deep", doc.Fields["dir"])
	}
	if doc.Size != 42 {
		t.Errorf("Size = %d, want 42", doc.Size)
	}
	if len(doc.Fields) != 4 {
		t.Errorf("Fields = %v, want exactly kind, language, dir, ext", doc.Fields)
	}

	root, err := Extract(ref("top.md", 0), []byte("body"))
	if err != nil {
		t.Fatalf("Extract root: %v", err)
	}
	if root.Fields["dir"] != "." {
		t.Errorf("root dir = %q, want .", root.Fields["dir"])
	}
	if root.Size != 4 {
		t.Errorf("Size = %d, want 4 from the data length", root.Size)
	}
}

func TestExtractContentHash(t *testing.T) {
	const wantHash = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	doc, err := Extract(ref("a.txt", 5), []byte("hello"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if doc.ContentHash != wantHash {
		t.Errorf("ContentHash = %q, want %q", doc.ContentHash, wantHash)
	}
	// The hash covers the raw bytes, not the normalized body.
	crlf, err := Extract(ref("a.txt", 6), []byte("hello\r"))
	if err != nil {
		t.Fatalf("Extract crlf: %v", err)
	}
	if crlf.ContentHash == wantHash {
		t.Error("ContentHash ignored the raw bytes")
	}
	if crlf.Body != "hello" {
		t.Errorf("Body = %q, want %q", crlf.Body, "hello")
	}
}

func TestExtractTitle(t *testing.T) {
	cases := []struct {
		name string
		rel  string
		data string
		want string
	}{
		{"markdown heading", "a.md", "\n\n# Getting Started\n\ntext\n", "Getting Started"},
		{"markdown heading after text", "a.md", "badge line\n\n## Deep Dive\n", "Deep Dive"},
		{"markdown closed atx", "a.md", "### Title ###\n", "Title"},
		{"markdown seven hashes is not a heading", "a.md", "####### nope\n", "####### nope"},
		{"markdown hash without space", "a.md", "#nospace\nplain line\n", "#nospace"},
		{"markdown without heading", "a.md", "just a line\nmore\n", "just a line"},
		{"text first non-empty line", "notes.txt", "\n\n  Release Notes  \nbody\n", "Release Notes"},
		{"source comment stripped", "main.go", "// Package main runs it.\npackage main\n", "Package main runs it."},
		{"source block comment marker alone", "a.c", "/*\n * Ring buffer.\n */\n", "Ring buffer."},
		{"source no comment", "a.py", "import os\n", "import os"},
		{"empty body falls back to base name", "docs/empty.md", "\n \n\n", "empty.md"},
		{"whitespace only source", "src/blank.go", "   \n", "blank.go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Extract(ref(tc.rel, int64(len(tc.data))), []byte(tc.data))
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if doc.Title != tc.want {
				t.Fatalf("Title = %q, want %q", doc.Title, tc.want)
			}
		})
	}
}

func TestExtractLongTitleTruncated(t *testing.T) {
	line := strings.Repeat("a", 200)
	doc, err := Extract(ref("a.txt", int64(len(line))), []byte(line))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(doc.Title) != maxTitleRunes {
		t.Fatalf("len(Title) = %d, want %d", len(doc.Title), maxTitleRunes)
	}
}

func TestExtractBodyNormalization(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{"crlf to lf", "a\r\nb\r\n", "a\nb"},
		{"lone cr to lf", "a\rb", "a\nb"},
		{"tabs to spaces", "a\tb\tc", "a b c"},
		{"collapse blank runs", "a\n\n\n\n\nb\n", "a\n\nb"},
		{"trim leading and trailing blanks", "\n\n a \n\n\n", " a"},
		{"trailing spaces dropped", "a   \nb  \n", "a\nb"},
		{"bom stripped", bom + "# Title\n", "# Title"},
		{"empty stays empty", "", ""},
		{"blank lines only", "\n\n\n", ""},
		{"tab indent becomes one space", "func f() {\n\treturn\n}\n", "func f() {\n return\n}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Extract(ref("a.txt", int64(len(tc.data))), []byte(tc.data))
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if doc.Body != tc.want {
				t.Fatalf("Body = %q, want %q", doc.Body, tc.want)
			}
		})
	}
}

func TestExtractDeterministic(t *testing.T) {
	data := []byte("# Title\r\n\r\n\r\nbody\ttext\r\n")
	first, err := Extract(ref("docs/a.md", int64(len(data))), data)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	second, err := Extract(ref("docs/a.md", int64(len(data))), data)
	if err != nil {
		t.Fatalf("Extract again: %v", err)
	}
	if first.Body != second.Body || first.Title != second.Title || first.ContentHash != second.ContentHash {
		t.Fatalf("extraction is not deterministic: %+v vs %+v", first, second)
	}
	if first.Body != "# Title\n\nbody text" {
		t.Errorf("Body = %q", first.Body)
	}
}

func TestExtractErrors(t *testing.T) {
	if _, err := Extract(ref("bin.dat", 4), []byte{'a', 0x00, 'b', 'c'}); !errors.Is(err, ErrBinary) {
		t.Fatalf("binary err = %v, want ErrBinary", err)
	}
	if _, err := Extract(core.FileRef{}, []byte("text")); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("empty path err = %v, want core.ErrNotFound", err)
	}
	doc, err := Extract(ref("bin.dat", 4), []byte{0x00})
	if err == nil {
		t.Fatal("want an error for binary contents")
	}
	if doc.ID != "" || doc.Body != "" {
		t.Errorf("document should be zero on error, got %+v", doc)
	}
	if !strings.Contains(err.Error(), "bin.dat") {
		t.Errorf("error %q does not name the file", err.Error())
	}
}

func TestIsBinary(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"empty", nil, false},
		{"ascii text", []byte("package main\n\nfunc main() {}\n"), false},
		{"utf8 text", []byte(accented + " " + string(rune(0x4e2d)) + "\n"), false},
		{"text with tabs and crlf", []byte("a\tb\r\nc\v\f\n"), false},
		{"nul byte", []byte("abc\x00def"), true},
		{"utf16 le", []byte{'a', 0, 'b', 0}, true},
		{"invalid utf8", []byte{0xff, 0xd8, 0xff, 0xe0, 'J', 'F', 'I', 'F'}, true},
		{"control heavy", []byte{0x01, 0x02, 0x03, 0x04, 'a', 'b', 'c', 'd'}, true},
		{"few controls", []byte("aaaaaaaaaa\x01aaaaaaaaaa"), false},
		{"long text stays text", []byte(strings.Repeat("hello world ", 2000)), false},
		{"multibyte cut at the prefix boundary", truncatedRunes(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsBinary(tc.data); got != tc.want {
				t.Fatalf("IsBinary = %v, want %v", got, tc.want)
			}
		})
	}
}

// truncatedRunes builds text longer than the inspected prefix whose final
// inspected byte falls inside a multi-byte rune.
func truncatedRunes() []byte {
	b := []byte(strings.Repeat("a", binaryPrefix-1))
	b = append(b, []byte(strings.Repeat(string(rune(0xe9)), 100))...)
	return b
}

func TestSupportedExt(t *testing.T) {
	cases := []struct {
		ext  string
		want bool
	}{
		{".md", true},
		{"md", true},
		{".MD", true},
		{".markdown", true},
		{".txt", true},
		{".rst", true},
		{".go", true},
		{"go", true},
		{".YAML", true},
		{".tar", false},
		{".png", false},
		{"", false},
		{".", false},
	}
	for _, tc := range cases {
		t.Run(tc.ext, func(t *testing.T) {
			if got := SupportedExt(tc.ext); got != tc.want {
				t.Fatalf("SupportedExt(%q) = %v, want %v", tc.ext, got, tc.want)
			}
		})
	}
}
