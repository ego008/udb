package udb

// HGetInt is the high-level typed Hash read. It uses one managed read
// transaction and returns no mmap-backed bytes.
func (db *DB) HGetInt(name string, key []byte) (uint64, error) {
	var value uint64
	err := db.ReadTransaction(func(tx *Tx) error {
		engine := newReadEngine(tx)
		var err2 error
		value, err2 = engine.HGetInt(name, key)
		return err2
	})
	return value, err
}

// ZScore is the high-level typed ZSet read.
func (db *DB) ZScore(name string, key []byte) (uint64, error) {
	var value uint64
	err := db.ReadTransaction(func(tx *Tx) error {
		engine := newReadEngine(tx)
		var err2 error
		value, err2 = engine.ZScore(name, key)
		return err2
	})
	return value, err
}

// HGetBatch reads several Hash keys in one managed read transaction.
func (db *DB) HGetBatch(name string, keys [][]byte) *Reply {
	var result *Reply
	if err := db.ReadTransaction(func(tx *Tx) error {
		engine := newReadEngine(tx)
		result = engine.HGetBatch(name, keys)
		return result.ErrOrNil()
	}); err != nil {
		return errorReply(err)
	}
	return result
}

// ZGetBatch reads several ZSet members in one managed read transaction.
func (db *DB) ZGetBatch(name string, keys [][]byte) *Reply {
	var result *Reply
	if err := db.ReadTransaction(func(tx *Tx) error {
		engine := newReadEngine(tx)
		result = engine.ZGetBatch(name, keys)
		return result.ErrOrNil()
	}); err != nil {
		return errorReply(err)
	}
	return result
}
