package udb

import "errors"

var (
	ErrWritePipelineClosed = errors.New("udb: write pipeline is closed")
)
