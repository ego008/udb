package udb

import (
	"bytes"
	"fmt"
	"unsafe"

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
		if keyBucket == nil {
			return ZSetIntegrityReport{Name: name, Valid: true}, nil, nil
		}
		// A key-index bucket without its primary score bucket is corruption.
		// Count it with a sequential cursor rather than a Bucket.Stats call.
		count := 0
		c := keyBucket.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			count++
		}
		issues := make([]IntegrityIssue, 0, 1)
		if count > 0 {
			issues = append(issues, IntegrityIssue{
				Kind:   issueMissingScoreIndex,
				Name:   name,
				Detail: "key index bucket exists without score bucket",
			})
		}
		return ZSetIntegrityReport{
			Name:              name,
			KeyIndexEntries:   count,
			MissingScoreIndex: count,
			Valid:             count == 0,
		}, issues, nil
	}

	// V5.16 keeps the V5.15 sequential-scan design but removes the
	// transaction-scoped secondaryOrder string slice. We only need the map to
	// answer primary->secondary membership checks; orphan reporting can be done
	// with one final sequential scan of the secondary bucket. This avoids a
	// second O(N) string slice while preserving deterministic cursor order.
	//
	// The map value is only the secondary score. The score slice points into the
	// current read transaction and is never exposed outside this function.
	secondaryCap := 0
	if keyBucket != nil {
		// Stats is used only once to give the map a useful initial capacity. The
		// hot path still performs a sequential cursor scan; this avoids repeated
		// map growth without returning to per-entry B-tree searches.
		secondaryCap = keyBucket.Stats().KeyN
	}
	secondary := make(map[string][]byte, secondaryCap)
	zr := ZSetIntegrityReport{Name: name, Valid: true}
	issues := make([]IntegrityIssue, 0)

	if keyBucket != nil {
		kc := keyBucket.Cursor()
		for k, _ := kc.First(); k != nil; k, _ = kc.Next() {
			zr.KeyIndexEntries++
			if len(k) < uint64EncodedLen {
				zr.InvalidKeyIndex++
				zr.Valid = false
				issues = append(issues, IntegrityIssue{
					Kind:   issueInvalidKeyIndex,
					Name:   name,
					Key:    cloneBytes(k),
					Detail: fmt.Sprintf("index key length=%d", len(k)),
				})
				continue
			}
			member := k[uint64EncodedLen:]
			if len(member) == 0 {
				zr.InvalidKeyIndex++
				zr.Valid = false
				issues = append(issues, IntegrityIssue{
					Kind:   issueInvalidKeyIndex,
					Name:   name,
					Key:    cloneBytes(k),
					Detail: "empty member",
				})
				continue
			}
			memberKey := bytesToStringNoCopy(member)
			secondary[memberKey] = k[:uint64EncodedLen]
		}
	}

	// The primary score bucket is authoritative. Its cursor order is member
	// order, so all primary-side issue reporting remains deterministic.
	pc := scoreBucket.Cursor()
	for member, score := pc.First(); member != nil; member, score = pc.Next() {
		zr.ScoreEntries++
		memberKey := bytesToStringNoCopy(member)
		indexScore, hasSecondary := secondary[memberKey]
		if len(score) != uint64EncodedLen {
			zr.InvalidScoreValue++
			zr.Valid = false
			issues = append(issues, IntegrityIssue{
				Kind:   issueInvalidScore,
				Name:   name,
				Key:    cloneBytes(member),
				Detail: fmt.Sprintf("score length=%d", len(score)),
			})
			if hasSecondary {
				zr.MismatchedScore++
				zr.Valid = false
				issues = append(issues, IntegrityIssue{
					Kind:   issueMismatchedScore,
					Name:   name,
					Member: cloneBytes(member),
					Key:    joinScoreMember(indexScore, member),
					Detail: "secondary score differs from primary score",
				})
				delete(secondary, memberKey)
			}
			continue
		}
		if !hasSecondary {
			zr.MissingKeyIndex++
			zr.Valid = false
			issues = append(issues, IntegrityIssue{
				Kind:   issueMissingKeyIndex,
				Name:   name,
				Member: cloneBytes(member),
				Detail: "score entry has no secondary key index",
			})
			continue
		}
		if !bytes.Equal(indexScore, score) {
			// Historical behavior reports both the missing exact score||member
			// index and the mismatched secondary score.
			zr.MissingKeyIndex++
			zr.MismatchedScore++
			zr.Valid = false
			issues = append(issues,
				IntegrityIssue{Kind: issueMissingKeyIndex, Name: name, Member: cloneBytes(member), Detail: "score entry has no secondary key index"},
				IntegrityIssue{Kind: issueMismatchedScore, Name: name, Member: cloneBytes(member), Key: joinScoreMember(indexScore, member), Detail: "secondary score differs from primary score"},
			)
		}
		delete(secondary, memberKey)
	}

	// Remaining secondary entries are orphans. Scan the physical secondary
	// index again instead of retaining secondaryOrder. This keeps the report
	// deterministic while reducing one O(N) string allocation per entry.
	if keyBucket != nil && len(secondary) > 0 {
		kc := keyBucket.Cursor()
		for k, _ := kc.First(); k != nil; k, _ = kc.Next() {
			if len(k) < uint64EncodedLen {
				continue
			}
			member := k[uint64EncodedLen:]
			if len(member) == 0 {
				continue
			}
			memberKey := bytesToStringNoCopy(member)
			indexScore, ok := secondary[memberKey]
			if !ok {
				continue
			}
			zr.MissingScoreIndex++
			zr.Valid = false
			issues = append(issues, IntegrityIssue{
				Kind:   issueMissingScoreIndex,
				Name:   name,
				Member: cloneBytes(member),
				Key:    joinScoreMember(indexScore, member),
				Detail: "secondary index has no primary score entry",
			})
			delete(secondary, memberKey)
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

		// Rebuild the secondary index from the primary member->score map. The
		// two buckets are independent, so the score cursor remains valid while
		// the key-index bucket is cleared and repopulated. This avoids keeping
		// an O(N) temporary slice of copied score entries in memory.
		if err := clearBucket(tx, keyBucket); err != nil {
			return repaired, dropped, err
		}

		// Malformed primary scores require an explicit destructive option. We
		// cannot safely delete them while iterating the same bucket, so collect
		// only malformed member keys; valid entries are written directly.
		var invalidMembers [][]byte
		if opts.DropInvalidScores {
			invalidMembers = make([][]byte, 0)
		}
		c := scoreBucket.Cursor()
		for member, score := c.First(); member != nil; member, score = c.Next() {
			if len(score) != uint64EncodedLen {
				if !opts.DropInvalidScores {
					return repaired, dropped, fmt.Errorf("udb: cannot repair zset %q: invalid score for member %q", zr.Name, member)
				}
				invalidMembers = append(invalidMembers, cloneBytes(member))
				continue
			}
			if err := keyBucket.Put(joinScoreMember(score, member), nil); err != nil {
				return repaired, dropped, err
			}
			repaired++
		}
		for _, member := range invalidMembers {
			if err := scoreBucket.Delete(member); err != nil {
				return repaired, dropped, err
			}
			dropped++
		}
	}
	return repaired, dropped, nil
}

// bytesToStringNoCopy creates a string view over b without copying. It is
// intentionally used only inside a bbolt transaction where b points into the
// transaction-owned mmap and the resulting string never escapes the transaction.
// Keeping this helper private makes the lifetime contract local to integrity
// checking instead of exposing zero-copy strings through the public API.
func bytesToStringNoCopy(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}

func joinScoreMember(score, member []byte) []byte {
	out := make([]byte, uint64EncodedLen+len(member))
	copy(out, score)
	copy(out[uint64EncodedLen:], member)
	return out
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
