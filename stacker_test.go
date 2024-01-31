// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/thediveo/lxkns/discover"
	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/lxkns/species"
	"github.com/thediveo/morbyd"
	"github.com/thediveo/morbyd/build"
	"github.com/thediveo/morbyd/exec"
	"github.com/thediveo/morbyd/run"
	"github.com/thediveo/morbyd/session"
	"github.com/thediveo/morbyd/timestamper"
	"github.com/thediveo/whalewatcher/engineclient/cri"
	"github.com/thediveo/whalewatcher/engineclient/cri/test/img"
	"github.com/thediveo/whalewatcher/test"
	"github.com/thediveo/whalewatcher/watcher/containerd"
	"github.com/thediveo/whalewatcher/watcher/moby"
	"golang.org/x/exp/slices"

	testlog "github.com/siemens/turtlefinder/internal/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/siemens/turtlefinder/matcher"
	. "github.com/thediveo/fdooze"
	. "github.com/thediveo/success"
)

const (
	kindischName = "turtlefinder-containerd"

	testNamespace     = "testing"
	testContainerName = "canary"
	testImageRef      = "docker.io/library/busybox:latest"
)

var _ = Describe("turtles and elephants", Serial, Ordered, func() {

	BeforeEach(testlog.LogToGinkgo)

	BeforeEach(func() {
		goodfds := Filedescriptors()
		goodgos := Goroutines() // avoid other failed goroutine tests to spill over
		DeferCleanup(func() {
			Eventually(Goroutines).WithTimeout(goroutinesUnwindTimeout).WithPolling(goroutinesUnwindPolling).
				ShouldNot(HaveLeaked(goodgos))
			Expect(Filedescriptors()).NotTo(HaveLeakedFds(goodfds))
		})
	})

	It("prefixes and stacks turtles and elephants", NodeTimeout(60*time.Second), func(ctx context.Context) {
		if os.Getuid() != 0 {
			Skip("needs root")
		}

		By("creating a turtle finder")
		watcherctx, watchercancel := context.WithCancel(ctx)
		finder := New(func() context.Context { return watcherctx }, WithWorkers(1))
		Expect(finder).NotTo(BeNil())
		defer watchercancel()
		defer finder.Close()

		By("discovering engine base line")
		discover := func() *discover.Result {
			return discover.Namespaces(
				discover.FromProcs(),
				discover.FromBindmounts(),
				discover.WithNamespaceTypes(
					species.CLONE_NEWNET|species.CLONE_NEWPID|species.CLONE_NEWNS|species.CLONE_NEWUTS),
				discover.WithHierarchy(),
				discover.WithContainerizer(finder), // !!!
				discover.WithPIDMapper(),
			)
		}

		var engines []*model.ContainerEngine
		Eventually(func() []*model.ContainerEngine {
			_ = discover()
			engines = finder.Engines()
			return engines
		}).Within(10 * time.Second).ProbeEvery(250 * time.Millisecond).
			Should(ContainElements(
				HaveEngine(moby.Type, `^unix:///proc/\d+/root/run/docker.sock$`),
				// In a Docker Desktop on WSL2 configuration, Docker runs inside a
				// containerd, and there's also Docker's containerd sidekick...
				HaveEngine(containerd.Type, `^unix:///proc/\d+/root/run/containerd/containerd.sock$`),
			))

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
		Expect(sess.BuildImage(ctx, "./detector/containerd/_test/kindisch",
			build.WithTag(img.Name),
			build.WithBuildArg("KINDEST_BASE_TAG="+test.KindestBaseImageTag),
			build.WithOutput(timestamper.New(GinkgoWriter)))).
			Error().NotTo(HaveOccurred())
		providerCntr := Successful(sess.Run(ctx, img.Name,
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

		// This basically tests that scans correctly detect the newly
		// starting/started containerd process and start two watchers for it:
		// one covering containerd's native workload (sans Docker, sans k8s),
		// the other one covering the CRI k8s workload.
		By("waiting for turtle finder to catch up")
		Eventually(ctx, func() []*model.ContainerEngine {
			_ = discover()
			engines := slices.DeleteFunc(finder.Engines(), isStackerTestEngineTyp)
			slices.SortFunc(engines, func(a, b *model.ContainerEngine) int {
				return strings.Compare(a.Type, b.Type)
			})
			return engines
		}).Within(10 * time.Second).ProbeEvery(250 * time.Millisecond).
			Should(HaveExactElements(
				HaveField("Type", containerd.Type),
				HaveField("Type", containerd.Type),
				HaveField("Type", moby.Type),
				HaveField("Type", cri.Type),
			))

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

		Eventually(func() model.Containers {
			return discover().Containers
		}).Within(10 * time.Second).ProbeEvery(250 * time.Millisecond).
			Should(ContainElement(
				SatisfyAll(
					HaveContainerNameID(testNamespace+"/"+testContainerName),
					WithTransform(
						func(actual *model.Container) model.Labels { return actual.Labels },
						HaveKeyWithValue(TurtlefinderContainerPrefixLabelName, kindischName)))))

		By("stopping the containerized container engine")
		providerCntr.Kill(ctx)

		By("waiting for the containerized containerd engine to vanish")
		Eventually(ctx, func() []*model.ContainerEngine {
			_ = discover()
			engines := slices.DeleteFunc(finder.Engines(), isStackerTestEngineTyp)
			slices.SortFunc(engines, func(a, b *model.ContainerEngine) int {
				return strings.Compare(a.Type, b.Type)
			})
			return engines
		}).Within(10 * time.Second).ProbeEvery(250 * time.Millisecond).
			Should(HaveExactElements(
				HaveField("Type", containerd.Type), // ...only one left
				HaveField("Type", moby.Type),
			))
	})

})

// filter for the engine types guaranteed to be present; please note that we
// filter out podman here, in order to be independent of host podman
// installations. We cover podman explicitly in the overall turtlefinder
// test(s).
func isStackerTestEngineTyp(e *model.ContainerEngine) bool {
	switch e.Type {
	case containerd.Type, moby.Type, cri.Type:
		return false
	default:
		return true
	}
}
