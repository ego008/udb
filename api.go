package udb

// High-level helpers hide the underlying bbolt transaction type.
func (db *DB) HSet(name string, key, value []byte) error {
	return db.Update(func(tx *Tx) error { return db.Hset(tx, name, key, value) })
}
func (db *DB) HGet(name string, key []byte) *Reply {
	var r *Reply
	if err := db.View(func(tx *Tx) error { r = db.Hget(tx, name, key); return r.ErrOrNil() }); err != nil {
		return errorReply(err)
	}
	return r
}
func (db *DB) HDel(name string, key []byte) error {
	return db.Update(func(tx *Tx) error { return db.Hdel(tx, name, key) })
}
func (db *DB) ZSet(name string, key []byte, score uint64) error {
	return db.Update(func(tx *Tx) error { return db.Zset(tx, name, key, score) })
}
func (db *DB) ZGet(name string, key []byte) *Reply {
	var r *Reply
	if err := db.View(func(tx *Tx) error { r = db.Zget(tx, name, key); return r.ErrOrNil() }); err != nil {
		return errorReply(err)
	}
	return r
}
func (db *DB) ZDel(name string, key []byte) error {
	return db.Update(func(tx *Tx) error { return db.Zdel(tx, name, key) })
}
