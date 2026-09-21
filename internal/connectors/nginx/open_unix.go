//go:build unix

package nginx

import (
	"os"
	"syscall"
)

// openConfig opens a path the loader already resolved. O_NOFOLLOW refuses a
// symlink swapped in at the final component since then, and O_NONBLOCK keeps
// a swapped-in FIFO from blocking the scan until a writer appears.
func openConfig(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
