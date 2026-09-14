package udb

import "testing"

func TestV518ZSetBatchMatchesCanonicalZSet(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()

	entries := []ZEntry{
		{Member: []byte("a"), Score: 10},
		{Member: []byte("b"), Score: 10},
		{Member: []byte("c"), Score: 20},
	}
	if err := db.ZSetBatch("batch", entries); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		r := db.ZGet("batch", e.Member)
		if r.Err != nil {
			t.Fatalf("ZGet(%q): %v", e.Member, r.Err)
		}
		if len(r.Data) != 1 || B2i(r.Data[0]) != e.Score {
			t.Fatalf("ZGet(%q): got %#v, want score %d", e.Member, r.Data, e.Score)
		}
	}

	// Changing a score must remove the old ordered-index entry and retain the
	// new one. Equal-score members also exercise the shared-score index path.
	if err := db.ZSetBatch("batch", []ZEntry{{Member: []byte("a"), Score: 30}, {Member: []byte("b"), Score: 10}}); err != nil {
		t.Fatal(err)
	}
	report, err := db.CheckIntegrity()
	if err != nil {
		t.Fatal(err)
	}
	if !report.Consistent {
		t.Fatalf("inconsistent after batch score change: %+v", report.Issues)
	}
}

func TestV518ZSetBatchAtomicValidation(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()

	entries := []ZEntry{
		{Member: []byte("ok"), Score: 1},
		{Member: nil, Score: 2},
	}
	if err := db.ZSetBatch("atomic-v518", entries); err == nil {
		t.Fatal("expected validation error")
	}
	if r := db.ZGet("atomic-v518", []byte("ok")); r.Err != ErrBucketNotFound && r.Err != ErrKeyNotFound {
		t.Fatalf("partial write after validation failure: err=%v state=%s", r.Err, r.State)
	}
}
