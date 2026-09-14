package udb

import (
	"bytes"
	"errors"
	"sort"
	"sync"

	bolt "go.etcd.io/bbolt"
)

// Snapshot is a point-in-time logical snapshot of UDB's public Hash/ZSet data.
//
// The snapshot is materialized into ordinary Go memory while one source
// bbolt read transaction is active. The source transaction is released before
// Snapshot returns. Consequently, a long-lived Snapshot can never block source
// database mmap growth, writes, compaction, or Close.
//
// This deliberately does not keep a second bbolt database open. Besides being
// simpler, that avoids making Snapshot correctness depend on bbolt's mmap and
// nested-transaction behavior. The trade-off is memory usage proportional to
// the logical data contained in the snapshot.
var errSnapshotNilCallback = errors.New("udb: snapshot callback is nil")

type Snapshot struct {
	db *DB

	mu     sync.RWMutex
	closed bool

	hashes map[string]map[string][]byte
	zsets  map[string]map[string]uint64
}

// Snapshot creates a point-in-time logical snapshot. Hash and ZSet data are
// copied while one managed read transaction is active, so all data belongs to
// the same database state. No source transaction survives this method.
func (db *DB) Snapshot() (*Snapshot, error) {
	if db == nil {
		return nil, ErrDatabaseClosed
	}

	s := &Snapshot{
		db:     db,
		hashes: make(map[string]map[string][]byte),
		zsets:  make(map[string]map[string]uint64),
	}

	if err := db.View(func(tx *Tx) error {
		return s.capture(tx)
	}); err != nil {
		return nil, err
	}

	if db.metrics != nil {
		db.metrics.snapOpen.Add(1)
		db.metrics.snapActive.Add(1)
	}
	return s, nil
}

func (s *Snapshot) capture(tx *Tx) error {
	if tx == nil || tx.inner == nil {
		return ErrDatabaseClosed
	}

	return tx.inner.ForEach(func(name []byte, top *bolt.Bucket) error {
		// UDB stores Hash/ZSet data in top-level buckets.
		// ForEach supplies the bucket directly as the second argument.
		if top == nil {
			return nil
		}

		if bytes.HasPrefix(name, hashPrefix) {
			logical := string(name[len(hashPrefix):])
			bucket := top
			m := make(map[string][]byte, bucket.Stats().KeyN)
			if err := bucket.ForEach(func(k, v []byte) error {
				if v == nil {
					return nil
				}
				m[string(k)] = cloneBytes(v)
				return nil
			}); err != nil {
				return err
			}
			s.hashes[logical] = m
			return nil
		}

		if bytes.HasPrefix(name, zetScorePrefix) {
			logical := string(name[len(zetScorePrefix):])
			bucket := top
			m := make(map[string]uint64, bucket.Stats().KeyN)
			if err := bucket.ForEach(func(k, v []byte) error {
				if v == nil {
					return nil
				}
				score, err := DecodeUint64(v)
				if err != nil {
					return err
				}
				m[string(k)] = score
				return nil
			}); err != nil {
				return err
			}
			s.zsets[logical] = m
		}
		return nil
	})
}

// Close releases the snapshot's in-memory data. Close is idempotent.
func (s *Snapshot) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.hashes = nil
	s.zsets = nil
	if s.db != nil && s.db.metrics != nil {
		s.db.metrics.snapClose.Add(1)
		s.db.metrics.snapActive.Add(-1)
	}
	return nil
}

func (s *Snapshot) readLocked() error {
	if s.closed {
		return ErrDatabaseClosed
	}
	return nil
}

func (s *Snapshot) HGet(name string, key []byte) *Reply {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.readLocked(); err != nil {
		return errorReply(err)
	}
	if err := validName(name); err != nil {
		return errorReply(err)
	}
	if err := validKey(key); err != nil {
		return errorReply(err)
	}
	bucket, ok := s.hashes[name]
	if !ok {
		return &Reply{State: bucketNotFound}
	}
	v, ok := bucket[string(key)]
	if !ok {
		return &Reply{State: keyNotFound}
	}
	return &Reply{State: replyOK, Data: []BS{cloneBytes(v)}}
}

func (s *Snapshot) HGetInt(name string, key []byte) (uint64, error) {
	r := s.HGet(name, key)
	if r.Err != nil {
		return 0, r.Err
	}
	if r.State == bucketNotFound {
		return 0, ErrBucketNotFound
	}
	if r.State == keyNotFound {
		return 0, ErrKeyNotFound
	}
	return DecodeUint64(r.Data[0])
}

func (s *Snapshot) ZGet(name string, key []byte) *Reply {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.readLocked(); err != nil {
		return errorReply(err)
	}
	if err := validName(name); err != nil {
		return errorReply(err)
	}
	if err := validKey(key); err != nil {
		return errorReply(err)
	}
	bucket, ok := s.zsets[name]
	if !ok {
		return &Reply{State: bucketNotFound}
	}
	score, ok := bucket[string(key)]
	if !ok {
		return &Reply{State: keyNotFound}
	}
	return &Reply{State: replyOK, Data: []BS{I2b(score)}}
}

