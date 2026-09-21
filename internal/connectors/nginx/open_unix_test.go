//go:build linux || darwin

package nginx

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These call read directly: they stand in for a file swapped between the
// loader's resolution and its open, which candidate would otherwise reject.
func swapLoader(t *testing.T) *loader {
	t.Helper()
	root := writeTree(t, map[string]string{"nginx.conf": "http {" + site("a.example.com") + "}", "a.conf": site("b.example.com")})
	l, _, err := loadWith(t, defaultLimits(), map[string]any{"path": root})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestReadDoesNotBlockOnFIFO(t *testing.T) {
	l := swapLoader(t)
	fifo := filepath.Join(l.entryRoot, "swapped.conf")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := l.read(fifo, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || err.Error() != "nginx: swapped.conf: file is not a regular file" {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read blocked on a FIFO")
	}
}

func TestReadRefusesSymlinkAtFinalPath(t *testing.T) {
	l := swapLoader(t)
	link := filepath.Join(l.entryRoot, "link.conf")
	symlink(t, "a.conf", link)
	_, err := l.read(link, &directive{file: "nginx.conf", line: 7})
	if err == nil || !strings.HasPrefix(err.Error(), "nginx: nginx.conf:7: cannot read link.conf: ") {
		t.Fatalf("got %v", err)
	}
}
