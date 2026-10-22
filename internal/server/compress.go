package server

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// minCompressBytes is the size below which a file is served as is: a gzip
// stream costs ~20 bytes of framing plus the Content-Encoding and Vary headers,
// and a response that small fits a single packet either way.
const minCompressBytes = 1024

// compressible is an allow-list of text types. Anything not named here (png,
// jpg, woff2, zip, ...) is already compressed or unknown and is never touched.
var compressible = map[string]bool{
	".html": true, ".js": true, ".mjs": true, ".css": true, ".svg": true,
	".json": true, ".txt": true, ".xml": true, ".webmanifest": true, ".map": true,
}

// gzipCache holds each bundle file's gzip form. A file is compressed at most
// once per process (the bundle is immutable and embedded), never per request;
// the key includes size and mtime so the on-disk dev override stays correct.
type gzipCache struct {
	mu sync.Mutex
	m  map[string]gzipEntry
}

type gzipEntry struct {
	size int64
	mod  time.Time
	gz   []byte
}

// get returns the gzip form of name, or nil if compressing it would not help.
func (c *gzipCache) get(fsys fs.FS, name string, size int64, mod time.Time) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[name]; ok && e.size == size && e.mod.Equal(mod) {
		return e.gz
	}
	var gz []byte
	if b, err := fs.ReadFile(fsys, name); err == nil {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		_, _ = zw.Write(b)
		if zw.Close() == nil && buf.Len() < len(b) {
			gz = buf.Bytes()
		}
	}
	if c.m == nil {
		c.m = map[string]gzipEntry{}
	}
	c.m[name] = gzipEntry{size: size, mod: mod, gz: gz}
	return gz
}

// canGzip reports whether a file of this name and size is worth compressing.
func canGzip(name string, size int64) bool {
	return size >= minCompressBytes && compressible[strings.ToLower(path.Ext(name))]
}

// acceptsGzip reads Accept-Encoding: gzip (or *) with a non-zero q-value.
func acceptsGzip(header string) bool {
	star, gz := false, false
	seen := false
	for _, part := range strings.Split(header, ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		coding = strings.ToLower(strings.TrimSpace(coding))
		q := 1.0
		if k, v, ok := strings.Cut(strings.TrimSpace(params), "="); ok && strings.EqualFold(strings.TrimSpace(k), "q") {
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				q = f
			}
		}
		switch coding {
		case "gzip", "x-gzip":
			gz, seen = q > 0, true
		case "*":
			star = q > 0
		}
	}
	if seen {
		return gz
	}
	return star
}
