// Package extract turns a scanned file into the indexable document form.
//
// Extraction classifies a file by extension, derives a title, normalizes the
// body text and records the fields the rest of Sift filters and facets on.
// It is pure: the same FileRef and bytes always produce the same Document, so
// two runs over an unchanged corpus index identical content.
package extract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"sift/internal/core"
)

// Document kinds reported in Document.Kind and in the "kind" field.
const (
	// KindMarkdown is a Markdown document.
	KindMarkdown = "markdown"
	// KindText is a plain text document.
	KindText = "text"
	// KindSource is a source or structured data file with a known language.
	KindSource = "source"
	// KindOther is a text file of an unrecognized type.
	KindOther = "other"
)

// ErrBinary reports contents that are not text and therefore not indexable.
// Extract wraps it so a caller can skip such a file with
// errors.Is(err, extract.ErrBinary) instead of aborting the run.
var ErrBinary = errors.New("sift: binary content")

// maxTitleRunes caps a derived title so one very long line cannot dominate a
// report.
const maxTitleRunes = 120

// binaryPrefix is the number of leading bytes IsBinary inspects.
const binaryPrefix = 8000

// commentMarkers are stripped from the front of a source line used as a title.
const commentMarkers = "/#*-;%"

// extKinds maps an extension to a non-source document kind.
var extKinds = map[string]string{
	".md":       KindMarkdown,
	".markdown": KindMarkdown,
	".txt":      KindText,
	".rst":      KindText,
}

// extLanguages maps an extension to the language of a source document.
var extLanguages = map[string]string{
	".bash":  "shell",
	".c":     "c",
	".cc":    "cpp",
	".cpp":   "cpp",
	".cs":    "csharp",
	".css":   "css",
	".ex":    "elixir",
	".exs":   "elixir",
	".go":    "go",
	".h":     "c",
	".hpp":   "cpp",
	".hs":    "haskell",
	".html":  "html",
	".java":  "java",
	".js":    "javascript",
	".json":  "json",
	".jsx":   "javascript",
	".kt":    "kotlin",
	".lua":   "lua",
	".m":     "objective-c",
	".php":   "php",
	".pl":    "perl",
	".ps1":   "powershell",
	".py":    "python",
	".r":     "r",
	".rb":    "ruby",
	".rs":    "rust",
	".scala": "scala",
	".sh":    "shell",
	".sql":   "sql",
	".swift": "swift",
	".toml":  "toml",
	".ts":    "typescript",
	".tsx":   "typescript",
	".xml":   "xml",
	".yaml":  "yaml",
	".yml":   "yaml",
}

// Extract builds the document for one scanned file from its raw contents.
//
// Kind and Language come from the extension, Title from the first Markdown
// heading, else the first non-empty line, else the base name. Body is the
// normalized text: CRLF and CR become LF, tabs become single spaces, trailing
// spaces and leading and trailing blank lines are dropped, and runs of blank
// lines collapse to one. Fields carries "kind", "language", "dir" and "ext",
// and ContentHash is the hex SHA-256 of data exactly as passed in.
//
// Binary contents return an error wrapping ErrBinary and an empty path returns
// one wrapping core.ErrNotFound; both leave the Document zero.
func Extract(f core.FileRef, data []byte) (core.Document, error) {
	rel := strings.TrimPrefix(path.Clean(filepath.ToSlash(f.Rel)), "./")
	if f.Rel == "" || rel == "." || rel == "/" {
		return core.Document{}, fmt.Errorf("extract: empty document path: %w", core.ErrNotFound)
	}
	if IsBinary(data) {
		return core.Document{}, fmt.Errorf("extract: %s: %w", rel, ErrBinary)
	}
	ext := strings.ToLower(path.Ext(rel))
	kind := kindFor(ext)
	language := ""
	if kind == KindSource {
		language = extLanguages[ext]
	}
	body := normalize(string(data))
	size := f.Size
	if size <= 0 {
		size = int64(len(data))
	}
	sum := sha256.Sum256(data)
	return core.Document{
		ID:       core.DocID(rel),
		Path:     rel,
		Title:    titleFor(kind, body, path.Base(rel)),
		Kind:     kind,
		Language: language,
		Body:     body,
		Fields: map[string]string{
			"kind":     kind,
			"language": language,
			"dir":      path.Dir(rel),
			"ext":      ext,
		},
		Size:        size,
		ContentHash: hex.EncodeToString(sum[:]),
	}, nil
}

