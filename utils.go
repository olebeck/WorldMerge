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
