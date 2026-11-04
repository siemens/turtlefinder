// (c) Siemens AG 2026
//
// SPDX-License-Identifier: MIT

package podman

import (
	"net"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/thediveo/success"
)

func TestActivatorPodman(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "turtlefinder/activator/podman")
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
