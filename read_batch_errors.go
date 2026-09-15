package udb

import "errors"

var (
	errReadBatchClosed      = errors.New("udb: read batch is closed")
	errReadBatchEmpty       = errors.New("udb: read batch is empty")
	errReadBatchNilCallback = errors.New("udb: read batch callback is nil")
	errInvalidReadBatchOp   = errors.New("udb: invalid read batch operation")
)