// SupportedExt reports whether the extension names a document kind extraction
// recognizes. The leading dot is optional and case is ignored.
func SupportedExt(ext string) bool {
	return kindFor(normalizeExt(ext)) != KindOther
}

// IsBinary reports whether data looks like something other than text.
//
// Contents are binary when they hold a NUL byte, are not valid UTF-8, or are
// more than 30 percent control characters. Only the first 8000 bytes are
// inspected, and empty contents are text.
func IsBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	p := data
	truncated := false
	if len(p) > binaryPrefix {
		p = p[:binaryPrefix]
		truncated = true
	}
	if bytes.IndexByte(p, 0) >= 0 {
		return true
	}
	if truncated {
		p = trimPartialRune(p)
	}
	if !utf8.Valid(p) {
		return true
	}
	control := 0
	for _, b := range p {
		switch {
		case b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v':
			// Ordinary text whitespace.
		case b < 0x20 || b == 0x7f:
			control++
		}
	}
	return control*100 > len(p)*30
}

// kindFor maps a normalized extension to a document kind.
func kindFor(ext string) string {
	if kind, ok := extKinds[ext]; ok {
		return kind
	}
	if _, ok := extLanguages[ext]; ok {
		return KindSource
	}
	return KindOther
}

// normalizeExt lower-cases an extension and gives it a leading dot.
func normalizeExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "" || strings.HasPrefix(ext, ".") {
		return ext
	}
	return "." + ext
}

// normalize rewrites raw file text into the indexable body form.
func normalize(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", " ")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " ")
		if line == "" {
			if blank || len(out) == 0 {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// titleFor derives the document title from its normalized body.
func titleFor(kind, body, base string) string {
	if kind == KindMarkdown {
		if heading := firstHeading(body); heading != "" {
			return heading
		}
	}
	for _, line := range strings.Split(body, "\n") {
		candidate := strings.TrimSpace(line)
		if kind == KindSource {
			candidate = strings.TrimSpace(strings.TrimLeft(candidate, commentMarkers))
		}
		if candidate != "" {
			return truncateTitle(candidate)
		}
	}
	return base
}

// firstHeading returns the text of the first ATX Markdown heading, or "".
func firstHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimLeft(line, " ")
		level := 0
		for level < len(line) && line[level] == '#' {
			level++
		}
		if level == 0 || level > 6 {
			continue
		}
		rest := line[level:]
		if rest != "" && !strings.HasPrefix(rest, " ") {
			continue
		}
		rest = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(rest), "#"))
		if rest != "" {
			return truncateTitle(rest)
		}
	}
	return ""
}

// truncateTitle caps a title at maxTitleRunes runes.
func truncateTitle(s string) string {
	if utf8.RuneCountInString(s) <= maxTitleRunes {
		return s
	}
	count := 0
	for i := range s {
		if count == maxTitleRunes {
			return strings.TrimRight(s[:i], " ")
		}
		count++
	}
	return s
}

// trimPartialRune drops the incomplete UTF-8 sequence a truncated prefix may
// end with, so cutting at 8000 bytes never makes text look binary.
func trimPartialRune(p []byte) []byte {
	for i := 0; i < utf8.UTFMax-1 && len(p) > 0; i++ {
		if utf8.Valid(p) {
			break
		}
		r, size := utf8.DecodeLastRune(p)
		if r != utf8.RuneError || size != 1 {
			break
		}
		p = p[:len(p)-1]
	}
	return p
}
