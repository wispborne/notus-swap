package llamaupdate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// replaceLink makes link a junction to target. Junctions are Windows' folder
// links. Unlike symbolic links, making one needs no admin rights or
// Developer Mode. A junction must name an absolute path.
//
// Windows can't rename a folder link over another, so the old link is
// removed first. Removing a junction leaves the folder it points at alone.
func replaceLink(link, target string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	tmp := link + ".next"
	os.Remove(tmp)
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", tmp, abs).CombinedOutput(); err != nil {
		return fmt.Errorf("making a junction: %s", strings.TrimSpace(string(out)))
	}
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, link)
}
