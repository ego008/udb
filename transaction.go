package udb

import "context"

// Atomic runs fn as one managed, atomic write transaction.
// It is an explicit semantic alias for Update and is useful when the caller
// wants to emphasize that several operations must commit or roll back together.
func (db *DB) Atomic(fn func(*Tx) error) error {
	return db.Update(fn)
}

// AtomicContext is the context-aware form of Atomic. The context is checked
// before admission; once bbolt has started the transaction, cancellation does
// not forcibly interrupt the callback because bbolt has no safe transaction
// cancellation primitive.
func (db *DB) AtomicContext(ctx context.Context, fn func(*Tx) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return db.Update(fn)
}

// ReadTransaction is an explicit semantic alias for View.
func (db *DB) ReadTransaction(fn func(*Tx) error) error {
	return db.View(fn)
}

// ReadTransactionContext is the context-aware form of ReadTransaction.
func (db *DB) ReadTransactionContext(ctx context.Context, fn func(*Tx) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return db.View(fn)
}
