package web

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The interface is Go compiled to WebAssembly, which is large and compresses to about a quarter of
// itself. Sent uncompressed it is eighteen megabytes a person waits through before seeing anything.
var packed = sync.OnceValues(func() ([]byte, error) {
	raw, err := assets.ReadFile("assets/drop.wasm")
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	writer, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(raw); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
})

// wasm serves the interface, compressed when the browser will take it.
//
// Compressed once and kept, not per request: it is the same bytes for everyone, and doing it again
// for each viewer would cost a second of processor time to save nothing.
func (s *Server) wasm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/wasm")

	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		http.ServeFileFS(w, r, assets, "assets/drop.wasm")
		return
	}

	body, err := packed()
	if err != nil {
		http.ServeFileFS(w, r, assets, "assets/drop.wasm")
		return
	}

	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Vary", "Accept-Encoding")
	http.ServeContent(w, r, "drop.wasm", time.Time{}, bytes.NewReader(body))
}
