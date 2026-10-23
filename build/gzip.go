//go:build ignore

// gzip compresses a file as `gzip -9 -n` does, for build hosts without one (Windows):
// go run build/gzip.go <file> leaves <file>.gz in its place.
package main

import (
	"compress/gzip"
	"log"
	"os"
)

func main() {
	path := os.Args[1]
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(path + ".gz")
	if err != nil {
		log.Fatal(err)
	}
	w, _ := gzip.NewWriterLevel(f, gzip.BestCompression) // no name or time in the header, as -n
	if _, err := w.Write(b); err != nil {
		log.Fatal(err)
	}
	if err := w.Close(); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		log.Fatal(err)
	}
}
