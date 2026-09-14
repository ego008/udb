package udb

// ZEntry is one ZSet member and its score.
//
// It is intentionally separate from Entry because ZSet scores are uint64 and
// are stored using UDB's canonical fixed-width encoding.
type ZEntry struct {
	Member BS
	Score  uint64
}

func recordBatchMetrics(db *DB, items int) {
	if db == nil || db.metrics == nil || items <= 0 {
		return
	}
	db.metrics.batchCommits.Add(1)
	db.metrics.batchItems.Add(uint64(items))
}

// HSetBatch applies all entries in one managed write transaction.
func (db *DB) HSetBatch(name string, entries []Entry) error {
	if err := validName(name); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	ops := make([]writeOp, len(entries))
	for i := range entries {
		if err := validKey(entries[i].Key); err != nil {
			return err
		}
		ops[i] = writeOp{kind: writeHSet, name: name, key: entries[i].Key, value: entries[i].Value}
	}
	if err := db.Update(func(tx *Tx) error { return applyWriteOps(tx, ops) }); err != nil {
		return err
	}
	recordBatchMetrics(db, len(ops))
	return nil
}

// ZSetBatch applies all ZSet entries in one managed write transaction.
// ZSet's primary score map and secondary ordered index are updated atomically.
func (db *DB) ZSetBatch(name string, entries []ZEntry) error {
	if err := validName(name); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	ops := make([]writeOp, len(entries))
	for i := range entries {
		if err := validKey(entries[i].Member); err != nil {
			return err
		}
		ops[i] = writeOp{kind: writeZSet, name: name, key: entries[i].Member, score: entries[i].Score}
	}
	if err := db.Update(func(tx *Tx) error { return applyWriteOps(tx, ops) }); err != nil {
		return err
	}
	recordBatchMetrics(db, len(ops))
	return nil
}

// HDelBatch deletes multiple Hash keys in one managed write transaction.
func (db *DB) HDelBatch(name string, keys [][]byte) error {
	if err := validName(name); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	ops := make([]writeOp, len(keys))
	for i := range keys {
		if err := validKey(keys[i]); err != nil {
			return err
		}
		ops[i] = writeOp{kind: writeHDel, name: name, key: keys[i]}
	}
	if err := db.Update(func(tx *Tx) error { return applyWriteOps(tx, ops) }); err != nil {
		return err
	}
	recordBatchMetrics(db, len(ops))
	return nil
}

// ZDelBatch deletes multiple ZSet members in one managed write transaction.
func (db *DB) ZDelBatch(name string, keys [][]byte) error {
	if err := validName(name); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	ops := make([]writeOp, len(keys))
	for i := range keys {
		if err := validKey(keys[i]); err != nil {
			return err
		}
		ops[i] = writeOp{kind: writeZDel, name: name, key: keys[i]}
	}
	if err := db.Update(func(tx *Tx) error { return applyWriteOps(tx, ops) }); err != nil {
		return err
	}
	recordBatchMetrics(db, len(ops))
	return nil
}
