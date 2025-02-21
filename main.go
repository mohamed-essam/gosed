// Copyright GoSed (c) 2021, Carter Peel
// This code is licensed under MIT license (see LICENSE for details)

package gosed

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"time"
)

// Replacer contains all of the methods needed to properly execute replace operations
type Replacer struct {
	Config *replacerConfig
}

// replacerConfig contains all of the config variables
type replacerConfig struct {
	File         *os.File
	FilePath     string
	FileSize     int64
	FilePerm     os.FileMode
	Asynchronous bool
	Mappings     *replacerMappings
}

// replacerStringMappings maps old byte sequences to new byte sequences
type replacerMappings struct {
	Keys    [][]byte
	Indices [][]byte
}

// NewReplacer returns a new *Replacer type
func NewReplacer(fileName string) (*Replacer, error) {
	fd, err := os.Stat(fileName)
	if err != nil {
		return nil, err
	}
	fi, err := os.OpenFile(fileName, os.O_RDWR, fd.Mode().Perm())
	if err != nil {
		return nil, err
	}
	return &Replacer{
		Config: &replacerConfig{
			File:     fi,
			FilePath: fileName,
			FileSize: fd.Size(),
			FilePerm: fd.Mode().Perm(),
			Mappings: &replacerMappings{
				Keys:    make([][]byte, 0),
				Indices: make([][]byte, 0),
			},
		},
	}, nil
}

// NewMapping maps a new oldString:newString []byte entry
func (rp *Replacer) NewMapping(oldString, newString []byte) error {
	switch len(oldString) {
	case 0:
		return fmt.Errorf("cannot replace empty string with new value")
	}
	rp.Config.Mappings.Keys = append(rp.Config.Mappings.Keys, oldString)
	rp.Config.Mappings.Indices = append(rp.Config.Mappings.Indices, newString)
	return nil
}

// NewStringMapping maps a new oldString:newString string entry
func (rp *Replacer) NewStringMapping(oldString, newString string) error {
	switch oldString {
	case "":
		return fmt.Errorf("cannot replace empty string with new value")
	}
	rp.Config.Mappings.Keys = append(rp.Config.Mappings.Keys, []byte(oldString))
	rp.Config.Mappings.Indices = append(rp.Config.Mappings.Indices, []byte(newString))
	return nil
}

func (rp *Replacer) Reset() error {
	var err error
	if err := rp.Config.File.Close(); err != nil {
		return err
	}
	fd, err := os.Stat(rp.Config.FilePath)
	if err != nil {
		return err
	}
	rp.Config.File, err = os.OpenFile(rp.Config.FilePath, os.O_RDWR, fd.Mode().Perm())
	if err != nil {
		return err
	}
	rp.Config.Mappings.Keys = rp.Config.Mappings.Keys[:0]
	rp.Config.Mappings.Indices = rp.Config.Mappings.Indices[:0]
	rp.Config.FilePerm = fd.Mode().Perm()
	return nil
}

// ReplaceChained does the replace operation with a chained reader model
func (rp *Replacer) ReplaceChained() (int, error) {
	return DoChainReplace(rp)
}

// Replace does the replace operation with a concurrent (sequential) reader --> tmpfile model
func (rp *Replacer) Replace() (int, error) {
	return DoSequentialReplace(rp)
}

// DoSequentialReplace does the replace operation without reader chaining, which is slower but less resource intensive.
func DoSequentialReplace(rp *Replacer) (int, error) {
	buf := bytes.NewBuffer(make([]byte, 8192))
	replacer := BytesReplacingReader{}
	DoSingleReplace := func(old, new []byte) (int, error) {
		tmpFile := path.Join(path.Dir(rp.Config.FilePath), fmt.Sprintf("tmp-gosed-%d", time.Now().UnixNano()))
		input, err := os.OpenFile(rp.Config.FilePath, os.O_RDWR, rp.Config.FilePerm)
		if err != nil {
			return 0, err
		}
		output, err := os.OpenFile(tmpFile, os.O_RDWR|os.O_CREATE, rp.Config.FilePerm)
		if err != nil {
			return 0, err
		}
		defer func(input, output *os.File) {
			_ = input.Close()
			_ = input.Close()
		}(input, output)
		replacer.Reset(bufio.NewReaderSize(input, 8192), old, new)
		wrote, err := io.CopyBuffer(output, &replacer, buf.Bytes())
		if err != nil {
			return 0, err
		}
		if err := os.Rename(tmpFile, rp.Config.FilePath); err != nil {
			return 0, err
		}
		rp.Config.FileSize = wrote
		return int(wrote), nil
	}
	var count int
	for index, key := range rp.Config.Mappings.Keys {
		wrote, err := DoSingleReplace(key, rp.Config.Mappings.Indices[index])
		if err != nil {
			return count, err
		}
		count += wrote
		rp.Config.FileSize = int64(wrote)
	}
	rp.Config.Mappings.Indices = rp.Config.Mappings.Indices[:0]
	rp.Config.Mappings.Keys = rp.Config.Mappings.Keys[:0]
	return count, nil

}

// DoChainReplace does the replace operation with reader chaining, which is faster but more resource intensive.
func DoChainReplace(rp *Replacer) (int, error) {
	tmpfile := fmt.Sprintf("tmp-gosed-%d", time.Now().UnixNano())
	input, err := os.OpenFile(rp.Config.FilePath, os.O_RDWR, rp.Config.FilePerm)
	if err != nil {
		return 0, err
	}
	output, err := os.OpenFile(tmpfile, os.O_RDWR|os.O_CREATE, rp.Config.FilePerm)
	if err != nil {
		return 0, err
	}
	defer func(input, output *os.File) {
		_ = input.Close()
		_ = input.Close()
	}(input, output)
	var replacer = NewBytesReplacingReader(bufio.NewReaderSize(input, 8192), rp.Config.Mappings.Keys[0], rp.Config.Mappings.Indices[0])
	//replacer.SetBufferSize(8192*4)
	for index, key := range rp.Config.Mappings.Keys {
		if index == 0 {
			continue
		}
		replacer = NewBytesReplacingReader(replacer, key, rp.Config.Mappings.Indices[index])
	}
	wrote, err := io.CopyBuffer(output, replacer, make([]byte, 8192))
	if err != nil {
		return 0, err
	}
	if err := os.Remove(rp.Config.FilePath); err != nil {
		return 0, err
	}
	if err := os.Rename(tmpfile, rp.Config.FilePath); err != nil {
		return 0, err
	}
	rp.Config.FileSize = wrote
	rp.Config.Mappings.Indices = rp.Config.Mappings.Indices[:0]
	rp.Config.Mappings.Keys = rp.Config.Mappings.Keys[:0]
	return int(wrote), nil
}
