// Package config owns the corpus configuration: the defaults every command
// starts from, the rules that decide whether a configuration is usable, and
// the canonical hash recorded in a published manifest.
//
// A corpus is configured by an optional .sift.json file in its root. Only
// the keys present in that file override the defaults, so a configuration
// stays valid as new settings are added. The hash deliberately ignores the
// corpus location: the same corpus checked out at two different paths
// produces the same hash, and therefore the same manifest bytes.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"sift/internal/core"
)

// FileName is the per-corpus configuration file, read from the corpus root.
const FileName = ".sift.json"

// Defaults applied when a corpus does not configure a setting.
const (
	// DefaultMaxFileBytes skips source files larger than one mebibyte.
	DefaultMaxFileBytes int64 = 1 << 20
	// DefaultMinTermLength drops one-character terms.
	DefaultMinTermLength = 2
	// DefaultSegmentDocs caps how many documents one segment holds.
	DefaultSegmentDocs = 256
)

// hashVersion tags the canonical hash encoding. Changing the encoding must
// change this tag so old and new hashes can never collide.
const hashVersion = "sift-config/1"

// Default returns the configuration used for a corpus that has no
// .sift.json. Include and Exclude are empty, so every readable file under
// the root is a candidate, and Stopwords is empty, which leaves the token
// package free to apply its own default list.
func Default(root string) core.Config {
	return core.Config{
		Root:          root,
		OutputDir:     core.DefaultOutputDir,
		Include:       nil,
		Exclude:       nil,
		MaxFileBytes:  DefaultMaxFileBytes,
		Stopwords:     nil,
		MinTermLength: DefaultMinTermLength,
		SegmentDocs:   DefaultSegmentDocs,
	}
}

// Load reads <root>/.sift.json when it exists and applies it over Default,
// then validates the result. A missing file is not an error and yields the
// defaults. Root always comes from the argument and can not be set from the
// file. Key names are matched ignoring case, underscores and dashes, so
// "output_dir", "outputDir" and "OutputDir" all name the same setting; an
// unrecognized key, a value of the wrong type or a setting that fails
// validation returns a *core.ConfigError.
func Load(root string) (core.Config, error) {
	cfg := Default(root)
	file := filepath.Join(root, FileName)
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return core.Config{}, fmt.Errorf("config: read %s: %w", file, err)
	}
	if err := apply(&cfg, data); err != nil {
		return core.Config{}, err
	}
	cfg.Root = root
	if err := Validate(cfg); err != nil {
		return core.Config{}, err
	}
	return cfg, nil
}

// Validate reports the first problem with cfg as a *core.ConfigError, naming
// the offending field. It accepts any Root, including an empty one, because a
// configuration is often validated before a corpus is resolved.
func Validate(cfg core.Config) error {
	if err := validateOutputDir(cfg.OutputDir); err != nil {
		return err
	}
	if cfg.MinTermLength < 1 {
		return &core.ConfigError{Field: "MinTermLength", Reason: "must be at least 1"}
	}
	if cfg.SegmentDocs < 1 {
		return &core.ConfigError{Field: "SegmentDocs", Reason: "must be at least 1"}
	}
	if cfg.MaxFileBytes < 0 {
		return &core.ConfigError{Field: "MaxFileBytes", Reason: "must not be negative"}
	}
	if err := validatePatterns("Include", cfg.Include); err != nil {
		return err
	}
	return validatePatterns("Exclude", cfg.Exclude)
}

// Hash returns the lower-case hex SHA-256 of the canonical form of cfg. The
// encoding sorts Include, Exclude and Stopwords and length-prefixes every
// value, so the hash depends on the settings alone: reordering a list or
// moving the corpus to another path leaves it unchanged, while any change to
// a value changes it.
func Hash(cfg core.Config) string {
	h := sha256.New()
	hashField(h, "version", hashVersion)
	hashField(h, "output_dir", canonicalDir(cfg.OutputDir))
	hashList(h, "include", cfg.Include)
	hashList(h, "exclude", cfg.Exclude)
	hashField(h, "max_file_bytes", strconv.FormatInt(cfg.MaxFileBytes, 10))
	hashList(h, "stopwords", cfg.Stopwords)
	hashField(h, "min_term_length", strconv.Itoa(cfg.MinTermLength))
	hashField(h, "segment_docs", strconv.Itoa(cfg.SegmentDocs))
	return hex.EncodeToString(h.Sum(nil))
}

// OutputPath returns the directory generations are published to.
func OutputPath(cfg core.Config) string {
	return filepath.Join(cfg.Root, cfg.OutputDir)
}

