package udb

import (
	"errors"
	"testing"
	"time"
)

func TestV523UnifiedWriteCore(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()

	if err := db.Update(func(tx *Tx) error {
		return applyWriteOps(tx, []writeOp{
			{kind: writeHSet, name: "h", key: []byte("a"), value: []byte("1")},
			{kind: writeHSet, name: "h", key: []byte("b"), value: []byte("2")},
			{kind: writeHDel, name: "h", key: []byte("a")},
			{kind: writeZSet, name: "z", key: []byte("a"), score: 10},
			{kind: writeZSet, name: "z", key: []byte("b"), score: 20},
			{kind: writeZDel, name: "z", key: []byte("a")},
			{kind: writeZSet, name: "z", key: []byte("c"), score: 30},
		})
	}); err != nil {
		t.Fatal(err)
	}
	if r := db.HGet("h", []byte("a")); r.State != keyNotFound {
		t.Fatalf("HDel failed: %+v", r)
	}
	if r := db.HGet("h", []byte("b")); r.State != replyOK || string(r.Data[0]) != "2" {
		t.Fatalf("HSet failed: %+v", r)
	}
	if r := db.ZGet("z", []byte("a")); r.State != keyNotFound {
		t.Fatalf("ZDel failed: %+v", r)
	}
	var score uint64
	err := db.View(func(tx *Tx) error {
		var err error
		score, err = db.Zscore(tx, "z", []byte("c"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if score != 30 {
		t.Fatalf("unexpected score: %d", score)
	}
}

func TestV523BatchMetricsAcrossAPIs(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	db.ResetMetrics()

	if err := db.HSetBatch("h", []Entry{{Key: []byte("a"), Value: []byte("1")}, {Key: []byte("b"), Value: []byte("2")}}); err != nil {
		t.Fatal(err)
	}
	if err := db.ZSetBatch("z", []ZEntry{{Member: []byte("a"), Score: 1}}); err != nil {
		t.Fatal(err)
	}
	b, err := db.NewBatch()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.HDel("h", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	m := db.Metrics()
	if m.BatchCommits != 3 || m.BatchItems != 4 {
		t.Fatalf("unexpected batch metrics: %+v", m)
	}
}

func TestV523PipelineQueueWaitMetrics(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 4, MaxWait: time.Millisecond, QueueSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	f, err := p.HSetAsync("q", []byte("k"), []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(); err != nil {
		t.Fatal(err)
	}
	st := p.Stats()
	if st.Committed != 1 || st.TotalItems != 1 || st.Batches != 1 {
		t.Fatalf("unexpected pipeline metrics: %+v", st)
	}
	if st.TotalQueueWaitLatency < 0 || st.AvgQueueWaitLatency < 0 || st.MaxQueueWaitLatency < 0 {
		t.Fatalf("invalid queue latency metrics: %+v", st)
	}
}

func TestV523SnapshotMetrics(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	if err := db.HSet("h", []byte("k"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	db.ResetMetrics()
	s, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := db.Metrics()
	if m.SnapshotsOpened != 1 || m.SnapshotsActive != 1 {
		t.Fatalf("snapshot counters: %+v", m)
	}
	if m.SnapshotBytes == 0 || m.SnapshotDuration <= 0 || m.SnapshotMaxBytes == 0 || m.AvgSnapshotBytes <= 0 || m.AvgSnapshotDuration <= 0 {
		t.Fatalf("snapshot metrics not recorded: %+v", m)
	}
}

func TestV523InvalidWriteOpIsAtomic(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	err := db.Update(func(tx *Tx) error {
		return applyWriteOps(tx, []writeOp{
			{kind: writeHSet, name: "atomic", key: []byte("ok"), value: []byte("value")},
			{kind: writeOpKind(255), name: "atomic", key: []byte("bad")},
		})
	})
	if !errors.Is(err, errInvalidWriteOp) {
		t.Fatalf("unexpected error: %v", err)
	}
	if r := db.HGet("atomic", []byte("ok")); r.State != bucketNotFound && r.State != keyNotFound {
		t.Fatalf("partial write survived rollback: %+v", r)
	}
}
