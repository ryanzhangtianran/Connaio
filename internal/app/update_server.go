//go:build server

package app

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// unpack writes the tarball's files into dir, each renamed into place, so running binaries are
// replaced rather than written over.
func unpack(tarball, dir string) error {
	f, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		// the tarball is flat: nothing lands outside dir
		if err := writeRenamed(filepath.Join(dir, filepath.Base(h.Name)), tr, os.FileMode(h.Mode).Perm()); err != nil {
			return err
		}
	}
}

// writeRenamed writes r beside dst and renames it over dst.
func writeRenamed(dst string, r io.Reader, perm os.FileMode) error {
	tmp := dst + ".new"
	w, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
