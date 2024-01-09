// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/whalewatcher/watcher"

	"github.com/siemens/turtlefinder/internal/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/thediveo/success"
)

const sockactivatorSyncWait = 5 * time.Second

func clearCachedDetectorPlugins() {
	muDaemonDetectorPlugins.Lock()
	defer muDaemonDetectorPlugins.Unlock()
	demonDetectorPlugins = nil
}

var _ = Describe("socket activator", Serial, Ordered, func() {

	BeforeAll(dockerEngineFinderOnly)

	BeforeEach(test.LogToGinkgo)

	BeforeEach(clearCachedDetectorPlugins)

	BeforeEach(func() {
		goodgos := Goroutines() // avoid other failed goroutine tests to spill over
		DeferCleanup(func() {
			Eventually(Goroutines).WithTimeout(goroutinesUnwindTimeout).WithPolling(goroutinesUnwindPolling).
				ShouldNot(HaveLeaked(goodgos))
		})
	})

	It("discovers new API paths", func(ctx context.Context) {
		if os.Getuid() != 0 {
			Skip("needs root")
		}

		By("using PID 1 systemd as an already-present socket activator")
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		s := newSocketActivator(
			&model.Process{PID: 1},
			sockactivatorSyncWait,
			func() context.Context { return ctx },
			nil,
		)

		By("discovering potential API paths")
		rawsox, hash := Successful2R(s.rawSocketFdsWithHash())
		Expect(hash).NotTo(BeZero())
		newapis := s.discoverAPIPaths(rawsox, hash)
		Expect(s.hash).To(Equal(hash))
		Expect(newapis).To(ContainElement("/run/docker.sock"))

		Expect(s.discoverAPIPaths(rawsox, hash)).To(BeNil(), "unexpected/invalid state change")

		By("spinning off a Docker watcher and waiting for it to become ready")
		var wg sync.WaitGroup
		wch := make(chan watcher.Watcher, 1)
		s.activateAndWatch(newapis, &wg, func(w watcher.Watcher, err error) {
			defer GinkgoRecover()
			defer close(wch)
			Expect(err).NotTo(HaveOccurred())
			Expect(w).NotTo(BeNil())
			wch <- w
		})
		done := make(chan struct{})
		go func() {
			defer close(done)
			wg.Wait()
		}()
		Eventually(done).Within(sockactivatorSyncWait).ProbeEvery(100 * time.Millisecond).
			Should(BeClosed())
		var w watcher.Watcher
		Eventually(wch).Should(Receive(&w), "no watcher created")
		Expect(w).NotTo(BeNil(), "no watcher created")
		Eventually(w.Ready).Within(2 * time.Second).ProbeEvery(250 * time.Millisecond).
			Should(BeClosed())

		By("winding things down")
		w.Close()
	})

	It("discovers and watches socket-activatable engines", func(ctx context.Context) {
		if os.Getuid() != 0 {
			Skip("needs root")
		}

		By("using PID 1 systemd as an already-present socket activator")
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		wch := make(chan watcher.Watcher, 1)
		s := newSocketActivator(
			&model.Process{PID: 1},
			sockactivatorSyncWait,
			func() context.Context { return ctx },
			func(w watcher.Watcher, pid model.PIDType) {
				defer GinkgoRecover()
				defer close(wch)
				Expect(w).NotTo(BeNil())
				Expect(pid).NotTo(BeZero())
				wch <- w
			},
		)

		By("discovering and updating")
		var wg sync.WaitGroup
		s.update(&wg)
		done := make(chan struct{})
		go func() {
			defer close(done)
			wg.Wait()
		}()
		Eventually(done).Within(sockactivatorSyncWait).ProbeEvery(100 * time.Millisecond).
			Should(BeClosed())
		var w watcher.Watcher
		Eventually(wch).Should(Receive(&w), "no watcher created")
		Expect(w).NotTo(BeNil(), "no watcher created")
		Eventually(w.Ready).Within(2 * time.Second).ProbeEvery(250 * time.Millisecond).
			Should(BeClosed())

		By("winding things down")
		w.Close()
	})

})
