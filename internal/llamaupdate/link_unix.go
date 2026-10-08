//go:build !windows

package llamaupdate

import (
	"os"
	"path/filepath"
)

// replaceLink makes link point at target, replacing the old link in one
// step. A relative link keeps working if the whole folder moves.
func replaceLink(link, target string) error {
	tmp := link + ".next"
	os.Remove(tmp)
	if err := os.Symlink(filepath.Base(target), tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
