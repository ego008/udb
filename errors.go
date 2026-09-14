package udb

import "errors"

var (
	ErrNilTransaction     = errors.New("udb: nil transaction")
	ErrNilTransactionFunc = errors.New("udb: transaction function is nil")
	ErrDatabaseClosed     = errors.New("udb: database is closed")
	ErrMaintenanceBusy    = errors.New("udb: maintenance is already running")
	ErrMaintenanceStopped = errors.New("udb: maintenance manager is stopped")
)
