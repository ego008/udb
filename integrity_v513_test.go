package udb

import "testing"

// TestV513IntegritySharedScore verifies that integrity checking recognizes
// every member when several ZSet members have the same score. The secondary
// index is ordered by score||member, so a cursor Seek(score) lands on the first
// member for that score and must scan the complete score range.
func TestV513IntegritySharedScore(t *testing.T) {
	db, err := Open(t.TempDir() + "/shared-score.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const score = uint64(100)
	for _, member := range []string{"k-01", "k-02", "k-03", "k-07", "k-09"} {
		if err := db.ZSet("shared", []byte(member), score); err != nil {
			t.Fatal(err)
		}
	}

	report, err := db.CheckIntegrity()
	if err != nil {
		t.Fatal(err)
	}
	if !report.Consistent {
		t.Fatalf("shared-score zset reported inconsistent: %+v", report.Issues)
	}
}

func TestV515IntegrityDetectsMissingAndMismatchedIndexes(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	if err := db.ZSet("z", []byte("a"), 10); err != nil {
		t.Fatal(err)
	}
	if err := db.ZSet("z", []byte("b"), 10); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error {
		kb := tx.bucket(bucketName(zetKeyPrefix, "z"))
		if err := kb.Delete(Bconcat(I2b(10), []byte("a"))); err != nil {
			return err
		}
		return kb.Put(Bconcat(I2b(20), []byte("b")), nil)
	}); err != nil {
		t.Fatal(err)
	}

	report, err := db.CheckIntegrity()
	if err != nil {
		t.Fatal(err)
	}
	if report.Consistent {
		t.Fatal("expected inconsistency")
	}
	if len(report.ZSets) != 1 {
		t.Fatalf("zset reports=%d", len(report.ZSets))
	}
	zr := report.ZSets[0]
	if zr.ScoreEntries != 2 || zr.KeyIndexEntries != 2 {
		t.Fatalf("unexpected counts: %+v", zr)
	}
	if zr.MissingKeyIndex != 2 || zr.MismatchedScore != 1 {
		t.Fatalf("unexpected mismatch accounting: %+v", zr)
	}
}

func TestV515ZscanEach(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	if err := db.ZSet("z", []byte("a"), 10); err != nil {
		t.Fatal(err)
	}
	if err := db.ZSet("z", []byte("b"), 20); err != nil {
		t.Fatal(err)
	}
	if err := db.View(func(tx *Tx) error {
		var got []string
		if err := db.zscanEach(tx, "z", nil, I2b(0), 10, false, func(member []byte, score uint64) error {
			got = append(got, string(member)+":"+B2ds(I2b(score)))
			return nil
		}); err != nil {
			return err
		}
		if len(got) != 2 || got[0] != "a:10" || got[1] != "b:20" {
			t.Fatalf("unexpected zscanEach result: %v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestV516IntegrityReportOrderWithoutSecondaryOrder(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()

	if err := db.ZSet("v516-order", []byte("a"), 10); err != nil {
		t.Fatal(err)
	}
	if err := db.ZSet("v516-order", []byte("b"), 20); err != nil {
		t.Fatal(err)
	}
	if err := db.ZSet("v516-order", []byte("c"), 30); err != nil {
		t.Fatal(err)
	}

	// Leave three different logical problems: a missing secondary index for
	// a, a wrong-score secondary index for b, and an orphan secondary index z.
	// The final orphan scan must preserve physical cursor order and must run
	// after all primary-side issues.
	if err := db.Update(func(tx *Tx) error {
		b := tx.bucket(bucketName(zetKeyPrefix, "v516-order"))
		if b == nil {
			return ErrBucketNotFound
		}
		if err := b.Delete(joinScoreMember(I2b(10), []byte("a"))); err != nil {
			return err
		}
		if err := b.Delete(joinScoreMember(I2b(20), []byte("b"))); err != nil {
			return err
		}
		return b.Put(joinScoreMember(I2b(99), []byte("b")), nil)
	}); err != nil {
		t.Fatal(err)
	}

	// Add a true orphan after the existing secondary entries.
	if err := db.Update(func(tx *Tx) error {
		b := tx.bucket(bucketName(zetKeyPrefix, "v516-order"))
		return b.Put(joinScoreMember(I2b(100), []byte("z")), nil)
	}); err != nil {
		t.Fatal(err)
	}

	r, err := db.CheckIntegrity()
	if err != nil {
		t.Fatal(err)
	}
	if r.Consistent {
		t.Fatal("expected inconsistency")
	}
	if len(r.Issues) != 4 {
		t.Fatalf("expected 4 issues, got %d: %+v", len(r.Issues), r.Issues)
	}
	if r.Issues[len(r.Issues)-1].Kind != issueMissingScoreIndex {
		t.Fatalf("expected final issue to be orphan secondary index, got %q", r.Issues[len(r.Issues)-1].Kind)
	}
	if string(r.Issues[len(r.Issues)-1].Member) != "z" {
		t.Fatalf("expected orphan member z, got %q", r.Issues[len(r.Issues)-1].Member)
	}
}
