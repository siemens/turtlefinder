// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/thediveo/lxkns/discover"
	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/lxkns/species"
	"github.com/thediveo/whalewatcher/engineclient/containerd/test/ctr"
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

		By("starting an additional container engine in a container")
		By("spinning up a Docker container with stand-alone containerd, courtesy of the KinD k8s sig")
		pool := Successful(dockertest.NewPool("unix:///run/docker.sock"))
		_ = pool.RemoveContainerByName(kindischName)
		// The necessary container start arguments come from KinD's Docker node
		// provisioner, see:
		// https://github.com/kubernetes-sigs/kind/blob/3610f606516ccaa88aa098465d8c13af70937050/pkg/cluster/internal/providers/docker/provision.go#L133
		//
		// Please note that --privileged already implies switching off AppArmor.
		//
		// Please note further, that currently some Docker client CLI flags
		// don't translate into dockertest-supported options.
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
		Expect(pool.Client.BuildImage(docker.BuildImageOptions{
			Name:       img.Name,
			ContextDir: "./detector/containerd/_test/kindisch", // sorry, couldn't resist the pun.
			Dockerfile: "Dockerfile",
			BuildArgs: []docker.BuildArg{
				{Name: "KINDEST_BASE_TAG", Value: test.KindestBaseImageTag},
			},
			OutputStream: io.Discard,
		})).To(Succeed())
		providerCntr := Successful(pool.RunWithOptions(
			&dockertest.RunOptions{
				Name:       kindischName,
				Repository: img.Name,
				Privileged: true,
				Mounts: []string{
					"/var", // well, this actually is an unnamed volume
					"/dev/mapper:/dev/mapper",
					"/lib/modules:/lib/modules:ro",
				},
				Tty: true,
			}, func(hc *docker.HostConfig) {
				hc.Init = false
				hc.Tmpfs = map[string]string{
					"/tmp": "",
					"/run": "",
				}
				hc.Devices = []docker.Device{
					{PathOnHost: "/dev/fuse"},
				}
			}))
		DeferCleanup(func() {
			By("removing the containerd Docker container")
			_ = pool.Purge(providerCntr)
		})

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
			))

		By("pulling a busybox image (if necessary)")
		ctr.Successfully(providerCntr,
			"-n", testNamespace,
			"image", "pull", testImageRef)

		By("creating a new container+task and starting it")
		ctr.Successfully(providerCntr,
			"-n", testNamespace,
			"run", "-d",
			testImageRef,
			testContainerName,
			"/bin/sleep", "30s")

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
		Expect(pool.Purge(providerCntr)).To(Succeed())

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
				HaveField("Type", cri.Type),
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
