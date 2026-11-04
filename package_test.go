// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/whalewatcher/v2/watcher"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/thediveo/success"
)

const goroutinesUnwindTimeout = 2 * time.Second
const goroutinesUnwindPolling = 250 * time.Millisecond

func TestTurtlefinder(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "turtlefinder")
}

// countingWatcher wraps a watcher.Watcher and counts its Watch calls.
type countingWatcher struct {
	watcher.Watcher
	watches atomic.Int32
}

func (c *countingWatcher) Watch(ctx context.Context) error {
	c.watches.Add(1)
	return c.Watcher.Watch(ctx)
}

// silentUDS returns the path of a listening unix domain socket that never
// accepts connections, so that API clients hang until their context is done.
func silentUDS() string {
	tmpdir := Successful(os.MkdirTemp("", "turtlefinder-test-"))
	DeferCleanup(os.RemoveAll, tmpdir)
	path := tmpdir + "/silent.sock"
	l := Successful(net.Listen("unix", path))
	DeferCleanup(l.Close)
	return path
}

// inoOfListeningUDS returns the inode number of this process's listening unix
// domain socket with the specified path.
func inoOfListeningUDS(path string) uint64 {
	for ino, soxpath := range listeningUDSVisibleToProcess(model.PIDType(os.Getpid())) {
		if soxpath == path {
			return ino
		}
	}
	Fail("no listening unix domain socket " + path)
	return 0
}
