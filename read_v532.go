package udb

// V5.32 Read Engine 4.0 adds an explicit access-path model on top of the
// V5.31 cursor-start strategy. The planner still has only cheap information
// available before opening the read transaction: key count, sortedness,
// locality and duplicates. It therefore chooses Point for small/sparse or
// unsorted workloads and Cursor for sufficiently large/clustered sorted
// workloads. For Cursor, CursorStart controls First, Seek, or Adaptive.
//
// The important constraint is unchanged: all execution happens inside one
// short-lived managed bbolt read transaction. There is no reader pool, cache,
// global read lock, or long-lived bbolt transaction.

type ReadAccessPath uint8

const (
	ReadAccessPoint ReadAccessPath = iota + 1
	ReadAccessFirst
	ReadAccessSeek
	ReadAccessAdaptive
)

func (p ReadAccessPath) String() string {
	switch p {
	case ReadAccessPoint:
		return "point"
	case ReadAccessFirst:
		return "first"
	case ReadAccessSeek:
		return "seek"
	case ReadAccessAdaptive:
		return "adaptive"
	default:
		return "unknown"
	}
}

// accessPathFromCursorStart converts the public cursor-start mode into the
// corresponding V5.32 access-path diagnostic value.
func accessPathFromCursorStart(start ReadCursorStart) ReadAccessPath {
	switch start {
	case ReadCursorFirst:
		return ReadAccessFirst
	case ReadCursorSeek:
		return ReadAccessSeek
	case ReadCursorAdaptive:
		return ReadAccessAdaptive
	default:
		return ReadAccessAdaptive
	}
}

// cursorPosition selects the requested cursor start without allocating.
func cursorPosition(c interface {
	First() ([]byte, []byte)
	Seek([]byte) ([]byte, []byte)
	Next() ([]byte, []byte)
}, firstKey []byte, start ReadCursorStart, probeKeys int) ([]byte, []byte) {
	switch start {
	case ReadCursorFirst:
		return c.First()
	case ReadCursorSeek:
		return c.Seek(firstKey)
	case ReadCursorAdaptive:
		return adaptiveCursor(c, firstKey, ReadCursorOptions{HeadProbeKeys: probeKeys})
	default:
		return adaptiveCursor(c, firstKey, ReadCursorOptions{HeadProbeKeys: probeKeys})
	}
}