// apply overlays the settings found in data onto cfg. Absent keys and JSON
// nulls keep the value already in cfg.
func apply(cfg *core.Config, data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return &core.ConfigError{Field: FileName, Reason: "invalid JSON: " + err.Error()}
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	// Sorted so a file with several problems always reports the same one.
	sort.Strings(keys)

	for _, key := range keys {
		var err error
		switch normalizeKey(key) {
		case "outputdir":
			err = decodeString(key, raw[key], &cfg.OutputDir)
		case "include":
			err = decodeStrings(key, raw[key], &cfg.Include)
		case "exclude":
			err = decodeStrings(key, raw[key], &cfg.Exclude)
		case "maxfilebytes":
			err = decodeInt64(key, raw[key], &cfg.MaxFileBytes)
		case "stopwords":
			err = decodeStrings(key, raw[key], &cfg.Stopwords)
		case "mintermlength":
			err = decodeInt(key, raw[key], &cfg.MinTermLength)
		case "segmentdocs":
			err = decodeInt(key, raw[key], &cfg.SegmentDocs)
		default:
			err = &core.ConfigError{Field: key, Reason: "unknown setting"}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// normalizeKey folds a configuration key to its comparable form so that
// snake_case, kebab-case and camelCase spellings all agree.
func normalizeKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		switch r {
		case '_', '-', ' ':
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isNull reports whether raw is the JSON literal null, which keeps the default.
func isNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// decodeString reads a JSON string setting.
func decodeString(key string, raw json.RawMessage, dst *string) error {
	if isNull(raw) {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return &core.ConfigError{Field: key, Reason: "expected a string"}
	}
	return nil
}

// decodeStrings reads a JSON list-of-strings setting.
func decodeStrings(key string, raw json.RawMessage, dst *[]string) error {
	if isNull(raw) {
		return nil
	}
	var v []string
	if err := json.Unmarshal(raw, &v); err != nil {
		return &core.ConfigError{Field: key, Reason: "expected a list of strings"}
	}
	*dst = v
	return nil
}

// decodeInt reads a whole-number setting that must fit in an int.
func decodeInt(key string, raw json.RawMessage, dst *int) error {
	if isNull(raw) {
		return nil
	}
	var v int64
	if err := decodeInt64(key, raw, &v); err != nil {
		return err
	}
	if v < int64(minInt) || v > int64(maxInt) {
		return &core.ConfigError{Field: key, Reason: "out of range"}
	}
	*dst = int(v)
	return nil
}

// decodeInt64 reads a whole-number setting.
func decodeInt64(key string, raw json.RawMessage, dst *int64) error {
	if isNull(raw) {
		return nil
	}
	var v int64
	if err := json.Unmarshal(raw, &v); err != nil {
		return &core.ConfigError{Field: key, Reason: "expected a whole number"}
	}
	*dst = v
	return nil
}

const (
	maxInt = int(^uint(0) >> 1)
	minInt = -maxInt - 1
)

// validateOutputDir rejects output directories that are empty, absolute or
// outside the corpus root, on any platform.
func validateOutputDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return &core.ConfigError{Field: "OutputDir", Reason: "must not be empty"}
	}
	if isAbsolute(dir) {
		return &core.ConfigError{Field: "OutputDir", Reason: "must be relative to the corpus root"}
	}
	clean := canonicalDir(dir)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return &core.ConfigError{Field: "OutputDir", Reason: "must not escape the corpus root"}
	}
	if clean == "." {
		return &core.ConfigError{Field: "OutputDir", Reason: "must name a directory below the corpus root"}
	}
	return nil
}

// isAbsolute reports whether p is absolute on either Windows or unix, so a
// configuration rejected on one platform is rejected on the other too.
func isAbsolute(p string) bool {
	if filepath.IsAbs(p) {
		return true
	}
	if strings.HasPrefix(filepath.ToSlash(p), "/") {
		return true
	}
	// A leading drive letter, for example "c:out".
	if len(p) >= 2 && p[1] == ':' {
		c := p[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return false
}

// validatePatterns checks that every glob compiles under path.Match, the
// matcher the scanner applies to slash paths.
func validatePatterns(field string, patterns []string) error {
	for i, p := range patterns {
		name := fmt.Sprintf("%s[%d]", field, i)
		if p == "" {
			return &core.ConfigError{Field: name, Reason: "must not be empty"}
		}
		if _, err := path.Match(p, ""); err != nil {
			return &core.ConfigError{Field: name, Reason: fmt.Sprintf("invalid pattern %q", p)}
		}
	}
	return nil
}

// canonicalDir normalizes a relative directory to its slash form.
func canonicalDir(dir string) string {
	if dir == "" {
		return ""
	}
	return path.Clean(filepath.ToSlash(dir))
}

// hashField writes one length-prefixed key and value, so no combination of
// values can be confused with another.
func hashField(w io.Writer, key, value string) {
	fmt.Fprintf(w, "%s=%d:%s\n", key, len(value), value)
}

// hashList writes a sorted, length-prefixed list of values.
func hashList(w io.Writer, key string, values []string) {
	sorted := make([]string, len(values))
	copy(sorted, values)
	sort.Strings(sorted)
	fmt.Fprintf(w, "%s=%d\n", key, len(sorted))
	for _, v := range sorted {
		fmt.Fprintf(w, "-%d:%s\n", len(v), v)
	}
}
