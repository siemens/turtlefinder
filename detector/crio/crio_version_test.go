// (c) Siemens AG 2026
//
// SPDX-License-Identifier: MIT

package crio

import (
	"context"
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/siemens/turtlefinder/v2/internal/testslog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/thediveo/success"
)

var _ = Describe("CRI-O version check", func() {

	var fake *fakeRuntimeService
	var sockpath string

	BeforeEach(func() {
		tmpdir := Successful(os.MkdirTemp("", "crio-version-test-"))
		DeferCleanup(os.RemoveAll, tmpdir)
		sockpath = tmpdir + "/crio.sock"
		l := Successful(net.Listen("unix", sockpath))
		fake = &fakeRuntimeService{}
		srv := grpc.NewServer()
		runtime.RegisterRuntimeServiceServer(srv, fake)
		go func() { _ = srv.Serve(l) }()
		DeferCleanup(srv.Stop)
	})

	It("accepts an endpoint reporting its version", func(ctx context.Context) {
		ws := (&Detector{}).NewWatchers(ctx, 0, []string{sockpath})
		Expect(ws).To(HaveLen(1))
		ws[0].Close()
	})

	It("rejects an endpoint failing to report its version", func(ctx context.Context) {
		DeferCleanup(slog.SetDefault, slog.Default())
		slogout := testslog.SetDefault(slog.LevelDebug, GinkgoWriter)
		fake.failVersion.Store(true)

		Expect((&Detector{}).NewWatchers(ctx, 0, []string{sockpath})).To(BeEmpty())
		Expect(slogout.String()).To(ContainSubstring("CRI-O API endpoint returned no version"))
	})

})
