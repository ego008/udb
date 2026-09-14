package udb

import (
	"bytes"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// IntegrityReport is a logical consistency report for UDB data structures.
// Structural bbolt corruption is reported separately by Check().
type IntegrityReport struct {
	Consistent bool
	Hashes     []HashIntegrityReport
	ZSets      []ZSetIntegrityReport
	Issues     []IntegrityIssue
}

// HashIntegrityReport describes one hash bucket. Hashes have no secondary
// index, so integrity checking currently verifies bucket accessibility/count.
type HashIntegrityReport struct {
	Name  string
	Keys  int
	Valid bool
}

// ZSetIntegrityReport verifies the two physical indexes that make up a ZSet.
type ZSetIntegrityReport struct {
	Name              string
	ScoreEntries      int
	KeyIndexEntries   int
	MissingKeyIndex   int
	MissingScoreIndex int
	InvalidScoreValue int
	InvalidKeyIndex   int
	MismatchedScore   int
	Valid             bool
}

// IntegrityIssue identifies one logical inconsistency. Key and Member are
// copied so the report remains valid after the transaction closes.
type IntegrityIssue struct {
	Kind   string
	Name   string
	Key    BS
	Member BS
	Detail string
}

const (
	issueMissingKeyIndex   = "missing_key_index"
	issueMissingScoreIndex = "missing_score_index"
	issueInvalidScore      = "invalid_score"
	issueInvalidKeyIndex   = "invalid_key_index"
	issueMismatchedScore   = "mismatched_score"
)

// RepairOptions controls logical repair behavior.
type RepairOptions struct {
	// DropInvalidScores allows RepairIntegrity to delete ZSet members whose
	// primary score value is malformed. It defaults to false because deleting
	// application data should be an explicit decision.
	DropInvalidScores bool
}

// RepairReport describes changes made by RepairIntegrity.
type RepairReport struct {
	Before     IntegrityReport
	After      IntegrityReport
	Repaired   int
	Dropped    int
	Consistent bool
}

// CheckIntegrity performs a read-only UDB logical consistency check. It does
// not modify data. Structural bbolt corruption is returned as an error; logical
// inconsistencies are represented in the report with Consistent=false.
func (db *DB) CheckIntegrity() (IntegrityReport, error) {
	if db == nil {
		return IntegrityReport{}, ErrDatabaseClosed
	}
	var report IntegrityReport
	err := db.View(func(tx *Tx) error {
		var err error
		report, err = checkIntegrityTx(tx)
		return err
	})
	return report, err
}

func checkIntegrityBoltDB(d *bolt.DB) error {
	if d == nil {
		return ErrDatabaseClosed
	}
	return d.View(func(tx *bolt.Tx) error {
		report, err := checkIntegrityTx(newTx(tx))
		if err != nil {
			return err
		}
		if !report.Consistent {
			return fmt.Errorf("logical inconsistency: %d issue(s)", len(report.Issues))
		}
		return nil
	})
}

func checkIntegrityBoltFile(path string, opts *bolt.Options) error {
	o := *opts
	o.ReadOnly = true
	d, err := bolt.Open(path, 0600, &o)
	if err != nil {
		return err
	}
	defer d.Close()
	return checkIntegrityBoltDB(d)
}

// RepairIntegrity repairs missing, orphaned, and mismatched ZSet secondary
// indexes in one write transaction. Malformed primary score values are not
// deleted unless DropInvalidScores is explicitly enabled.
func (db *DB) RepairIntegrity(opts RepairOptions) (RepairReport, error) {
	if db == nil {
		return RepairReport{}, ErrDatabaseClosed
	}
	var out RepairReport
	err := db.Update(func(tx *Tx) error {
		before, err := checkIntegrityTx(tx)
		if err != nil {
			return err
		}
		out.Before = before
		if before.Consistent {
			out.After = before
			out.Consistent = true
			return nil
		}
		repaired, dropped, err := repairIntegrityTx(tx, before, opts)
		if err != nil {
			return err
		}
		after, err := checkIntegrityTx(tx)
		if err != nil {
			return err
		}
		out.Repaired = repaired
		out.Dropped = dropped
		out.After = after
		out.Consistent = after.Consistent
		return nil
	})
	return out, err
}

func checkIntegrityTx(tx *Tx) (IntegrityReport, error) {
	if err := validTx(tx); err != nil {
		return IntegrityReport{}, err
	}
	report := IntegrityReport{Consistent: true, Hashes: make([]HashIntegrityReport, 0), ZSets: make([]ZSetIntegrityReport, 0), Issues: make([]IntegrityIssue, 0)}

	seenZSets := make(map[string]struct{})
	if err := tx.inner.ForEach(func(name []byte, bucket *bolt.Bucket) error {
		if bucket == nil {
			return nil // UDB uses top-level buckets; ignore malformed external keys.
		}
		switch {
		case bytes.HasPrefix(name, hashPrefix):
			hr := HashIntegrityReport{Name: string(name[len(hashPrefix):]), Keys: bucket.Stats().KeyN, Valid: true}
			report.Hashes = append(report.Hashes, hr)
		case bytes.HasPrefix(name, zetScorePrefix):
			zname := string(name[len(zetScorePrefix):])
			seenZSets[zname] = struct{}{}
			zr, issues, err := checkZSetIntegrity(tx, zname)
			if err != nil {
				return err
			}
			report.ZSets = append(report.ZSets, zr)
			report.Issues = append(report.Issues, issues...)
		}
		return nil
	}); err != nil {
		return IntegrityReport{}, err
	}
	// A key-index bucket without its score bucket is itself corruption. Scan
	// these buckets after the primary pass so we also detect a completely
	// missing ZSet primary index.
	if err := tx.inner.ForEach(func(name []byte, bucket *bolt.Bucket) error {
		if bucket == nil || !bytes.HasPrefix(name, zetKeyPrefix) {
			return nil
		}
		zname := string(name[len(zetKeyPrefix):])
		if _, ok := seenZSets[zname]; ok {
			return nil
		}
		entries := bucket.Stats().KeyN
		report.ZSets = append(report.ZSets, ZSetIntegrityReport{Name: zname, KeyIndexEntries: entries, MissingScoreIndex: entries, Valid: entries == 0})
		if entries > 0 {
			report.Issues = append(report.Issues, IntegrityIssue{Kind: issueMissingScoreIndex, Name: zname, Detail: "key index bucket exists without score bucket"})
		}
		return nil
	}); err != nil {
		return IntegrityReport{}, err
	}
	report.Consistent = len(report.Issues) == 0
	return report, nil
}

func checkZSetIntegrity(tx *Tx, name string) (ZSetIntegrityReport, []IntegrityIssue, error) {
	keyBucket := tx.bucket(bucketName(zetKeyPrefix, name))
	scoreBucket := tx.bucket(bucketName(zetScorePrefix, name))
	if scoreBucket == nil {
		return ZSetIntegrityReport{Name: name, Valid: keyBucket == nil}, nil, nil
	}
	zr := ZSetIntegrityReport{Name: name, ScoreEntries: scoreBucket.Stats().KeyN, Valid: true}
	issues := make([]IntegrityIssue, 0)

	c := scoreBucket.Cursor()
	for k, score := c.First(); k != nil; k, score = c.Next() {
		if len(score) != uint64EncodedLen {
			zr.InvalidScoreValue++
			zr.Valid = false
			issues = append(issues, IntegrityIssue{Kind: issueInvalidScore, Name: name, Key: cloneBytes(k), Detail: fmt.Sprintf("score length=%d", len(score))})
			continue
		}
		idx := Bconcat(score, k)
		if keyBucket == nil || !bucketHasKey(keyBucket, idx) {
			zr.MissingKeyIndex++
			zr.Valid = false
			issues = append(issues, IntegrityIssue{Kind: issueMissingKeyIndex, Name: name, Member: cloneBytes(k), Detail: "score entry has no secondary key index"})
			continue
		}
	}

	if keyBucket != nil {
		zr.KeyIndexEntries = keyBucket.Stats().KeyN
		c = keyBucket.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			if len(k) < uint64EncodedLen {
				zr.InvalidKeyIndex++
				zr.Valid = false
				issues = append(issues, IntegrityIssue{Kind: issueInvalidKeyIndex, Name: name, Key: cloneBytes(k), Detail: fmt.Sprintf("index key length=%d", len(k))})
				continue
			}
			score := k[:uint64EncodedLen]
			member := k[uint64EncodedLen:]
			if len(member) == 0 {
				zr.InvalidKeyIndex++
				zr.Valid = false
				issues = append(issues, IntegrityIssue{Kind: issueInvalidKeyIndex, Name: name, Key: cloneBytes(k), Detail: "empty member"})
				continue
			}
			stored := scoreBucket.Get(member)
			if stored == nil {
				zr.MissingScoreIndex++
				zr.Valid = false
				issues = append(issues, IntegrityIssue{Kind: issueMissingScoreIndex, Name: name, Member: cloneBytes(member), Key: cloneBytes(k), Detail: "secondary index has no primary score entry"})
				continue
			}
			if !bytes.Equal(stored, score) {
				zr.MismatchedScore++
				zr.Valid = false
				issues = append(issues, IntegrityIssue{Kind: issueMismatchedScore, Name: name, Member: cloneBytes(member), Key: cloneBytes(k), Detail: "secondary score differs from primary score"})
			}
		}
	}
	return zr, issues, nil
}

