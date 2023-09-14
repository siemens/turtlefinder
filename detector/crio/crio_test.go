// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package crio

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/containerd/containerd"
	"github.com/google/uuid"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	detect "github.com/siemens/turtlefinder/detector"
	"github.com/thediveo/go-plugger/v3"
	criengine "github.com/thediveo/whalewatcher/engineclient/cri"
	"github.com/thediveo/whalewatcher/engineclient/cri/test/img"
	"github.com/thediveo/whalewatcher/test"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/thediveo/fdooze"
	. "github.com/thediveo/success"
)

const (
	kindischName = "turtlefinder-crio"

	k8sTestNamespace = "tfcritest"
	k8sTestPodName   = "wwcritestpod"
)

var _ = Describe("CRI-O turtle watcher", Ordered, func() {

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

		By("spinning up a Docker container with stand-alone CRI-O, courtesy of the KinD k8s sig and cri-o.io")
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
			By("removing the CRI-O Docker container")
			Expect(pool.Purge(providerCntr)).To(Succeed())
		})

		By("waiting for containerized CRI-O to become responsive")
		Expect(providerCntr.Container.State.Pid).NotTo(BeZero())
		// apipath must not include absolute symbolic links, but already be
		// properly resolved.
		endpointPath := fmt.Sprintf("/proc/%d/root%s",
			providerCntr.Container.State.Pid, "/run/crio/crio.sock")
		var cdclient *containerd.Client
		Eventually(func() error {
			var err error
			cdclient, err = containerd.New(endpointPath,
				containerd.WithTimeout(5*time.Second))
			return err
		}).Within(30*time.Second).ProbeEvery(1*time.Second).
			Should(Succeed(), "CRI-O API never became responsive")
		cdclient.Close() // not needed anymore, will create fresh ones over and over again
	})

	It("registers correctly", func() {
		Expect(plugger.Group[detect.Detector]().Plugins()).To(
			ContainElement("cri-o"))
	})

	It("tries unsuccessfully", NodeTimeout(30*time.Second), func(ctx context.Context) {
		d := &Detector{}
		Expect(d.NewWatchers(ctx, 0, []string{"/etc/rumpelpumpel"})).To(BeEmpty())
	})

	It("watches successfully", NodeTimeout(30*time.Second), func(ctx context.Context) {
		var cricl *criengine.Client

		By("waiting for the CRI-O API to become responsive")
		Expect(providerCntr.Container.State.Pid).NotTo(BeZero())
		// apipath must not include absolute symbolic links, but already be
		// properly resolved.
		endpoint := fmt.Sprintf("/proc/%d/root/run/crio/crio.sock",
			providerCntr.Container.State.Pid)
		Eventually(func() error {
			var err error
			cricl, err = criengine.New(endpoint, criengine.WithTimeout(1*time.Second))
			return err
		}).Within(30*time.Second).ProbeEvery(1*time.Second).
			Should(Succeed(), "CRI-O API never became responsive")
		DeferCleanup(func() {
			cricl.Close()
			cricl = nil
		})

		By("waiting for the CRI-O API to become fully operational", func() {
			Eventually(ctx, func(ctx context.Context) error {
				_, err := cricl.RuntimeService().Status(ctx, &runtime.StatusRequest{})
				return err
			}).ProbeEvery(250 * time.Millisecond).
				Should(Succeed())
		})

		By("creating a new pod")
		podconfig := &runtime.PodSandboxConfig{
			Metadata: &runtime.PodSandboxMetadata{
				Name:      k8sTestPodName,
				Namespace: k8sTestNamespace,
				Uid:       uuid.NewString(),
			},
		}
		podsbox := Successful(cricl.RuntimeService().RunPodSandbox(ctx, &runtime.RunPodSandboxRequest{
			Config: podconfig,
		}))
		defer func() {
			By("removing the pod")
			Expect(cricl.RuntimeService().RemovePodSandbox(ctx, &runtime.RemovePodSandboxRequest{
				PodSandboxId: podsbox.PodSandboxId,
			})).Error().NotTo(HaveOccurred())
		}()

		By("pulling the required canary image")
		Expect(cricl.ImageService().PullImage(ctx, &runtime.PullImageRequest{
			Image: &runtime.ImageSpec{
				Image: "busybox:stable",
			},
		})).Error().NotTo(HaveOccurred())

		By("creating a container inside the pod")
		podcntr := Successful(cricl.RuntimeService().CreateContainer(ctx, &runtime.CreateContainerRequest{
			PodSandboxId: podsbox.PodSandboxId,
			Config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{
					Name: "hellorld",
				},
				Image: &runtime.ImageSpec{
					Image: "busybox:stable",
				},
				Command: []string{
					"/bin/sh",
					"-c",
					"mkdir -p /www && echo Hellorld!>/www/index.html && httpd -f -p 5099 -h /www",
				},
				Labels: map[string]string{
					"foo": "bar",
				},
				Annotations: map[string]string{
					"fools": "barz",
				},
			},
			SandboxConfig: podconfig,
		}))
		defer func() {
			By("removing the container")
			Expect(cricl.RuntimeService().RemoveContainer(ctx, &runtime.RemoveContainerRequest{
				ContainerId: podcntr.ContainerId,
			})).Error().NotTo(HaveOccurred())
		}()

		By("starting the container")
		Expect(cricl.RuntimeService().StartContainer(ctx, &runtime.StartContainerRequest{
			ContainerId: podcntr.ContainerId,
		})).Error().NotTo(HaveOccurred())

		By("running the detector on the API endpoints")
		d := &Detector{}
		wormhole := fmt.Sprintf("/proc/%d/root", providerCntr.Container.State.Pid)
		ws := d.NewWatchers(ctx, 0, []string{
			wormhole + "/run/crio/crio.sock",
		})
		Expect(ws).To(HaveLen(1))
		w := ws[0]
		defer w.Close()
		go func() { // ...will be ended by cancelling the context
			_ = w.Watch(ctx)
		}()
		Eventually(w.Portfolio().Project("").ContainerNames).Within(5 * time.Second).ProbeEvery(250 * time.Millisecond).
			// CRI whalewather doesn't mangle container names with
			// namespace/pod, but instead leaves this up to others.
			Should(ContainElement("hellorld"))
	})

})
