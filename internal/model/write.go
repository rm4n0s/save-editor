package model

import (
	"context"
	"os"
	"path/filepath"

	"github.com/rm4n0s/errors"
)

// WriteFile writes data to path through a temporary file in the same folder, so a failure
// leaves the old file as it was. With backup, the old file is first copied to path + ".bak",
// unless that file exists already (it keeps the save as it was before the first edit).
func WriteFile(ctx context.Context, path string, data []byte, backup bool) error {
	if err := ctx.Err(); err != nil {
		return errors.NewErr(tagWriteFailed, err).SetMetadata("path", path)
	}
	if backup {
		old, err := os.ReadFile(path)
		switch {
		case err == nil:
			if _, statErr := os.Stat(path + ".bak"); os.IsNotExist(statErr) {
				if err := os.WriteFile(path+".bak", old, 0o644); err != nil {
					return errors.NewErr(tagBackupFailed, err).SetMetadata("path", path+".bak")
				}
			}
		case !os.IsNotExist(err):
			return errors.NewErr(tagBackupFailed, err).SetMetadata("path", path)
		}
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return errors.NewErr(tagWriteFailed, err).SetMetadata("path", path)
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name()) // the write failed already; the temporary file is only litter
		return errors.NewErr(tagWriteFailed, err).SetMetadata("path", path)
	}
	return nil
}

// The tags of the errors that this file makes.
const (
	tagBackupFailed = "FileBackupFailed"
	tagWriteFailed  = "FileWriteFailed"
)
