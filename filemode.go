package git2

import (
	"fmt"
	"os"
	"strconv"
)

// FileMode is a git tree entry mode. It converts directly to go-git's
// filemode.FileMode.
type FileMode uint32

const (
	Empty      FileMode = 0
	Dir        FileMode = 0040000
	Regular    FileMode = 0100644
	Deprecated FileMode = 0100664
	Executable FileMode = 0100755
	Symlink    FileMode = 0120000
	Submodule  FileMode = 0160000
)

// modeTypeMask selects a mode's type bits, git's S_IFMT.
const modeTypeMask FileMode = 0o170000

// isDir reports whether m's type is a directory.
func (m FileMode) isDir() bool { return m&modeTypeMask == Dir }

// IsFile reports whether m is a regular file, executable or symlink.
func (m FileMode) IsFile() bool {
	return m == Regular || m == Deprecated || m == Executable || m == Symlink
}

// IsRegular reports whether m is Regular or Deprecated.
func (m FileMode) IsRegular() bool { return m == Regular || m == Deprecated }

// IsMalformed reports whether m is not a known mode.
func (m FileMode) IsMalformed() bool {
	switch m {
	case Dir, Regular, Deprecated, Executable, Symlink, Submodule:
		return false
	}
	return true
}

// ToOSFileMode converts m to the equivalent os.FileMode.
func (m FileMode) ToOSFileMode() (os.FileMode, error) {
	switch m {
	case Dir:
		return os.ModePerm | os.ModeDir, nil
	case Submodule:
		return os.ModePerm | os.ModeDir, nil
	case Regular:
		return 0644, nil
	case Deprecated:
		return 0644, nil
	case Executable:
		return 0755, nil
	case Symlink:
		return os.ModePerm | os.ModeSymlink, nil
	}
	return 0, fmt.Errorf("malformed mode (%s)", m)
}

func (m FileMode) String() string {
	s := strconv.FormatUint(uint64(m), 8)
	for len(s) < 7 {
		s = "0" + s
	}
	return s
}
