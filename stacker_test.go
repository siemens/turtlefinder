// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/thediveo/lxkns/discover"
	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/lxkns/species"
	"github.com/thediveo/morbyd/v2"
	"github.com/thediveo/morbyd/v2/build"
	"github.com/thediveo/morbyd/v2/exec"
	"github.com/thediveo/morbyd/v2/run"
	"github.com/thediveo/morbyd/v2/session"
	"github.com/thediveo/morbyd/v2/timestamper"
	"github.com/thediveo/whalewatcher/v2/engineclient/cri"
	mobyengineclient "github.com/thediveo/whalewatcher/v2/engineclient/moby"
	"github.com/thediveo/whalewatcher/v2/test"
	"github.com/thediveo/whalewatcher/v2/watcher/containerd"
	"github.com/thediveo/whalewatcher/v2/watcher/moby"

	"github.com/siemens/turtlefinder/v2/internal/testimg"
	"github.com/siemens/turtlefinder/v2/internal/testslog"
	. "github.com/siemens/turtlefinder/v2/matcher"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/thediveo/fdooze"
	. "github.com/thediveo/success"
)

const (
	kindischName      = "turtlefinder-stacker"
	kindischImageName = "siemens/turtlefinder-kindisch-stacker"

	testNamespace     = "testing"
	testContainerName = "canary"
	testImageRef      = "docker.io/library/busybox:latest"
)

var _ = Describe("turtles and elephants", Serial, Ordered, func() {

	BeforeEach(func() {
		goodfds := Filedescriptors()
		goodgos := Goroutines() // avoid other failed goroutine tests to spill over
		DeferCleanup(func() {
			Eventually(Goroutines).WithTimeout(goroutinesUnwindTimeout).WithPolling(goroutinesUnwindPolling).
				ShouldNot(HaveLeaked(goodgos))
			Expect(Filedescriptors()).NotTo(HaveLeakedFds(goodfds))
		})
	})

	BeforeEach(func() {
		DeferCleanup(slog.SetDefault, slog.Default())
		_ = testslog.SetDefault(slog.LevelInfo, GinkgoWriter)
	})

	It("aborts stacking on duplicate engines", func() {
		slogout := testslog.SetDefault(slog.LevelInfo, GinkgoWriter)
		// PID beyond the kernel's PID_MAX_LIMIT, so it can never exist.
		const fakePID = 1<<22 + 42
		engines := []*Engine{}
		for range 2 {
			w := Successful(moby.New("unix:///run/docker.sock", nil, mobyengineclient.WithPID(fakePID)))
			DeferCleanup(w.Close)
			engines = append(engines, &Engine{Watcher: w})
		}
		stackEngines(nil, engines, model.ProcessTable{}, nil)
		Expect(slogout.String()).To(ContainSubstring("aborting turtlefinder engine stacking due to duplicate engine PID"))
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
		Eventually(ctx, func() []*model.ContainerEngine {
			_ = discover()
			engines = finder.Engines(ctx)
			return engines
		}).Within(10 * time.Second).ProbeEvery(250 * time.Millisecond).
			Should(ContainElements(
				HaveEngine(moby.Type, `^unix:///proc/\d+/root/(?:var/)?run/docker.sock$`),
				// In a Docker Desktop on WSL2 configuration, Docker runs inside a
				// containerd, and there's also Docker's containerd sidekick...
				HaveEngine(containerd.Type, `^unix:///proc/\d+/root/run/(?:docker/)?containerd/containerd.sock$`),
			))

		By("creating a new Docker session for testing")
		sess := Successful(morbyd.NewSession(ctx,
			session.WithAutoCleaning("test.turtlefinder=stacker")))
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
			build.WithTag(kindischImageName),
			build.WithBuildArg("KINDEST_BASE_TAG="+test.KindestBaseImageTag),
			build.WithOutput(timestamper.New(GinkgoWriter)))).
			Error().NotTo(HaveOccurred())
		providerCntr := Successful(sess.Run(ctx, kindischImageName,
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
			engines := slices.DeleteFunc(finder.Engines(ctx), isStackerTestEngineTyp)
			slices.SortFunc(engines, func(a, b *model.ContainerEngine) int {
				return strings.Compare(a.Type, b.Type)
			})
			return engines
		}).Within(10 * time.Second).ProbeEvery(250 * time.Millisecond).
			// running this inside a host with devcontainers and
			// dockers-in-docker and inside the host PID namespace will usually
			// turn up much more than we bargained for, so no exact match here.
			Should(ContainElements(
				HaveField("Type", containerd.Type),
				HaveField("Type", moby.Type),
				HaveField("Type", cri.Type),
			))

		By("loading a busybox image from the Docker engine into containerd")
		Expect(testimg.Load(ctx, providerCntr, testImageRef, timestamper.New(GinkgoWriter),
			"ctr", "-n", testNamespace, "image", "import", "-")).To(Succeed())

		By("creating a new container+task and starting it")
		ctr := Successful(providerCntr.Exec(ctx,
			exec.Command("ctr",
				"-n", testNamespace,
				"run", "-d",
				testImageRef,
				testContainerName,
				"/bin/sleep", "30s"),
			exec.WithCombinedOutput(timestamper.New(GinkgoWriter))))
		Expect(ctr.Wait(ctx)).To(BeZero())

		var canary *model.Container
		Eventually(func() model.Containers {
			return discover().Containers
		}).Within(10 * time.Second).ProbeEvery(250 * time.Millisecond).
			Should(ContainElement(
				SatisfyAll(
					HaveContainerNameID(testNamespace+"/"+testContainerName),
					WithTransform(
						func(actual *model.Container) model.Labels { return actual.Labels },
						HaveKeyWithValue(TurtlefinderContainerPrefixLabelName, kindischName))),
				&canary))
		stackedEnginePID := canary.Engine.PID
		Expect(stackedEnginePID).NotTo(BeZero())

		By("stopping the containerized container engine")
		providerCntr.Kill(ctx)

		By("waiting for the containerized containerd engine to vanish")
		// Engines of other concurrently running test suites may come and go,
		// so only check for our stacked engine (containerd and CRI) to vanish.
		Eventually(ctx, func() []*model.ContainerEngine {
			_ = discover()
			return slices.DeleteFunc(finder.Engines(ctx), isStackerTestEngineTyp)
		}).Within(10 * time.Second).ProbeEvery(250 * time.Millisecond).
			Should(SatisfyAll(
				Not(ContainElement(HaveField("PID", stackedEnginePID))),
				ContainElements(
					HaveField("Type", containerd.Type),
					HaveField("Type", moby.Type),
				),
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
