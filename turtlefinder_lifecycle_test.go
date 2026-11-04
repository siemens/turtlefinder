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

})