func (s *Snapshot) ZScore(name string, key []byte) (uint64, error) {
	r := s.ZGet(name, key)
	if r.Err != nil {
		return 0, r.Err
	}
	if r.State == bucketNotFound {
		return 0, ErrBucketNotFound
	}
	if r.State == keyNotFound {
		return 0, ErrKeyNotFound
	}
	return DecodeUint64(r.Data[0])
}

type snapshotZEntry struct {
	member string
	score  uint64
}

func (s *Snapshot) zEntries(name string) ([]snapshotZEntry, bool) {
	bucket, ok := s.zsets[name]
	if !ok {
		return nil, false
	}
	entries := make([]snapshotZEntry, 0, len(bucket))
	for member, score := range bucket {
		entries = append(entries, snapshotZEntry{member: member, score: score})
	}
	return entries, true
}

func sortSnapshotZ(entries []snapshotZEntry, reverse bool) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].score != entries[j].score {
			if reverse {
				return entries[i].score > entries[j].score
			}
			return entries[i].score < entries[j].score
		}
		if reverse {
			return entries[i].member > entries[j].member
		}
		return entries[i].member < entries[j].member
	})
}

func (s *Snapshot) zscan(name string, keyStart, scoreStart []byte, limit int, reverse bool) *Reply {
	if err := validName(name); err != nil {
		return errorReply(err)
	}
	if err := validLimit(limit); err != nil {
		return errorReply(err)
	}
	if len(scoreStart) != 0 && len(scoreStart) != uint64EncodedLen {
		return errorReply(ErrInvalidScore)
	}

	entries, ok := s.zEntries(name)
	if !ok {
		return &Reply{State: bucketNotFound}
	}
	sortSnapshotZ(entries, reverse)

	startScore := uint64(0)
	hasScore := len(scoreStart) != 0
	if hasScore {
		var err error
		startScore, err = DecodeUint64(scoreStart)
		if err != nil {
			return errorReply(err)
		}
	}

	r := &Reply{State: replyNotFound, Data: make([]BS, 0, 2*limit)}
	for _, entry := range entries {
		member := []byte(entry.member)
		if !snapshotZAfterStart(entry.score, member, hasScore, startScore, keyStart, reverse) {
			continue
		}
		r.Data = append(r.Data, cloneBytes(member), I2b(entry.score))
		if len(r.Data)/2 >= limit {
			break
		}
	}
	if len(r.Data) > 0 {
		r.State = replyOK
	}
	return r
}

func snapshotZAfterStart(score uint64, member []byte, hasScore bool, startScore uint64, keyStart []byte, reverse bool) bool {
	if !hasScore {
		if reverse {
			// Match zseekReverse: with an empty boundary, start at the
			// greatest entry.
			if len(keyStart) == 0 {
				return true
			}
			startScore = ^uint64(0)
			return score < startScore || (score == startScore && bytes.Compare(member, keyStart) < 0)
		}
		// Match zseekForward: an empty scoreStart is the minimum score.
		startScore = 0
		return score > startScore || (score == startScore && bytes.Compare(member, keyStart) > 0)
	}
	if reverse {
		if score != startScore {
			return score < startScore
		}
		return bytes.Compare(member, keyStart) < 0
	}
	if score != startScore {
		return score > startScore
	}
	return bytes.Compare(member, keyStart) > 0
}

func (s *Snapshot) ZScan(name string, keyStart, scoreStart []byte, limit int) *Reply {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.readLocked(); err != nil {
		return errorReply(err)
	}
	return s.zscan(name, keyStart, scoreStart, limit, false)
}

func (s *Snapshot) ZRScan(name string, keyStart, scoreStart []byte, limit int) *Reply {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.readLocked(); err != nil {
		return errorReply(err)
	}
	return s.zscan(name, keyStart, scoreStart, limit, true)
}

func (s *Snapshot) zscanEach(name string, keyStart, scoreStart []byte, limit int, reverse bool, fn func(member []byte, score uint64) error) error {
	if fn == nil {
		return errSnapshotNilCallback
	}
	r := s.zscan(name, keyStart, scoreStart, limit, reverse)
	if r.Err != nil {
		return r.Err
	}
	for i := 0; i+1 < len(r.Data); i += 2 {
		score, err := DecodeUint64(r.Data[i+1])
		if err != nil {
			return err
		}
		if err := fn(r.Data[i], score); err != nil {
			return err
		}
	}
	return nil
}

func (s *Snapshot) ZScanEach(name string, keyStart, scoreStart []byte, limit int, fn func(member []byte, score uint64) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.readLocked(); err != nil {
		return err
	}
	return s.zscanEach(name, keyStart, scoreStart, limit, false, fn)
}

func (s *Snapshot) ZRScanEach(name string, keyStart, scoreStart []byte, limit int, fn func(member []byte, score uint64) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.readLocked(); err != nil {
		return err
	}
	return s.zscanEach(name, keyStart, scoreStart, limit, true, fn)
}
