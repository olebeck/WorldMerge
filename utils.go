package main

import (
	"archive/zip"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

func UnpackZip(filename, unpackFolder string) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	s, _ := f.Stat()
	zr, _ := zip.NewReader(f, s.Size())
	for _, srcFile := range zr.File {
		srcName := filepath.ToSlash(srcFile.Name)
		outPath := path.Join(unpackFolder, srcName)
		if srcFile.Mode().IsDir() {
			os.Mkdir(outPath, 0o755)
		} else {
			os.MkdirAll(path.Dir(outPath), 0o755)
			fr, _ := srcFile.Open()
			f, _ := os.Create(path.Join(unpackFolder, srcName))
			io.Copy(f, fr)
		}
	}
	return nil
}

func ZipFolder(filename, folder string) error {
	f, err := os.Create(filename)
	if err != nil {
		logrus.Fatal(err)
	}
	zw := zip.NewWriter(f)
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
