// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
	"os"
	"time"

	"github.com/thediveo/lxkns/discover"
	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/whalewatcher/watcher/containerd"
	"github.com/thediveo/whalewatcher/watcher/moby"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/thediveo/fdooze"
)

var _ = Describe("turtle finder", func() {

	BeforeEach(func() {
		goodfds := Filedescriptors()
		goodgos := Goroutines() // avoid other failed goroutine tests to spill over
		DeferCleanup(func() {
			Eventually(Goroutines).WithTimeout(2 * time.Second).WithPolling(250 * time.Millisecond).
				ShouldNot(HaveLeaked(goodgos))
			Expect(Filedescriptors()).NotTo(HaveLeakedFds(goodfds))
		})
	})

	// This is an ugly test with respect to goroutine leakage, as it runs a
	// discovery and then very quickly cancels the context, so watchers might
	// still be in their spin-up phase.
	It("finds docker and containerd", func(ctx context.Context) {
		if os.Getuid() != 0 {
			Skip("needs root")
		}

		ctx, cancel := context.WithCancel(ctx)
		tf := New(func() context.Context { return ctx })
		Expect(tf).NotTo(BeNil())
		defer cancel()
		defer tf.Close()

		// Ironically we should find containerd also when running this test on
		// Docker Desktop on WSL2, where the Docker daemon lives inside a
		// containerd container. In this case, we'll see another containerd
		// instance that is the Docker daemon's sidekick.
		Eventually(func() []*model.ContainerEngine {
			lxdisco := discover.Namespaces(discover.WithFullDiscovery())
			_ = tf.Containers(ctx, lxdisco.Processes, lxdisco.PIDMap)
			return tf.Engines()
		}).Within(10 * time.Second).ProbeEvery(250 * time.Millisecond).
			Should(ContainElements(
				HaveEngine(moby.Type, `^unix:///proc/\d+/root/run/docker.sock$`),
				HaveEngine(containerd.Type, `^unix:///proc/\d+/root/run/containerd/containerd.sock$`),
			))
	})

})
