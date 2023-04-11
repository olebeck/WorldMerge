package main

import (
	"archive/zip"
	"compress/flate"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

func UnpackZip(filename, unpackFolder string, filteFn func(string) bool) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	s, _ := f.Stat()
	zr, _ := zip.NewReader(f, s.Size())
	for _, srcFile := range zr.File {
		srcName := filepath.ToSlash(srcFile.Name)
		if !filteFn(srcName) {
			continue
		}

		outPath := path.Join(unpackFolder, srcName)
		if srcFile.Mode().IsDir() {
			os.Mkdir(outPath, 0o755)
		} else {
			os.MkdirAll(path.Dir(outPath), 0o755)
			fr, _ := srcFile.Open()
			f, _ := os.Create(path.Join(unpackFolder, srcName))
			f.Chmod(0775)
			io.Copy(f, fr)
			fr.Close()
			f.Close()
		}
	}
	return nil
}

func ZipFolder(filename, folder string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)

	// Register a custom Deflate compressor.
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.NoCompression)
	})

	err = filepath.WalkDir(folder, func(path string, d fs.DirEntry, err error) error {
		if !d.Type().IsDir() {
			rel := path[len(folder)+1:]
			zwf, _ := zw.Create(rel)
			data, err := os.ReadFile(path)
			if err != nil {
				logrus.Error(err)
			}
			zwf.Write(data)
		}
		return nil
	})
	zw.Close()
	f.Close()
	return err
}

// ChunkPos is the position of a chunk. It is composed of two integers and is written as two varint32s.
type ChunkPos [2]int32

// X returns the X coordinate of the chunk position. It is equivalent to ChunkPos[0].
func (pos ChunkPos) X() int32 {
	return pos[0]
}

// Z returns the Z coordinate of the chunk position. It is equivalent to ChunkPos[1].
func (pos ChunkPos) Z() int32 {
	return pos[1]
}

func (pos ChunkPos) Add(pos2 [2]int32) (out ChunkPos) {
	out[0] = pos[0] + pos2[0]
	out[1] = pos[1] + pos2[1]
	return
}

func (pos ChunkPos) Sub(pos2 [2]int32) (out ChunkPos) {
	out[0] = pos[0] - pos2[0]
	out[1] = pos[1] - pos2[1]
	return
}

func (pos ChunkPos) Div(d int32) (out ChunkPos) {
	out[0] = pos[0] / d
	out[1] = pos[1] / d
	return
}

func glob(dir string, ext string) ([]string, error) {
	files := []string{}
	err := filepath.Walk(dir, func(path string, f os.FileInfo, err error) error {
		if filepath.Ext(path) == ext {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
