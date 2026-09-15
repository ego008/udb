package udb

// V5.30 focuses on reducing Go-side read-engine overhead without changing the
// transaction lifetime model. In particular, hot read paths construct the
// engine as a stack value instead of allocating a *ReadEngine on every View.
// The underlying bbolt read transaction remains short-lived.

// newReadEngine is the internal zero-allocation constructor used by hot paths.
// The returned value normally stays on the caller's stack for the duration of
// the managed View callback.
func newReadEngine(tx *Tx) ReadEngine {
	return ReadEngine{tx: tx}
}
