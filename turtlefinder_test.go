// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/siemens/turtlefinder/matcher"
	"github.com/thediveo/lxkns/discover"
	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/whalewatcher/watcher/containerd"
	"github.com/thediveo/whalewatcher/watcher/moby"

	"github.com/siemens/turtlefinder/activator/podman"
	"github.com/siemens/turtlefinder/internal/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/thediveo/fdooze"
	. "github.com/thediveo/success"
)

const (
	fedoraTag = "39"

	pindName      = "turtlefinder-pind"
	pindImageName = "siemens/turtlefinder-pind"

	canaryContainerName = "canary"
	canaryImageRef      = "docker.io/library/busybox:latest"

	spinupTimeout = 10 * time.Second
	spinupPolling = 500 * time.Millisecond
)

var _ = Describe("turtle finder", Ordered, Serial, func() {

	var pindCntr *dockertest.Resource

	BeforeAll(func() {
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

		By("spinning up a Docker container with a podman system demon^Wservice")
		pool := Successful(dockertest.NewPool("unix:///run/docker.sock"))
		_ = pool.RemoveContainerByName(pindName)
		// The necessary container start arguments loosely base on
		// https://www.redhat.com/sysadmin/podman-inside-container but had to be
		// heavily modified because they didn't work out as is, for whatever
		// reasons. This is now a mash-up of the args used to get the KinD
		// base-based images correctly working and some "spirit" of the before
		// mentioned RedHat blog post.
		//
		// Lesson learnt: podman in Docker is much more fragile than the podmen
		// want us to believe.
		//
		// docker run -it --rm --name pind
		//     --privileged \
		//     --cgroupns=private \
		//     --tmpfs /tmp \
		//     --tmpfs /run \
		//     --volume /var \
		//     --device=/dev/fuse \
		//   pind
		//
		// Please note that the initial build of the podman-in-Docker image is
		// really slow, as fedora installs lots of things.
		Expect(pool.Client.BuildImage(docker.BuildImageOptions{
			Name:       pindImageName,
			ContextDir: "./_test/pind", // sorry, couldn't resist the pun.
			Dockerfile: "Dockerfile",
			BuildArgs: []docker.BuildArg{
				{Name: "FEDORA_TAG", Value: fedoraTag},
			},
			OutputStream: io.Discard,
		})).To(Succeed())
		pindCntr = Successful(pool.RunWithOptions(
			&dockertest.RunOptions{
				Name:       pindName,
				Repository: pindImageName,
				Privileged: true,
				Mounts: []string{
					"/var", // well, this actually is an unnamed volume
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
			By("removing the podman-in-Docker container")
			Expect(pool.Purge(pindCntr)).To(Succeed())
		})

	})

	BeforeEach(resetActivatorPlugins)

	BeforeEach(test.LogToGinkgo)

	BeforeEach(func() {
		goodfds := Filedescriptors()
		goodgos := Goroutines() // avoid other failed goroutine tests to spill over
		DeferCleanup(func() {
			Eventually(Goroutines).WithTimeout(goroutinesUnwindTimeout).WithPolling(goroutinesUnwindPolling).
				ShouldNot(HaveLeaked(goodgos))
			Expect(Filedescriptors()).NotTo(HaveLeakedFds(goodfds))
		})
	})

	It("it finds and updates socket activators", func(ctx context.Context) {
		By("setting up a socket activator object for our systemd PID 1")
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		tf := New(
			func() context.Context { return ctx },
			WithGettingOnlineWait(5*time.Second))
		pidmap := model.NewProcessTable(false)
		var wg sync.WaitGroup
		tf.updateActivators(pidmap, &wg)
		done := make(chan struct{})
		go func() {
			defer close(done)
			wg.Wait()
		}()
		By("waiting for the update to be done")
		Eventually(done).Within(5 * time.Second).ProbeEvery(250 * time.Millisecond).
			Should(BeClosed())
		By("winding down")
		cancel()
	})

	// And Now ... All Together! This is becoming more extreme by the version
	// tag :D
	//
	// In order to avoid all the "nice" problems with installing podman distro
	// packages side-by-side into the host, just to find out that the way the
	// distro packagers made it destroys the docker installation, we run a
	// podman demon ("don't call ..." *plonk* ) inside a Docker container, as a
	// socket-activated service. And we create a podman workload inside that
	// container in the insane hope of discovering it. Ah, so many demons...
	It("finds docker, containerd, and podman-in-docker", func(ctx context.Context) {
		if os.Getuid() != 0 {
			Skip("needs root")
		}

		By("creating a new turtle finder")
		ctx, cancel := context.WithCancel(ctx)
		tf := New(func() context.Context { return ctx })
		Expect(tf).NotTo(BeNil())
		defer cancel()
		defer tf.Close()

		By("discovering at least three types of engines")
		// Ironically we should find containerd also when running this test on
		// Docker Desktop on WSL2, where the Docker daemon lives inside a
		// containerd container. In this case, we'll see another containerd
		// instance that is the Docker daemon's sidekick.
		Eventually(func() []*model.ContainerEngine {
			lxdisco := discover.Namespaces(discover.WithFullDiscovery())
			_ = tf.Containers(ctx, lxdisco.Processes, lxdisco.PIDMap)
			return tf.Engines()
		}).Within(spinupTimeout).ProbeEvery(spinupPolling).
			Should(ContainElements(
				HaveEngine(moby.Type, `^unix:///proc/\d+/root/run/docker.sock$`),
				HaveEngine(containerd.Type, `^unix:///proc/\d+/root/run/containerd/containerd.sock$`),
				HaveEngine(podman.Type, `^unix:///proc/\d+/root/run/podman/podman.sock$`),
			))

		By("checking for the presence of our dedicated podman-in-Docker engine instance...")
		Expect(tf.Engines()).To(ContainElement(
			HaveEngine(podman.Type, fmt.Sprintf(`^unix:///proc/%d/root/run/podman/podman.sock$`, pindCntr.Container.State.Pid)),
		), "missing podman-in-Docker engine")

		By("creating podman workload")
		exitcode, err := pindCntr.Exec([]string{
			"podman", "run", "-d", "-it" /*!!!?*/, "--rm", "--name", canaryContainerName, "--net", "host", canaryImageRef,
		}, dockertest.ExecOptions{
			StdOut: GinkgoWriter,
			StdErr: GinkgoWriter,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(exitcode).To(BeZero())

		By("discovering podman workload and its managing podman engine hierarchy")
		Eventually(func() []*model.Container {
			lxdisco := discover.Namespaces(discover.WithFullDiscovery())
			return tf.Containers(ctx, lxdisco.Processes, lxdisco.PIDMap)
		}).Within(spinupTimeout).ProbeEvery(spinupPolling).
			Should(ContainElement(And(
				matcher.HaveContainerNameID(canaryContainerName),
				HaveField("Type", podman.Type),
				HaveField("Labels", HaveKeyWithValue(
					TurtlefinderContainerPrefixLabelName, pindName)),
			)))
	})

})
