package udb

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// v511Model is a deliberately small reference model. It stores only logical
// application state and knows nothing about UDB's physical buckets/indexes.
type v511Model struct {
	hash map[string][]byte
	zset map[string]uint64
}

func newV511Model() *v511Model {
	return &v511Model{hash: make(map[string][]byte), zset: make(map[string]uint64)}
}

func (m *v511Model) hashKey(k []byte) string { return string(k) }
func (m *v511Model) zKey(k []byte) string    { return string(k) }

func (m *v511Model) applyHashSet(k, v []byte) { m.hash[m.hashKey(k)] = append([]byte(nil), v...) }
func (m *v511Model) applyHashDel(k []byte)    { delete(m.hash, m.hashKey(k)) }
func (m *v511Model) applyZSet(k []byte, v uint64) {
	m.zset[m.zKey(k)] = v
}
func (m *v511Model) applyZDel(k []byte) { delete(m.zset, m.zKey(k)) }

func v511AddUint64(v uint64, step int64) (uint64, bool) {
	if step >= 0 {
		s := uint64(step)
		if v > math.MaxUint64-s {
			return 0, false
		}
		return v + s, true
	}
	s := uint64(-(step + 1)) + 1
	if v < s {
		return 0, false
	}
	return v - s, true
}

func v511CompareModel(t *testing.T, db *DB, m *v511Model) {
	v511CompareModelNamed(t, db, m, "fuzz-hash", "fuzz-zset")
}

