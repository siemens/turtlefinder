// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package containerd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/containerd/containerd"
	detect "github.com/siemens/turtlefinder/detector"
	"github.com/thediveo/go-plugger/v3"
	"github.com/thediveo/morbyd"
	"github.com/thediveo/morbyd/build"
	"github.com/thediveo/morbyd/exec"
	"github.com/thediveo/morbyd/run"
	"github.com/thediveo/morbyd/session"
	"github.com/thediveo/morbyd/timestamper"
	"github.com/thediveo/whalewatcher/engineclient/cri/test/img"
	"github.com/thediveo/whalewatcher/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/thediveo/fdooze"
	. "github.com/thediveo/success"
)

const (
	kindischName = "turtlefinder-containerd"

	testNamespace     = "testing"
	testContainerName = "canary"
	testImageRef      = "docker.io/library/busybox:latest"
)

var _ = Describe("containerd turtle watcher", Ordered, func() {

	var providerCntr *morbyd.Container

	BeforeAll(func(ctx context.Context) {
		if os.Getuid() != 0 {
			Skip("needs root")
		}

		goodfds := Filedescriptors()
		goodgos := Goroutines() // avoid other failed goroutine tests to spill over
		DeferCleanup(func() {
			Eventually(Goroutines).WithTimeout(goroutinesUnwindTimeout).WithPolling(goroutinesUnwindPolling).
				ShouldNot(HaveLeaked(goodgos))
			Expect(Filedescriptors()).NotTo(HaveLeakedFds(goodfds))
		})

		By("creating a new Docker session for testing")
		sess := Successful(morbyd.NewSession(ctx,
			session.WithAutoCleaning("test.turtlefinder=detector/containerd")))
		DeferCleanup(func(ctx context.Context) {
			By("auto-cleaning the session")
			sess.Close(ctx)
		})

		By("spinning up a Docker container with stand-alone containerd, courtesy of the KinD k8s sig")
		// The necessary container start arguments come from KinD's Docker node
		// provisioner, see:
		// https://github.com/kubernetes-sigs/kind/blob/3610f606516ccaa88aa098465d8c13af70937050/pkg/cluster/internal/providers/docker/provision.go#L133
		//
		// Please note that --privileged already implies switching off AppArmor.
		//
		// docker run -it --rm --name kindisch-...
		//   --privileged
		//   --cgroupns=private
		//   --init=false
		//   --volume /dev/mapper:/dev/mapper
		//   --device /dev/fuse
		//   --tmpfs /tmp
		//   --tmpfs /run
		//   --volume /var
		//   --volume /lib/modules:/lib/modules:ro
		//   kindisch-...
		Expect(sess.BuildImage(ctx, "./_test/kindisch",
			build.WithTag(img.Name),
			build.WithBuildArg("KINDEST_BASE_TAG="+test.KindestBaseImageTag),
			build.WithOutput(timestamper.New(GinkgoWriter)))).
			Error().NotTo(HaveOccurred())
		providerCntr = Successful(sess.Run(ctx, img.Name,
			run.WithName(kindischName),
			run.WithAutoRemove(),
			run.WithPrivileged(),
			run.WithSecurityOpt("label=disable"),
			run.WithCgroupnsMode("private"),
			run.WithVolume("/var"),
			run.WithVolume("/dev/mapper:/dev/mapper"),
			run.WithVolume("/lib/modules:/lib/modules:ro"),
			run.WithTmpfs("/tmp"),
			run.WithTmpfs("/run"),
			run.WithDevice("/dev/fuse"),
			run.WithCombinedOutput(timestamper.New(GinkgoWriter))))

		By("waiting for containerized containerd to become responsive")
		pid := Successful(providerCntr.PID(ctx))
		// apipath must not include absolute symbolic links, but already be
		// properly resolved.
		endpointPath := fmt.Sprintf("/proc/%d/root%s",
			pid, "/run/containerd/containerd.sock")
		var cdclient *containerd.Client
		Eventually(func() error {
			var err error
			cdclient, err = containerd.New(endpointPath,
				containerd.WithTimeout(5*time.Second))
			return err
		}).Within(30*time.Second).ProbeEvery(1*time.Second).
			Should(Succeed(), "containerd API never became responsive")
		cdclient.Close() // not needed anymore, will create fresh ones over and over again
	})

	It("registers correctly", func() {
		Expect(plugger.Group[detect.Detector]().Plugins()).To(
			ContainElement("containerd"))
	})

	It("tries unsuccessfully", NodeTimeout(30*time.Second), func(ctx context.Context) {
		d := &Detector{}
		Expect(d.NewWatchers(ctx, 0, []string{"/etc/rumpelpumpel"})).To(BeEmpty())
	})

	It("watches successfully", NodeTimeout(30*time.Second), func(ctx context.Context) {
		By("pulling a busybox image (if necessary)")
		ctr := Successful(providerCntr.Exec(ctx,
			exec.Command("ctr",
				"-n", testNamespace,
				"image", "pull", testImageRef),
			exec.WithCombinedOutput(timestamper.New(GinkgoWriter))))
		Expect(ctr.Wait(ctx)).To(BeZero())

		By("creating a new container+task and starting it")
		ctr = Successful(providerCntr.Exec(ctx,
			exec.Command("ctr",
				"-n", testNamespace,
				"run", "-d",
				testImageRef,
				testContainerName,
				"/bin/sleep", "30s"),
			exec.WithCombinedOutput(timestamper.New(GinkgoWriter))))
		Expect(ctr.Wait(ctx)).To(BeZero())

		DeferCleanup(func(ctx context.Context) {
			ctr := Successful(providerCntr.Exec(ctx,
				exec.Command("ctr",
					"-n", testNamespace,
					"task", "rm", "-f", testContainerName),
				exec.WithCombinedOutput(timestamper.New(GinkgoWriter))))
			_, _ = ctr.Wait(ctx)

			ctr = Successful(providerCntr.Exec(ctx,
				exec.Command("ctr",
					"-n", testNamespace,
					"container", "rm", testContainerName),
				exec.WithCombinedOutput(timestamper.New(GinkgoWriter))))
			_, _ = ctr.Wait(ctx)
		})

		By("running the detector on the API endpoints")
		d := &Detector{}
		wormhole := fmt.Sprintf("/proc/%d/root", Successful(providerCntr.PID(ctx)))
		ws := d.NewWatchers(ctx, 0, []string{
			wormhole + "/run/containerd/containerd.sock",
		})
		Expect(ws).To(HaveLen(2), "expected two watchers")
		for _, w := range ws {
			w := w
			defer w.Close()
			go func() { // ...will be ended by cancelling the context
				_ = w.Watch(ctx)
			}()
		}
		w := ws[0]
		Eventually(w.Portfolio().Project("").ContainerNames).Within(5 * time.Second).ProbeEvery(250 * time.Millisecond).
			Should(ContainElement(testNamespace + "/" + testContainerName))
	})

})
