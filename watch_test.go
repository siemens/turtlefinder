// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/thediveo/lxkns/model"
	engineclient "github.com/thediveo/whalewatcher/v2/engineclient/moby"
	"github.com/thediveo/whalewatcher/v2/watcher"
	"github.com/thediveo/whalewatcher/v2/watcher/moby"

	"github.com/siemens/turtlefinder/v2/internal/testslog"

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
			Eventually(Goroutines).Within(10 * time.Second).ProbeEvery(100 * time.Millisecond).
				ShouldNot(HaveLeaked(goodgos))
		})
	})

	var slogout fmt.Stringer

	BeforeEach(func() {
		DeferCleanup(slog.SetDefault, slog.Default())
		slogout = testslog.SetDefault(slog.LevelInfo, GinkgoWriter)
	})

	Context("connecting to a known engine process and starting a watch", func() {

		It("returns synchronized and properly winds down", func(ctx context.Context) {
			w := &countingWatcher{Watcher: Successful(moby.New("unix:///run/docker.sock", nil))}
			defer w.Close()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			_ = NewEngine(ctx, w, 0)
			start := time.Now()
			awaitSync(ctx, w, watchSyncMaxWait)
			Expect(time.Since(start)).To(BeNumerically("<", watchSyncMaxWait))
			Eventually(w.Ready).Should(BeClosed())
			Consistently(w.watches.Load).Within(time.Second).ProbeEvery(100*time.Millisecond).
				Should(Equal(int32(1)), "Watch() must be started exactly once")
			// nota bene: the "synchronized" log comes from another go routine, so
			// we need to wait for it.
			Eventually(slogout.String).Should(MatchRegexp(
				`"beginning synchronization with engine" type=docker\.com .*\n` +
					`.*"successfully synchronized with container engine" type=docker\.com .* ` +
					`id=(?:[A-Z0-9]{4}(?::[A-Z0-9]{4}){11})|(?:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`))
			cancel()
			Eventually(slogout.String).Should(MatchRegexp(
				`level=INFO msg="terminated engine workload watch" type=docker\.com`))
			Expect(slogout.String()).NotTo(MatchRegexp(
				`level=WARN msg="terminated engine workload watch"`))
		})

		It("returns early when the context gets cancelled", func(ctx context.Context) {
			w := Successful(moby.New("unix:///run/docker.sock", nil))
			defer w.Close()
			ctx, cancel := context.WithCancel(ctx)
			cancel() // sic!
			_ = NewEngine(ctx, w, 0)
			start := time.Now()
			awaitSync(ctx, w, watchSyncMaxWait)
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
			_ = NewEngine(ctx, w, 0)
			start := time.Now()
			awaitSync(ctx, w, watchSyncMaxWait)
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
			DeferCleanup(netunix.Close)
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
			Expect(scanner.Err()).NotTo(HaveOccurred())
			Expect(udsino).NotTo(BeZero())

			By("activating and watching")
			ch := make(chan watcher.Watcher, 1)
			activateAndStartWatch(ctx,
				"/run/docker.sock",
				udsino,
				1,
				"dockerd",
				func(apipath string, pid model.PIDType) (watcher.Watcher, error) {
					w, err := moby.New("unix://"+apipath, nil, engineclient.WithPID(int(pid)))
					if err != nil {
						return nil, err
					}
					return &countingWatcher{Watcher: w}, nil
				},
				func(ctx context.Context, nw watcher.Watcher, err error) {
					defer GinkgoRecover()
					Expect(err).NotTo(HaveOccurred())
					Expect(nw).NotTo(BeNil())
					_ = NewEngine(ctx, nw, 0)
					ch <- nw
				},
				watchSyncMaxWait)
			var w watcher.Watcher
			Expect(ch).To(Receive(&w), "watcher must be handed over before returning")
			defer w.Close()
			Expect(w.PID()).NotTo(BeZero())
			Expect(w.ID(ctx)).NotTo(BeEmpty())
			By("waiting for watcher to become synchronized")
			Eventually(w.Ready).Within(5 * time.Second).ProbeEvery(250 * time.Millisecond).
				Should(BeClosed())
			Consistently(AssignableTo[*countingWatcher](w).watches.Load).
				Within(time.Second).ProbeEvery(100*time.Millisecond).
				Should(Equal(int32(1)), "Watch() must be started exactly once")
			By("winding down")
			cancel()
			Eventually(slogout.String).Within(2 * time.Second).ProbeEvery(250 * time.Millisecond).
				Should(ContainSubstring("terminated engine workload watch"))
		})

	})

	Context("failing to activate and watch", func() {

		// activate runs activateAndStartWatch, returning the error passed to the
		// outcome callback.
		activate := func(
			ctx context.Context,
			apipath string,
			ino uint64,
			activatorPID model.PIDType,
			enginename string,
			creatorfn func(apipath string, pid model.PIDType) (watcher.Watcher, error),
			maxwait time.Duration,
		) error {
			outcome := make(chan error, 1)
			activateAndStartWatch(ctx, apipath, ino, activatorPID, enginename, creatorfn,
				func(_ context.Context, w watcher.Watcher, err error) {
					defer GinkgoRecover()
					Expect(w).To(BeNil())
					outcome <- err
				},
				maxwait)
			var err error
			Eventually(outcome).Within(5 * time.Second).Should(Receive(&err))
			return err
		}

		unexpectedCreator := func(string, model.PIDType) (watcher.Watcher, error) {
			defer GinkgoRecover()
			Fail("unexpected watcher creation")
			return nil, nil
		}

		ownName := func() string {
			return strings.TrimSpace(string(Successful(os.ReadFile("/proc/self/comm"))))
		}

		It("reports an unreachable API endpoint", func(ctx context.Context) {
			Expect(activate(ctx, "/nowhere/docker.sock", 0, 1, "dockerd",
				unexpectedCreator, time.Second)).To(HaveOccurred())
		})

		It("reports a missing engine process, also when cancelled while searching", func(ctx context.Context) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			time.AfterFunc(2*findPolling, cancel)
			Expect(activate(ctx, silentUDS(), 0, 1, "nonexisting-engine",
				unexpectedCreator, 2*time.Second)).
				To(MatchError(ContainSubstring("cannot find activated container engine process")))
		})

		It("reports failing watcher creation", func(ctx context.Context) {
			sockpath := silentUDS()
			ino := inoOfListeningUDS(sockpath)
			activatorPID := model.PIDType(os.Getppid())
			name := ownName()

			Expect(activate(ctx, sockpath, ino, activatorPID, name,
				func(_ string, pid model.PIDType) (watcher.Watcher, error) {
					defer GinkgoRecover()
					Expect(pid).To(Equal(model.PIDType(os.Getpid())))
					return nil, errors.New("no watcher for you")
				}, 2*time.Second)).To(MatchError("no watcher for you"))

			Expect(activate(ctx, sockpath, ino, activatorPID, name,
				func(string, model.PIDType) (watcher.Watcher, error) { return nil, nil },
				2*time.Second)).To(MatchError(ContainSubstring("no '" + name + "' watcher")))
		})

		It("doesn't wait endlessly for watcher creation", func(ctx context.Context) {
			sockpath := silentUDS()
			outcome := make(chan error, 1)
			start := time.Now()
			activateAndStartWatch(ctx, sockpath, inoOfListeningUDS(sockpath),
				model.PIDType(os.Getppid()), ownName(),
				func(string, model.PIDType) (watcher.Watcher, error) {
					time.Sleep(500 * time.Millisecond)
					return nil, errors.New("too late")
				},
				func(_ context.Context, _ watcher.Watcher, err error) { outcome <- err },
				100*time.Millisecond)
			Expect(time.Since(start)).To(BeNumerically("<", 400*time.Millisecond))
			Eventually(slogout.String).Should(ContainSubstring("engine endpoint still in activation"))
			Eventually(outcome).Within(2 * time.Second).Should(Receive(MatchError("too late")))
		})

	})

})
