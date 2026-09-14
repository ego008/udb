package udb

import (
	"testing"
)

func openV57TestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestV57IntegrityHealthy(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()

	if err := db.Update(func(tx *Tx) error {
		if err := db.Hset(tx, "h", []byte("a"), []byte("1")); err != nil {
			return err
		}
		if err := db.Zset(tx, "z", []byte("a"), 10); err != nil {
			return err
		}
		return db.Zset(tx, "z", []byte("b"), 20)
	}); err != nil {
		t.Fatal(err)
	}

	report, err := db.CheckIntegrity()
	if err != nil {
		t.Fatal(err)
	}
	if !report.Consistent {
		t.Fatalf("unexpected inconsistency: %+v", report.Issues)
	}
	if len(report.Hashes) != 1 || len(report.ZSets) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestV57IntegrityRepairsMissingAndOrphanIndexes(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	if err := db.Update(func(tx *Tx) error {
		if err := db.Zset(tx, "z", []byte("a"), 10); err != nil {
			return err
		}
		if err := db.Zset(tx, "z", []byte("b"), 20); err != nil {
			return err
		}
		kb := tx.bucket(bucketName(zetKeyPrefix, "z"))
		if err := kb.Delete(Bconcat(I2b(10), []byte("a"))); err != nil {
			return err
		}
		return kb.Put(Bconcat(I2b(99), []byte("orphan")), nil)
	}); err != nil {
		t.Fatal(err)
	}

	before, err := db.CheckIntegrity()
	if err != nil {
		t.Fatal(err)
	}
	if before.Consistent || len(before.Issues) < 2 {
		t.Fatalf("corruption not detected: %+v", before)
	}

	rr, err := db.RepairIntegrity(RepairOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !rr.Consistent || rr.Repaired != 2 {
		t.Fatalf("unexpected repair report: %+v", rr)
	}

	after, err := db.CheckIntegrity()
	if err != nil {
		t.Fatal(err)
	}
	if !after.Consistent {
		t.Fatalf("still inconsistent: %+v", after.Issues)
	}
}

func TestV57IntegrityRepairsMismatchedIndex(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	if err := db.Update(func(tx *Tx) error { return db.Zset(tx, "z", []byte("a"), 10) }); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error {
		kb := tx.bucket(bucketName(zetKeyPrefix, "z"))
		if err := kb.Delete(Bconcat(I2b(10), []byte("a"))); err != nil {
			return err
		}
		return kb.Put(Bconcat(I2b(99), []byte("a")), nil)
	}); err != nil {
		t.Fatal(err)
	}
	rr, err := db.RepairIntegrity(RepairOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !rr.Consistent {
		t.Fatalf("repair failed: %+v", rr)
	}
	if err := db.View(func(tx *Tx) error {
		score, err := db.Zscore(tx, "z", []byte("a"))
		if err != nil {
			return err
		}
		if score != 10 {
			t.Fatalf("score changed: %d", score)
		}
		kb := tx.bucket(bucketName(zetKeyPrefix, "z"))
		if kb.Get(Bconcat(I2b(10), []byte("a"))) == nil {
			t.Fatal("correct index missing")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestV57IntegrityInvalidScoreRequiresExplicitDrop(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	if err := db.Update(func(tx *Tx) error {
		if err := db.Zset(tx, "z", []byte("good"), 10); err != nil {
			return err
		}
		return tx.bucket(bucketName(zetScorePrefix, "z")).Put([]byte("bad"), []byte{1, 2})
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := db.RepairIntegrity(RepairOptions{}); err == nil {
		t.Fatal("expected invalid score repair error")
	}
	rr, err := db.RepairIntegrity(RepairOptions{DropInvalidScores: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rr.Consistent || rr.Dropped != 1 {
		t.Fatalf("unexpected drop report: %+v", rr)
	}
}

func TestV57IntegrityDetectsOrphanKeyBucket(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	if err := db.Update(func(tx *Tx) error {
		b, err := getOrCreateBucket(tx, bucketName(zetKeyPrefix, "orphan"))
		if err != nil {
			return err
		}
		return b.Put(Bconcat(I2b(1), []byte("x")), nil)
	}); err != nil {
		t.Fatal(err)
	}
	report, err := db.CheckIntegrity()
	if err != nil {
		t.Fatal(err)
	}
	if report.Consistent {
		t.Fatal("orphan key bucket was not detected")
	}
	if report.Issues[0].Kind != issueMissingScoreIndex {
		t.Fatalf("unexpected issue: %+v", report.Issues)
	}
}
