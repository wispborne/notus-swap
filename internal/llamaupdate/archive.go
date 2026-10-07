package llamaupdate

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// openTar opens a .tar.gz file. Closing the returned closer closes the file.
func openTar(archive string) (*tar.Reader, io.Closer, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, nil, err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("%s is not a .tar.gz file: %w", filepath.Base(archive), err)
	}
	return tar.NewReader(gz), f, nil
}

// inside cleans a path from an archive and checks that it stays inside the
// folder it is unpacked into.
func inside(name string) (string, error) {
	clean := path.Clean(name)
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("the archive has a path outside its folder: %s", name)
	}
	return clean, nil
}

// untar unpacks a .tar.gz into dir, creating dir. It keeps folders, files,
// file permissions (without setuid), and links that point inside the
// archive. It refuses an archive with any path or link that leads outside.
func untar(archive, dir string) error {
	tr, closer, err := openTar(archive)
	if err != nil {
		return err
	}
	defer closer.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", filepath.Base(archive), err)
		}
		name, err := inside(h.Name)
		if err != nil {
			return err
		}
		if name == "." {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, 0o755)
		case tar.TypeReg:
			err = writeFile(target, tr, h.FileInfo().Mode().Perm())
		case tar.TypeSymlink:
			if path.IsAbs(h.Linkname) {
				return fmt.Errorf("the archive has a link to an absolute path: %s -> %s", h.Name, h.Linkname)
			}
			if _, err := inside(path.Join(path.Dir(name), h.Linkname)); err != nil {
				return fmt.Errorf("the archive has a link outside its folder: %s -> %s", h.Name, h.Linkname)
			}
			err = os.Symlink(h.Linkname, target)
		case tar.TypeLink:
			var src string
			if src, err = inside(h.Linkname); err == nil {
				err = os.Link(filepath.Join(dir, filepath.FromSlash(src)), target)
			}
		default:
			// Devices, pipes and the like have no place in a release.
		}
		if err != nil {
			return err
		}
	}
}

func writeFile(target string, r io.Reader, perm os.FileMode) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// extractFile copies the first regular file named base (in any folder) out
// of a .tar.gz, to dst.
func extractFile(archive, base, dst string, perm os.FileMode) error {
	tr, closer, err := openTar(archive)
	if err != nil {
		return err
	}
	defer closer.Close()
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s has no file named %s", filepath.Base(archive), base)
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", filepath.Base(archive), err)
		}
		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == base {
			return writeFile(dst, tr, perm)
		}
	}
}

// onlyFolder returns the single folder inside dir when dir holds nothing
// else, and dir itself otherwise. Release archives often wrap everything in
// one top-level folder.
func onlyFolder(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return dir
	}
	return filepath.Join(dir, entries[0].Name())
}

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
