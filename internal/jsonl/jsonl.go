// Package jsonl reads session files line by line, transparently handling
// zstd-compressed (.jsonl.zst) Codex rollouts and arbitrarily long lines.
package jsonl

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// Open returns a reader over the decompressed file contents.
func Open(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(path, ".zst") {
		return f, nil
	}
	d, err := zstd.NewReader(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &zstReader{d: d, f: f}, nil
}

type zstReader struct {
	d *zstd.Decoder
	f *os.File
}

func (z *zstReader) Read(p []byte) (int, error) { return z.d.Read(p) }
func (z *zstReader) Close() error               { z.d.Close(); return z.f.Close() }

// Each calls fn with every JSON object in r. Lines that don't decode are skipped
// (a file that's being written can end with a torn line). fn returns false to stop.
func Each(r io.Reader, fn func(obj map[string]any) bool) error {
	br := bufio.NewReaderSize(r, 256*1024)
	for {
		line, err := br.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 && line[0] == '{' {
			var obj map[string]any
			dec := json.NewDecoder(bytes.NewReader(line))
			dec.UseNumber()
			if dec.Decode(&obj) == nil && !fn(obj) {
				return nil
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// EachFile is Each over a path.
func EachFile(path string, fn func(obj map[string]any) bool) error {
	r, err := Open(path)
	if err != nil {
		return err
	}
	defer r.Close()
	return Each(r, fn)
}

// Scan reads a session file once, decoding as little as possible:
//   - the first headLines objects go to head, until head returns false;
//   - after that, only lines containing one of markers (checked on raw bytes,
//     which is much cheaper than JSON decoding) are decoded and passed to marked.
//
// Metadata like titles can be appended anywhere in a session, so the whole file
// is read, but most lines are never decoded.
func Scan(path string, headLines int, head func(map[string]any) bool, markers [][]byte, marked func(map[string]any)) error {
	r, err := Open(path)
	if err != nil {
		return err
	}
	defer r.Close()
	br := bufio.NewReaderSize(r, 1<<20)
	n, inHead := 0, true
	var buf []byte
	for {
		// ReadSlice avoids allocating for every line; long lines are reassembled.
		chunk, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			buf = append(buf, chunk...)
			continue
		}
		line := chunk
		if len(buf) > 0 {
			buf = append(buf, chunk...)
			line = buf
		}
		if t := bytes.TrimSpace(line); len(t) > 0 && t[0] == '{' {
			n++
			if inHead && n > headLines {
				inHead = false
			}
			if inHead {
				if o := decode(t); o != nil && !head(o) {
					inHead = false
				}
			} else if marked != nil {
				for _, m := range markers {
					if bytes.Contains(t, m) {
						if o := decode(t); o != nil {
							marked(o)
						}
						break
					}
				}
			}
		}
		buf = buf[:0]
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func decode(line []byte) map[string]any {
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if dec.Decode(&obj) != nil {
		return nil
	}
	return obj
}
