// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
	"time"

	"github.com/onsi/gomega/types"
	"github.com/thediveo/morbyd"
	"github.com/thediveo/morbyd/run"
	"github.com/thediveo/morbyd/session"
	"github.com/thediveo/morbyd/timestamper"
	"github.com/thediveo/whalewatcher/watcher/moby"

	"github.com/siemens/turtlefinder/internal/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/siemens/turtlefinder/matcher"
	. "github.com/thediveo/fdooze"
	. "github.com/thediveo/success"
)

// testEngineWorkloadName specifies the name of a Docker container test
// workload, so we're sure that there is a well-defined container to be found.
const testEngineWorkloadName = "turtles-testengine-workload"

func HaveEngine(typ string, apiregex string) types.GomegaMatcher {
	return And(
		HaveField("Type", typ),
		HaveField("API", MatchRegexp(apiregex)))
}

var _ = Describe("container engine", Serial, Ordered, func() {

	BeforeEach(test.LogToGinkgo)

	BeforeEach(func() {
		goodfds := Filedescriptors()
		goodgos := Goroutines() // avoid other failed goroutine tests to spill over
		DeferCleanup(func() {
			Eventually(Goroutines).WithTimeout(goroutinesUnwindTimeout).WithPolling(goroutinesUnwindPolling).
				ShouldNot(HaveLeaked(goodgos))
			Expect(Filedescriptors()).NotTo(HaveLeakedFds(goodfds))
		})
	})

	It("tracks an engine", NodeTimeout(30*time.Second), func(ctx context.Context) {
		w, err := moby.New("", nil)
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		engine := NewEngine(ctx, w, 0)
		Expect(engine.ID).NotTo(BeZero())

		Consistently(engine.IsAlive).Should(BeTrue())

		By("creating a new Docker session for testing")
		sess := Successful(morbyd.NewSession(ctx,
			session.WithAutoCleaning("test.turtlefinder=turtlefinder")))
		DeferCleanup(func(ctx context.Context) {
			By("auto-cleaning the session")
			sess.Close(ctx)
		})

		By("creating a canary container")
		_ = Successful(sess.Run(ctx, "busybox",
			run.WithName(testEngineWorkloadName),
			run.WithAutoRemove(),
			run.WithCommand("/bin/sh", "-c", "while true; do sleep 1; done"),
			run.WithCombinedOutput(timestamper.New(GinkgoWriter))))

		// Give leeway for the container workload discovery to reflect the
		// correct situation even under heavy system load. And remember to pass
		// a *function* to Eventually, not the result of a function *call* ;)
		Eventually(engine.Containers).WithContext(ctx).
			Within(10*time.Second).ProbeEvery(500*time.Millisecond).
			Should(ContainElement(HaveContainerNameID(testEngineWorkloadName)),
				"missing container %s", testEngineWorkloadName)

		cancel()
		Eventually(engine.IsAlive).Should(BeFalse())
	})

})
