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

// unpack writes the tarball's files into dir: all of them beside their targets first, synced, then
// each renamed into place, so running binaries are replaced rather than written over, and a
// download or disk failing part-way leaves the old version whole, not the new connaio with the old
// core.
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
	var written []string // the targets, each written at target+".new"
	defer func() {
		for _, dst := range written {
			os.Remove(dst + ".new") // gone once renamed; left on a failure
		}
	}()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		// the tarball is flat: nothing lands outside dir
		dst := filepath.Join(dir, filepath.Base(h.Name))
		if err := writeNew(dst, tr, os.FileMode(h.Mode).Perm()); err != nil {
			return err
		}
		written = append(written, dst)
	}
	for _, dst := range written {
		if err := os.Rename(dst+".new", dst); err != nil {
			return err
		}
	}
	return nil
}

// writeNew writes r to dst+".new", synced to disk.
func writeNew(dst string, r io.Reader, perm os.FileMode) error {
	w, err := os.OpenFile(dst+".new", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	err = errors.Join(err, w.Sync(), w.Close())
	if err != nil {
		os.Remove(dst + ".new")
	}
	return err
}

// writeRenamed writes r beside dst and renames it over dst.
func writeRenamed(dst string, r io.Reader, perm os.FileMode) error {
	if err := writeNew(dst, r, perm); err != nil {
		return err
	}
	if err := os.Rename(dst+".new", dst); err != nil {
		os.Remove(dst + ".new")
		return err
	}
	return nil
}
