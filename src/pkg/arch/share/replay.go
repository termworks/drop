package share

import (
	"fmt"
	"io"
	"os"
)

type replayBody struct {
	original io.Reader
	cache    *os.File
	cached   int64
	eof      bool
}

func newReplay(original io.Reader) (*replayBody, error) {
	cache, err := os.CreateTemp("", ".drop-transfer-*")
	if err != nil {
		return nil, err
	}
	return &replayBody{original: original, cache: cache}, nil
}

func (r *replayBody) reader() io.Reader {
	cached := io.NewSectionReader(r.cache, 0, r.cached)
	if r.eof {
		return cached
	}
	return io.MultiReader(cached, &cachingReader{body: r})
}

func (r *replayBody) close() error {
	name := r.cache.Name()
	err := r.cache.Close()
	if removeErr := os.Remove(name); err == nil && removeErr != nil && !os.IsNotExist(removeErr) {
		err = removeErr
	}
	return err
}

type cachingReader struct {
	body *replayBody
}

func (r *cachingReader) Read(buf []byte) (int, error) {
	n, readErr := r.body.original.Read(buf)
	if n > 0 {
		written := 0
		for written < n {
			m, err := r.body.cache.WriteAt(buf[written:n], r.body.cached+int64(written))
			written += m
			if err != nil {
				return 0, fmt.Errorf("caching transfer input: %w", err)
			}
			if m == 0 {
				return 0, io.ErrShortWrite
			}
		}
		r.body.cached += int64(n)
	}
	if readErr == io.EOF {
		r.body.eof = true
	}
	return n, readErr
}
