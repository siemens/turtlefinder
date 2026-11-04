// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package containerd

import (
	"net"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/thediveo/success"
)

const goroutinesUnwindTimeout = 5 * time.Second
const goroutinesUnwindPolling = 250 * time.Millisecond

func TestDetectorContainerd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "turtlefinder/detector/containerd")
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
