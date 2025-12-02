// Package store is the file-system foundation every other Sift package
// builds on. Nothing else writes to disk directly: segments, manifests and
// caches are all published through the atomic helpers here, so a reader never
// observes a half-written file even when a writer crashes mid-run.
//
// Every write goes to a temporary file in the destination directory and is
// then renamed into place. Rename is atomic within a directory on the
// platforms Sift targets, so the destination either holds the previous
// bytes or the complete new bytes.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"sift/internal/core"
)

// dirPerm is the mode used for directories created by this package.
const dirPerm os.FileMode = 0o755

// filePerm is the mode used for files written by WriteJSONAtomic.
const filePerm os.FileMode = 0o644

// WriteFileAtomic writes data to path so that a concurrent reader sees either
// the old contents or all of the new contents. Parent directories are created
// when missing, and the temporary file is removed if any step fails.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return writeAtomic(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// WriteJSONAtomic encodes v as JSON and writes it atomically to path. The
// encoding is two-space indented and ends with a newline, and object keys are
// emitted in a deterministic order (struct field order, sorted map keys), so
// re-encoding unchanged data reproduces identical bytes and therefore an
// identical digest.
func WriteJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("store: encode %s: %w", path, err)
	}
	data = append(data, '\n')
	return WriteFileAtomic(path, data, filePerm)
}

// ReadJSON decodes the JSON file at path into v. A missing file yields an
// error matching core.ErrNotFound so callers can treat absence as a normal
// state; malformed contents yield a decoding error instead.
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return notFound(path)
		}
		return fmt.Errorf("store: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("store: decode %s: %w", path, err)
	}
	return nil
}

// SHA256File returns the lower-case hex SHA-256 of the file at path. A missing
// file yields an error matching core.ErrNotFound.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", notFound(path)
		}
		return "", fmt.Errorf("store: open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("store: read %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SHA256Bytes returns the lower-case hex SHA-256 of b.
func SHA256Bytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// EnsureDir creates path and any missing parents. It succeeds when path is
// already a directory and fails when path exists as something else.
func EnsureDir(path string) error {
	if err := os.MkdirAll(path, dirPerm); err != nil {
		return fmt.Errorf("store: create directory %s: %w", path, err)
	}
	return nil
}

// RemoveAllContents removes every entry inside dir but keeps dir itself, so a
// staging directory can be reused without being recreated. A directory that
// does not exist is not an error; a path that exists but is not a directory
// is.
func RemoveAllContents(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("store: stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("store: clear %s: not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("store: read directory %s: %w", dir, err)
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("store: remove %s: %w", p, err)
		}
	}
	return nil
}

// CopyDir copies the tree rooted at src into dst, creating dst when needed.
// Regular files are copied atomically with their source permissions and
// subdirectories are copied recursively; symlinks and other irregular entries
// are skipped rather than followed. A missing src yields an error matching
// core.ErrNotFound. Existing files in dst with the same names are replaced,
// and entries in dst that are absent from src are left alone.
func CopyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return notFound(src)
		}
		return fmt.Errorf("store: stat %s: %w", src, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("store: copy %s: not a directory", src)
	}
	if err := EnsureDir(dst); err != nil {
		return err
	}
	// os.ReadDir sorts by file name, so the copy order is deterministic.
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("store: read directory %s: %w", src, err)
	}
	for _, e := range entries {
		from := filepath.Join(src, e.Name())
		to := filepath.Join(dst, e.Name())
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			continue
		case e.IsDir():
			if err := CopyDir(from, to); err != nil {
				return err
			}
		case e.Type().IsRegular():
			fi, err := e.Info()
			if err != nil {
				return fmt.Errorf("store: stat %s: %w", from, err)
			}
			if err := copyFile(from, to, fi.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyFile streams src into dst atomically, keeping large files out of memory.
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return notFound(src)
		}
		return fmt.Errorf("store: open %s: %w", src, err)
	}
	defer in.Close()
	return writeAtomic(dst, perm, func(w io.Writer) error {
		_, err := io.Copy(w, in)
		return err
	})
}

// writeAtomic fills a temporary file in the destination directory using write,
// flushes it to stable storage and renames it over path. The temporary file is
// removed on every failure path.
func writeAtomic(path string, perm os.FileMode, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	if err := EnsureDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp")
	if err != nil {
		return fmt.Errorf("store: create temporary file in %s: %w", dir, err)
	}
	name := tmp.Name()
	// After a successful rename the temporary name is gone and this is a
	// no-op; on every failure path it cleans up the partial file.
	defer os.Remove(name)

	if err := write(tmp); err != nil {
		tmp.Close()
		return fmt.Errorf("store: write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("store: sync %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("store: close %s: %w", path, err)
	}
	if err := os.Chmod(name, perm); err != nil {
		return fmt.Errorf("store: chmod %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("store: rename %s to %s: %w", name, path, err)
	}
	return nil
}

// notFound reports a missing path as an error matching core.ErrNotFound.
func notFound(path string) error {
	return fmt.Errorf("store: %s: %w", path, core.ErrNotFound)
}
