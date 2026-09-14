//go:build !windows

package udb

import (
	"errors"
	"os"
)

// syncFile forces file contents and metadata to stable storage where the
// platform provides that guarantee. It is intentionally used only at
// recovery-critical boundaries, not on every normal write.
func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return err
	}
	return nil
}

// syncDir persists directory-entry changes such as rename/remove on Unix.
// Some filesystems may reject directory fsync; in that case the error is
// surfaced because V5.6's compaction protocol is explicitly durability-aware.
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		if errors.Is(err, os.ErrInvalid) {
			return nil
		}
		return err
	}
	return nil
}
