package udb

import "bytes"

// V5.31 adds a data-independent adaptive cursor start strategy.
//
// A sorted batch can be served by either:
//   1. Cursor.First + Next: excellent when the first requested key is near the
//      beginning of the bucket, but potentially expensive for deep keys.
//   2. Cursor.Seek + Next: excellent for deep ranges, but pays a B-tree seek
//      even when the requested range starts at the head.
//
// There is no cheap, public bbolt API that exposes a key's ordinal position.
// V5.31 therefore uses a bounded probe: start at First, walk at most
// HeadProbeKeys entries, and if the requested range is not reached, restart at
// Seek(firstKey). This keeps the transaction short-lived and bounds the extra
// work for deep ranges while preserving the very fast head case.

type ReadCursorStart uint8

const (
	ReadCursorAdaptive ReadCursorStart = iota + 1
	ReadCursorFirst
	ReadCursorSeek
)

// ReadCursorOptions controls the V5.31 adaptive cursor path.
type ReadCursorOptions struct {
	// HeadProbeKeys is the maximum number of entries inspected from Cursor.First
	// before falling back to Cursor.Seek. Values below 1 are normalized to 1.
	HeadProbeKeys int
}

func defaultReadCursorOptions() ReadCursorOptions {
	return ReadCursorOptions{HeadProbeKeys: 8}
}

func normalizeReadCursorOptions(opts ReadCursorOptions) ReadCursorOptions {
	if opts.HeadProbeKeys < 1 {
		opts.HeadProbeKeys = 1
	}
	return opts
}

// adaptiveCursor positions c at firstKey. If the requested range is within
// the bounded head probe, the original First path is retained. Otherwise the
// cursor is restarted with Seek(firstKey). The returned cursor position is
// ready for the normal sorted scan loop.
func adaptiveCursor(c interface {
	First() ([]byte, []byte)
	Seek([]byte) ([]byte, []byte)
	Next() ([]byte, []byte)
}, firstKey []byte, opts ReadCursorOptions) ([]byte, []byte) {
	opts = normalizeReadCursorOptions(opts)
	k, v := c.First()
	for i := 0; k != nil && i < opts.HeadProbeKeys; i++ {
		if bytesCompare(k, firstKey) >= 0 {
			return k, v
		}
		k, v = c.Next()
	}
	return c.Seek(firstKey)
}

// bytesCompare is kept as a tiny indirection so the adaptive helper can be
// tested with the same comparison semantics as bbolt's byte-key ordering.
func bytesCompare(a, b []byte) int {
	return bytes.Compare(a, b)
}
