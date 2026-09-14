package udb

import "errors"

var (
	ErrWritePipelineClosed = errors.New("udb: write pipeline is closed")
	ErrWritePipelineFull   = errors.New("udb: write pipeline queue is full")
)
