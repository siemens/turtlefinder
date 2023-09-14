// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package containerd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/containerd/containerd"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	detect "github.com/siemens/turtlefinder/detector"
	"github.com/thediveo/go-plugger/v3"
	"github.com/thediveo/whalewatcher/engineclient/containerd/test/ctr"
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

	var providerCntr *dockertest.Resource

	BeforeAll(func() {
		if os.Getuid() != 0 {
			Skip("needs root")
		}

		goodfds := Filedescriptors()
		goodgos := Goroutines() // avoid other failed goroutine tests to spill over
		DeferCleanup(func() {
			Eventually(Goroutines).WithTimeout(5 * time.Second).WithPolling(250 * time.Millisecond).
				ShouldNot(HaveLeaked(goodgos))
			Expect(Filedescriptors()).NotTo(HaveLeakedFds(goodfds))
		})

		By("spinning up a Docker container with stand-alone containerd, courtesy of the KinD k8s sig")
		pool := Successful(dockertest.NewPool("unix:///var/run/docker.sock"))
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
			ContextDir: "./test/_kindisch", // sorry, couldn't resist the pun.
			Dockerfile: "Dockerfile",
			BuildArgs: []docker.BuildArg{
				{Name: "KINDEST_BASE_TAG", Value: test.KindestBaseImageTag},
			},
			OutputStream: io.Discard,
		})).To(Succeed())
		providerCntr = Successful(pool.RunWithOptions(
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
			Expect(pool.Purge(providerCntr)).To(Succeed())
		})

		By("waiting for containerized containerd to become responsive")
		Expect(providerCntr.Container.State.Pid).NotTo(BeZero())
		// apipath must not include absolute symbolic links, but already be
		// properly resolved.
		endpointPath := fmt.Sprintf("/proc/%d/root%s",
			providerCntr.Container.State.Pid, "/run/containerd/containerd.sock")
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
		DeferCleanup(func() {
			_ = ctr.Exec(providerCntr,
				"-n", testNamespace,
				"task", "rm", "-f", testContainerName)
			_ = ctr.Exec(providerCntr,
				"-n", testNamespace,
				"container", "rm", testContainerName)
		})

		By("running the detector on the API endpoints")
		d := &Detector{}
		wormhole := fmt.Sprintf("/proc/%d/root", providerCntr.Container.State.Pid)
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
