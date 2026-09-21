//go:build !unix

package nginx

import "os"

// openConfig has no no-follow open to use here; read still requires the
// opened descriptor to be a regular file.
func openConfig(p string) (*os.File, error) { return os.Open(p) }
