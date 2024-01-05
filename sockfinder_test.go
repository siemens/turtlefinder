// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"net"
	"os"

	"github.com/thediveo/lxkns/model"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/thediveo/fdooze"
	. "github.com/thediveo/success"
)

var _ = Describe("socket finder", func() {

	BeforeEach(func() {
		goodfds := Filedescriptors()
		goodgos := Goroutines() // avoid other failed goroutine tests to spill over
		DeferCleanup(func() {
			Eventually(Goroutines).WithTimeout(goroutinesUnwindTimeout).WithPolling(goroutinesUnwindPolling).
				ShouldNot(HaveLeaked(goodgos))
			Expect(Filedescriptors()).NotTo(HaveLeakedFds(goodfds))
		})
	})

	When("reading socket file descriptors of a process", func() {

		It("reports a non-existing PID", func() {
			Expect(rawSocketFdsOfProcess("", 0)).Error().To(MatchError(ContainSubstring(
				"cannot determine fds for process with PID 0, reason")))
		})

		It("reports when access is denied", func() {
			if os.Getegid() == 0 {
				Skip("must be run as non-root")
			}
			Expect(rawSocketFdsOfProcess("", 1)).Error().To(MatchError(ContainSubstring(
				"permission denied")))
		})

		It("only returns sockets, nothing else", func() {
			fakeproc := Successful(os.MkdirTemp("", "fakeproc-*"))
			defer os.RemoveAll(fakeproc)
			fakefds := fakeproc + "/proc/123456/fd"
			Expect(os.MkdirAll(fakefds, 0770)).To(Succeed())
			Expect(os.Symlink("/foobar", fakefds+"/1")).To(Succeed())
			Expect(os.Symlink("socket:[2345678]", fakefds+"/2")).To(Succeed())
			Expect(os.WriteFile(fakefds+"/3", []byte("foobar"), 0644)).To(Succeed())
			Expect(os.Symlink("socket:[", fakefds+"/666")).To(Succeed())

			Expect(rawSocketFdsOfProcess(fakeproc, 123456)).To(ConsistOf(
				rawSocketFd{fd: "2", socketino: "2345678"},
			))
		})

	})

	It("finds Docker API unix socket", func() {
		sox := listeningUDSVisibleToProcess(model.PIDType(os.Getpid()))
		Expect(sox).To(ContainElement("/run/docker.sock"))
	})

	It("finds listening canary unix socket", func() {
		fakesockdir := Successful(os.MkdirTemp("", "fakesock-*"))
		defer os.RemoveAll(fakesockdir)

		canarysockpath := fakesockdir + "/canary.sock"
		lsock := Successful(net.Listen("unix", canarysockpath))
		defer lsock.Close()

		soxpaths := listeningUDSPathsOfProcess(
			model.PIDType(os.Getpid()),
			listeningUDSVisibleToProcess(model.PIDType(os.Getpid())))
		Expect(soxpaths).To(ContainElement(canarysockpath))

		rawfds := Successful(rawSocketFdsOfProcess("", model.PIDType(os.Getpid())))
		lsox := listeningUDSPaths(rawfds, listeningUDSVisibleToProcess(model.PIDType(os.Getpid())))
		Expect(lsox).To(ContainElement(canarysockpath))
	})

})
