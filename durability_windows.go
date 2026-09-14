//go:build windows

package udb

import "os"

// Windows does not provide the same portable directory-fsync primitive as
// Unix. File Sync is still useful for the journal itself; rename durability
// is delegated to the filesystem/OS semantics.
func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func syncDir(path string) error { return nil }
