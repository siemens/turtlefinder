// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/siemens/turtlefinder/internal/testslog"
	"github.com/thediveo/lxkns/model"
	engineclient "github.com/thediveo/whalewatcher/v2/engineclient/moby"
	"github.com/thediveo/whalewatcher/v2/watcher"
	"github.com/thediveo/whalewatcher/v2/watcher/moby"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/thediveo/success"
)

const watchSyncMaxWait = 5 * time.Second
const watchSlowSyncWait = watchSyncMaxWait + 2*time.Second

var _ = Describe("watch", Serial, func() {

	BeforeEach(func() {
		goodgos := Goroutines()
		DeferCleanup(func() {
			Eventually(Goroutines).Within(10 * time.Second).ProbeEvery(100 * time.Second).
				ShouldNot(HaveLeaked(goodgos))
		})
	})

	var slogout fmt.Stringer

	BeforeEach(func() {
		oldDefault := slog.Default()
		DeferCleanup(func() { slog.SetDefault(oldDefault) })
		slogout = testslog.SetDefault(slog.LevelInfo, GinkgoWriter)
	})

	Context("connecting to a known engine process and starting a watch", func() {

		It("returns synchronized and properly winds down", func(ctx context.Context) {
			w := Successful(moby.New("unix:///run/docker.sock", nil))
			defer w.Close()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			start := time.Now()
			startWatch(ctx, w, watchSyncMaxWait)
			Expect(time.Since(start)).To(BeNumerically("<", watchSyncMaxWait))
			Eventually(w.Ready).Should(BeClosed())
			// nota bene: the "synchronized" log comes from another go routine, so
			// we need to wait for it.
			Eventually(slogout.String).Should(MatchRegexp(
				`"beginning synchronization with engine" type=docker\.com .*\n` +
					`.*"successfully synchronized with container engine" type=docker\.com .* ` +
					`id=(?:[A-Z0-9]{4}(?::[A-Z0-9]{4}){11})|(?:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`))
			cancel()
			Eventually(slogout.String).Should(ContainSubstring(`"terminated engine workload watch"`))
		})

		It("returns early when the context gets cancelled", func(ctx context.Context) {
			w := Successful(moby.New("unix:///run/docker.sock", nil))
			defer w.Close()
			ctx, cancel := context.WithCancel(ctx)
			cancel() // sic!
			start := time.Now()
			startWatch(ctx, w, watchSyncMaxWait)
			Expect(time.Since(start)).To(BeNumerically("<", watchSyncMaxWait))
			Eventually(w.Ready).Should(BeClosed())
			Eventually(slogout.String).Within(2 * time.Second).ProbeEvery(250 * time.Millisecond).
				Should(MatchRegexp(
					`"terminated engine workload watch" type=docker\.com`))
		})

		It("doesn't wait endlessly for synchronization", func(ctx context.Context) {
			w := Successful(moby.New("unix:///run/docker.sock", nil))
			w = newSlowwatch(w, watchSlowSyncWait) // won't report ready before slowwait
			defer w.Close()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			start := time.Now()
			startWatch(ctx, w, watchSyncMaxWait)
			Expect(time.Since(start)).To(And(
				BeNumerically(">=", watchSyncMaxWait),
				BeNumerically("<", watchSlowSyncWait)))
			Eventually(slogout.String).Should(MatchRegexp(
				`"beginning synchronization with engine" type=docker\.com .*\n.*"container engine not yet synchronized.*" type=docker\.com`))
			Eventually(w.Ready).Within((watchSlowSyncWait - watchSyncMaxWait) * 2).Should(BeClosed())
			cancel()
			Eventually(slogout.String).Within(2 * time.Second).ProbeEvery(250 * time.Millisecond).
				Should(ContainSubstring("terminated engine workload watch"))
		})

	})

	Context("socket-activating a container engine process and watching it", func() {

		It("activates Docker first (sort of) and then watches", func(ctx context.Context) {
			if os.Getuid() != 0 {
				Skip("needs root")
			}

			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			By("finding the ino of the Docker API socket from /proc/self/net/unix")
			netunix := Successful(os.Open("/proc/self/net/unix"))
			defer netunix.Close()
			var udsino uint64
			scanner := bufio.NewScanner(netunix)
			for scanner.Scan() {
				line := scanner.Text()
				if !strings.HasSuffix(line, " /run/docker.sock") && !strings.HasSuffix(line, " /var/run/docker.sock") {
					continue
				}
				fields := strings.Fields(line)
				if fields[5] != "01" { // only take the listing UDS, not any other connected UDS
					continue
				}
				udsino = uint64(Successful(strconv.ParseUint(fields[6], 10, 64)))
				break
			}
			Expect(udsino).NotTo(BeZero())

			By("activating and watching")
			ch := make(chan watcher.Watcher, 1)
			activateAndStartWatch(ctx,
				"/run/docker.sock",
				udsino,
				1,
				"dockerd",
				func(apipath string, pid model.PIDType) (watcher.Watcher, error) {
					return moby.New("unix://"+apipath, nil, engineclient.WithPID(int(pid)))
				},
				func(nw watcher.Watcher, err error) {
					defer GinkgoRecover()
					Expect(err).NotTo(HaveOccurred())
					Expect(nw).NotTo(BeNil())
					ch <- nw
				},
				watchSyncMaxWait)
			var w watcher.Watcher
			Eventually(ch).Within(5 * time.Second).ProbeEvery(250 * time.Millisecond).
				Should(Receive(&w))
			defer w.Close()
			Expect(w.PID()).NotTo(BeZero())
			Expect(w.ID(ctx)).NotTo(BeEmpty())
			By("waiting for watcher to become synchronized")
			Eventually(w.Ready).Within(5 * time.Second).ProbeEvery(250 * time.Millisecond).
				Should(BeClosed())
			By("winding down")
			cancel()
			Eventually(slogout.String).Within(2 * time.Second).ProbeEvery(250 * time.Millisecond).
				Should(ContainSubstring("terminated engine workload watch"))
		})

	})

})
