// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package moby

import (
	"context"
	"os"
	"time"

	detect "github.com/siemens/turtlefinder/detector"

	"github.com/ory/dockertest/v3"
	"github.com/thediveo/go-plugger/v3"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/thediveo/fdooze"
	. "github.com/thediveo/success"
)

// testWorkloadName specifies the name of a Docker container test workload, so
// we're sure that there is a well-defined container to be found.
const testWorkloadName = "turtles-docker-watch-workload"

const timeout = NodeTimeout(30 * time.Second)

const goroutinesUnwindTimeout = 2 * time.Second
const goroutinesUnwindPolling = 250 * time.Millisecond

var _ = Describe("Docker detector", Ordered, func() {

	var pool *dockertest.Pool

	BeforeAll(timeout, func(_ context.Context) {
		if os.Getuid() != 0 {
			Skip("needs root")
		}

		By("cleaning up any leftovers")
		pool = Successful(dockertest.NewPool(""))
		_ = pool.RemoveContainerByName(testWorkloadName)

		By("creating a test workload")
		Expect(pool.RunWithOptions(&dockertest.RunOptions{
			Repository: "busybox",
			Tag:        "latest",
			Name:       testWorkloadName,
			Cmd:        []string{"/bin/sleep", "120s"},
		})).Error().NotTo(HaveOccurred(),
			"creating container %s", testWorkloadName)
		DeferCleanup(timeout, func(_ context.Context) {
			_ = pool.RemoveContainerByName(testWorkloadName)
		})
	})

	BeforeEach(timeout, func(_ context.Context) {
		goodfds := Filedescriptors()
		goodgos := Goroutines() // avoid other failed goroutine tests to spill over
		DeferCleanup(NodeTimeout(30*time.Second), func(_ context.Context) {
			Eventually(Goroutines).WithTimeout(goroutinesUnwindTimeout).WithPolling(goroutinesUnwindPolling).
				ShouldNot(HaveLeaked(goodgos))
			Expect(Filedescriptors()).NotTo(HaveLeakedFds(goodfds))
		})
	})

	It("registers correctly", func() {
		Expect(plugger.Group[detect.Detector]().Plugins()).To(
			ContainElement("dockerd"))
	})

	It("attempts to connect to the API unsuccessfully", NodeTimeout(30*time.Second), func(ctx context.Context) {
		d := &Detector{}
		Expect(d.NewWatchers(ctx, 0, []string{"/etc/rumpelpumpel"})).To(BeEmpty())
	})

	It("successfully connects to the API and watches", NodeTimeout(30*time.Second), func(ctx context.Context) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		d := &Detector{}
		ws := d.NewWatchers(ctx, 0, []string{"/etc/rumpelpumpel", "/run/docker/metrics.sock", "/run/docker.sock"})
		Expect(ws).To(HaveLen(1))
		w := ws[0]
		defer w.Close()
		go func() { // ...will be ended by cancelling the context
			_ = w.Watch(ctx)
		}()
		Eventually(w.Portfolio().Project("").ContainerNames,
			"5s", "250ms").Should(ContainElement(testWorkloadName))
	})

})
