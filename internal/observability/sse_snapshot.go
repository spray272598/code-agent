package observability

var sseSnapshotFn func() []SSECounter

type SSECounter struct {
	Name  string
	Help  string
	Type  string
	Value int64
}

func RegisterSSESnapshot(fn func() []SSECounter) {
	sseSnapshotFn = fn
}

func sseCountersSnapshot() []SSECounter {
	if sseSnapshotFn == nil {
		return nil
	}
	return sseSnapshotFn()
}
