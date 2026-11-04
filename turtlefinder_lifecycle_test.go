// (c) Siemens AG 2026
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"sync"

	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/nonstd/sets"
	"github.com/thediveo/nonstd/xslices"
	"github.com/thediveo/whalewatcher/v2/watcher/moby"

	"github.com/siemens/turtlefinder/v2/internal/testslog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/thediveo/success"
)

var _ = Describe("turtle finder engine lifecycle", Serial, func() {

	BeforeEach(func() {
		goodgos := Goroutines()
		DeferCleanup(func() {
			Eventually(Goroutines).WithTimeout(goroutinesUnwindTimeout).WithPolling(goroutinesUnwindPolling).
				ShouldNot(HaveLeaked(goodgos))
		})
	})

	BeforeEach(func() {
		DeferCleanup(slog.SetDefault, slog.Default())
		_ = testslog.SetDefault(slog.LevelInfo, GinkgoWriter)
	})

	It("closes engines registered after Close instead of panicking", func(ctx context.Context) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		tf := New(func() context.Context { return ctx })
		tf.Close()

		eng := NewEngine(ctx, Successful(moby.New("unix:///run/docker.sock", nil)), 0)
		Expect(tf.register(42, eng)).To(BeFalse())
		Expect(tf.Engines(ctx)).To(BeEmpty())
		Expect(tf.EnginesInclContainers(ctx, model.ProcessTable{}, nil)).To(BeEmpty())
	})

	It("keeps all engines registered for the same engine process", func(ctx context.Context) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		tf := New(func() context.Context { return ctx })
		defer tf.Close()

		Expect(tf.register(42, NewEngine(ctx, Successful(moby.New("unix:///run/docker.sock", nil)), 0))).To(BeTrue())
		Expect(tf.register(42, NewEngine(ctx, Successful(moby.New("unix:///run/docker.sock", nil)), 0))).To(BeTrue())
		Expect(tf.Engines(ctx)).To(HaveLen(2))
		Expect(tf.EngineCount()).To(Equal(2))
	})

	It("keeps failed engines registered but unused until their engine process has gone", func(ctx context.Context) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		tf := New(func() context.Context { return ctx })
		defer tf.Close()

		failedctx, failedcancel := context.WithCancel(ctx)
		failedcancel()
		eng := NewEngine(failedctx, Successful(moby.New("unix:///run/docker.sock", nil)), 0)
		Eventually(eng.IsAlive).Should(BeFalse())

		pid := model.PIDType(os.Getpid())
		procs := model.ProcessTable{pid: &model.Process{PID: pid}}
		Expect(tf.register(pid, eng)).To(BeTrue())
		Expect(tf.EnginesInclContainers(ctx, procs, nil)).To(BeEmpty())
		Expect(tf.Engines(ctx)).To(BeEmpty())
		tf.mux.Lock()
		Expect(tf.engines).To(HaveKey(pid))
		tf.mux.Unlock()

		tf.prune(model.ProcessTable{})
		tf.mux.Lock()
		defer tf.mux.Unlock()
		Expect(tf.engines).NotTo(HaveKey(pid))
	})

	It("doesn't watch the same engine twice when updating in parallel", func(ctx context.Context) {
		if os.Getuid() != 0 {
			Skip("needs root")
		}

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		tf := New(func() context.Context { return ctx })
		defer tf.Close()

		procs := model.NewProcessTable(false)
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() { tf.update(ctx, procs) })
		}
		wg.Wait()

		tf.mux.Lock()
		engines := maps.Clone(tf.engines)
		tf.mux.Unlock()
		Expect(engines).NotTo(BeEmpty())
		for pid, engs := range engines {
			types := xslices.Map(engs, func(e *Engine) string { return e.Type() })
			Expect(types).To(HaveLen(len(sets.New(types...).Elements())),
				"engine process %d watched more than once", pid)
		}
	})

	It("creates socket-activated engines outside the lock and bound to the watcher's context", func(ctx context.Context) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		tf := New(func() context.Context { return ctx })
		defer tf.Close()

		By("setting up a socket activator for a non-existing PID")
		const fakePID = model.PIDType(1<<22 + 42)
		fakeproc := &model.Process{PID: fakePID}
		fakeproc.Name = "systemd"
		var wg sync.WaitGroup
		tf.updateActivators(model.ProcessTable{fakePID: fakeproc}, &wg)
		wg.Wait()
		tf.mux.Lock()
		createdWatcherFn := tf.activators[fakePID].createdWatcherFn
		tf.mux.Unlock()

		By("reporting a newly created watcher while the turtle finder is locked")
		watcherctx, watchercancel := context.WithCancel(ctx)
		defer watchercancel()
		w := &idProbingWatcher{
			Watcher:  Successful(moby.New("unix:///run/docker.sock", nil)),
			idcalled: make(chan struct{}),
		}
		done := make(chan struct{})
		func() {
			tf.mux.Lock()
			defer tf.mux.Unlock()
			go func() {
				defer close(done)
				createdWatcherFn(watcherctx, w, 42)
			}()
			Eventually(w.idcalled).Should(BeClosed(), "engine creation blocked by turtle finder lock")
		}()
		Eventually(done).Should(BeClosed())
		Expect(tf.EngineCount()).To(Equal(1))

		By("cancelling the watcher's context")
		watchercancel()
		Eventually(tf.EngineCount).Should(BeZero(), "engine not bound to watcher's context")
	})

	It("returns the workloads of engines already consulted when the query gets cancelled", func(ctx context.Context) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		tf := New(func() context.Context { return ctx }, WithWorkers(1))
		defer tf.Close()

		entered := make(chan struct{}, 1)
		unblock := make(chan struct{})
		for range 2 {
			Expect(tf.register(42, NewEngine(ctx, &blockingPortfolioWatcher{
				Watcher: Successful(moby.New("unix:///run/docker.sock", nil)),
				entered: entered,
				unblock: unblock,
			}, 0))).To(BeTrue())
		}

		By("cancelling the query while the only worker is consulting the first engine")
		queryctx, querycancel := context.WithCancel(ctx)
		defer querycancel()
		result := make(chan []*model.ContainerEngine, 1)
		go func() { result <- tf.EnginesInclContainers(queryctx, model.ProcessTable{}, nil) }()
		Eventually(entered).Should(Receive())
		querycancel()
		close(unblock)
		Eventually(result).Should(Receive(HaveLen(1)))
	})

	It("returns no workloads when the query has already been cancelled", func(ctx context.Context) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		tf := New(func() context.Context { return ctx })
		defer tf.Close()

		Expect(tf.register(42, NewEngine(ctx, Successful(moby.New("unix:///run/docker.sock", nil)), 0))).To(BeTrue())
		queryctx, querycancel := context.WithCancel(ctx)
		querycancel()
		Expect(tf.EnginesInclContainers(queryctx, model.ProcessTable{}, nil)).To(BeEmpty())
	})

	It("skips engine processes without usable API endpoints", func(ctx context.Context) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		tf := New(func() context.Context { return ctx })
		defer tf.Close()
		slogout := testslog.SetDefault(slog.LevelDebug, GinkgoWriter)

		pid := model.PIDType(os.Getpid())
		fakeproc := &model.Process{PID: pid}
		fakeproc.Name = "dockerd"
		procs := model.ProcessTable{pid: fakeproc}

		By("not finding any API endpoint")
		tf.update(ctx, procs)
		Expect(slogout.String()).To(ContainSubstring("no API endpoint found"))
		Expect(tf.EngineCount()).To(BeZero())

		By("finding only an API endpoint that has been removed from the filesystem")
		sockpath := silentUDS()
		Expect(os.Remove(sockpath)).To(Succeed())
		tf.update(ctx, procs)
		Expect(slogout.String()).To(ContainSubstring("invalid API endpoint"))
		Expect(tf.EngineCount()).To(BeZero())
	})

})