func v511CompareModelNamed(t *testing.T, db *DB, m *v511Model, hashName, zsetName string) {
	t.Helper()
	err := db.View(func(tx *Tx) error {
		for i := 0; i < 16; i++ {
			k := []byte(fmt.Sprintf("k-%02d", i))
			want, ok := m.hash[string(k)]
			r := db.Hget(tx, hashName, k)
			if ok {
				if r.State != replyOK || len(r.Data) != 1 || !bytes.Equal(r.Data[0], want) {
					return fmt.Errorf("hash mismatch key=%q want=%q got=%+v", k, want, r)
				}
			} else if r.State == replyOK {
				return fmt.Errorf("unexpected hash value for key=%q", k)
			}

			zWant, zOK := m.zset[string(k)]
			zGot, zErr := db.Zscore(tx, zsetName, k)
			if zOK {
				if zErr != nil || zGot != zWant {
					return fmt.Errorf("zset mismatch key=%q want=%d got=%d err=%v", k, zWant, zGot, zErr)
				}
			} else if zErr == nil {
				return fmt.Errorf("unexpected zset value for key=%q", k)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func v511CheckClean(t *testing.T, db *DB) {
	t.Helper()
	report, err := db.CheckIntegrity()
	if err != nil {
		t.Fatal(err)
	}
	if !report.Consistent {
		t.Fatalf("integrity failure: %+v", report.Issues)
	}
}

func v511RunOperation(t *testing.T, db *DB, m *v511Model, op, keyID, value byte) {
	t.Helper()
	key := []byte(fmt.Sprintf("k-%02d", int(keyID)%16))
	hashValue := []byte{value, value ^ 0x5a, keyID}

	switch op % 6 {
	case 0:
		err := db.Update(func(tx *Tx) error { return db.Hset(tx, "fuzz-hash", key, hashValue) })
		if err != nil {
			t.Fatal(err)
		}
		m.applyHashSet(key, hashValue)
	case 1:
		err := db.Update(func(tx *Tx) error { return db.Hdel(tx, "fuzz-hash", key) })
		if err != nil {
			t.Fatal(err)
		}
		m.applyHashDel(key)
	case 2:
		score := uint64(value)*257 + uint64(keyID)
		err := db.Update(func(tx *Tx) error { return db.Zset(tx, "fuzz-zset", key, score) })
		if err != nil {
			t.Fatal(err)
		}
		m.applyZSet(key, score)
	case 3:
		err := db.Update(func(tx *Tx) error { return db.Zdel(tx, "fuzz-zset", key) })
		if err != nil {
			t.Fatal(err)
		}
		m.applyZDel(key)
	case 4:
		step := int64(int(value)%17) - 8
		old, exists := m.zset[string(key)]
		want, ok := v511AddUint64(old, step)
		if !exists {
			want, ok = v511AddUint64(0, step)
		}
		var got uint64
		err := db.Update(func(tx *Tx) error {
			var err error
			got, err = db.Zincr(tx, "fuzz-zset", key, step)
			return err
		})
		if ok {
			if err != nil || got != want {
				t.Fatalf("Zincr mismatch key=%q step=%d want=%d got=%d err=%v", key, step, want, got, err)
			}
			m.applyZSet(key, want)
		} else if err == nil {
			t.Fatalf("Zincr overflow/underflow unexpectedly succeeded key=%q step=%d", key, step)
		}
	case 5:
		// Read-only operation intentionally does not mutate the model.
		err := db.View(func(tx *Tx) error {
			_ = db.Hget(tx, "fuzz-hash", key)
			_, _ = db.Zscore(tx, "fuzz-zset", key)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestV511PropertyRandomOperationSequence(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "property.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	m := newV511Model()
	state := uint64(0x9e3779b97f4a7c15)
	for i := 0; i < 1200; i++ {
		state ^= state << 7
		state ^= state >> 9
		state ^= state << 8
		op := byte(state)
		keyID := byte(state >> 8)
		value := byte(state >> 16)
		v511RunOperation(t, db, m, op, keyID, value)

		if i%17 == 0 {
			v511CheckClean(t, db)
			v511CompareModel(t, db, m)
		}
	}
	v511CheckClean(t, db)
	v511CompareModel(t, db, m)
}

func TestV511PropertyCompactRepairCycles(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "cycles.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	m := newV511Model()
	for i := 0; i < 80; i++ {
		key := []byte(fmt.Sprintf("k-%02d", i%16))
		score := uint64(i*100 + i%7)
		if err := db.ZSet("cycle-zset", key, score); err != nil {
			t.Fatal(err)
		}
		m.applyZSet(key, score)
		if i%3 == 0 {
			if err := db.HSet("cycle-hash", key, []byte{byte(i), byte(i >> 8)}); err != nil {
				t.Fatal(err)
			}
			m.applyHashSet(key, []byte{byte(i), byte(i >> 8)})
		}
	}

	for cycle := 0; cycle < 5; cycle++ {
		// Deliberately remove one secondary index and add one orphan index. This
		// uses the package-private transaction only inside the regression test.
		err := db.Update(func(tx *Tx) error {
			kb := tx.bucket(bucketName(zetKeyPrefix, "cycle-zset"))
			if kb == nil {
				return fmt.Errorf("missing zset key bucket")
			}
			if err := kb.Delete(Bconcat(I2b(m.zset["k-03"]), []byte("k-03"))); err != nil {
				return err
			}
			return kb.Put(Bconcat(I2b(999999), []byte(fmt.Sprintf("orphan-%d", cycle))), nil)
		})
		if err != nil {
			t.Fatal(err)
		}

		report, err := db.CheckIntegrity()
		if err != nil {
			t.Fatal(err)
		}
		if report.Consistent {
			t.Fatal("corruption injection was not detected")
		}

		repair, err := db.RepairIntegrity(RepairOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !repair.Consistent {
			t.Fatalf("repair failed: %+v", repair.After.Issues)
		}
		v511CompareModelNamed(t, db, m, "cycle-hash", "cycle-zset")

		cfg := DefaultMaintenanceConfig()
		cfg.Enabled = false
		cfg.MinDBSize = 0
		cfg.FreeRatio = 0
		cfg.PendingRatio = 1
		cfg.TxMaxSize = 64 << 10
		cfg.CheckBeforeCompact = true
		cfg.CheckAfterCompact = true
		cfg.IntegrityBeforeCompact = true
		cfg.IntegrityAfterCompact = true
		cfg.KeepBackup = false
		if _, _, err := db.CompactAndReplaceContext(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		v511CheckClean(t, db)
		v511CompareModelNamed(t, db, m, "cycle-hash", "cycle-zset")
	}
}

func FuzzV511RandomOperationStream(f *testing.F) {
	f.Add([]byte("hash,zset,compact"))
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9})
	f.Add([]byte{0xff, 0x00, 0x7f, 0x80, 0x01})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		path := filepath.Join(t.TempDir(), "fuzz.db")
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		m := newV511Model()
		limit := len(data)
		if limit > 300 {
			limit = 300
		}
		for i := 0; i < limit; i++ {
			v511RunOperation(t, db, m, data[i], byte(i), data[(i+1)%len(data)])
			if i%31 == 0 {
				v511CheckClean(t, db)
			}
		}
		v511CompareModel(t, db, m)
		v511CheckClean(t, db)

		// Keep the fuzz target bounded and deterministic even if a future
		// mutation introduces an unexpectedly large artifact.
		if info, err := os.Stat(path); err == nil && info.Size() > 64<<20 {
			t.Fatalf("unexpected database growth: %d bytes", info.Size())
		}
	})
}