func repairIntegrityTx(tx *Tx, before IntegrityReport, opts RepairOptions) (int, int, error) {
	repaired, dropped := 0, 0
	for _, zr := range before.ZSets {
		if zr.Valid {
			continue
		}
		keyBucket, scoreBucket, err := ensureZSetBucketsForRepair(tx, zr.Name)
		if err != nil {
			return repaired, dropped, err
		}

		// Read the primary score map completely before changing either bucket.
		// This avoids cursor invalidation and makes repair deterministic even
		// when malformed values are present.
		type scoreEntry struct {
			member BS
			score  BS
		}
		validEntries := make([]scoreEntry, 0, scoreBucket.Stats().KeyN)
		invalidMembers := make([]BS, 0)
		c := scoreBucket.Cursor()
		for member, score := c.First(); member != nil; member, score = c.Next() {
			if len(score) != uint64EncodedLen {
				if !opts.DropInvalidScores {
					return repaired, dropped, fmt.Errorf("udb: cannot repair zset %q: invalid score for member %q", zr.Name, member)
				}
				invalidMembers = append(invalidMembers, cloneBytes(member))
				continue
			}
			validEntries = append(validEntries, scoreEntry{member: cloneBytes(member), score: cloneBytes(score)})
		}

		if err := clearBucket(tx, keyBucket); err != nil {
			return repaired, dropped, err
		}
		for _, member := range invalidMembers {
			if err := scoreBucket.Delete(member); err != nil {
				return repaired, dropped, err
			}
			dropped++
		}
		for _, entry := range validEntries {
			if err := keyBucket.Put(Bconcat(entry.score, entry.member), nil); err != nil {
				return repaired, dropped, err
			}
			repaired++
		}
	}
	return repaired, dropped, nil
}

func ensureZSetBucketsForRepair(tx *Tx, name string) (*bolt.Bucket, *bolt.Bucket, error) {
	keyBucket, err := getOrCreateBucket(tx, bucketName(zetKeyPrefix, name))
	if err != nil {
		return nil, nil, err
	}
	scoreBucket, err := getOrCreateBucket(tx, bucketName(zetScorePrefix, name))
	if err != nil {
		return nil, nil, err
	}
	return keyBucket, scoreBucket, nil
}

func clearBucket(tx *Tx, b *bolt.Bucket) error {
	if b == nil {
		return nil
	}
	keys := make([][]byte, 0, b.Stats().KeyN)
	c := b.Cursor()
	for k, _ := c.First(); k != nil; k, _ = c.Next() {
		keys = append(keys, cloneBytes(k))
	}
	for _, k := range keys {
		if err := b.Delete(k); err != nil {
			return err
		}
	}
	return nil
}
