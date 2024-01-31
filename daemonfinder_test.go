// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/thediveo/lxkns/model"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/thediveo/success"
)

const dialSocketTimeout = 5 * time.Second

var _ = Describe("finding socket-activated demons", Serial, Ordered, func() {

	DescribeTable("matching process status",
		func(statline, name, ppidtext string, expected bool) {
			Expect(processStatusMatch(statline, name, ppidtext)).To(Equal(expected))
		},
		Entry("empty stat line", "", "duhkr", "1", false),
		Entry("invalid comm field", "42 (duhkr", "duhkr", "1", false),
		Entry("not our name", "42 (foobar)", "duhkr", "1", false),
		Entry("no PPID", "42 (duhkr)", "duhkr", "1", false),
		Entry("other PID", "42 (duhkr) zx81 666 ", "duhkr", "1", false),
		Entry("match", "42 (duhkr;)-) spectrum 1 ", "duhkr;)-", "1", true),
	)

	It("finds the socket-activated Docker demon's PID", func(ctx context.Context) {
		if os.Getuid() != 0 {
			Skip("needs root")
		}

		By("activating the Docker demon")
		ctx, cancel := context.WithTimeout(ctx, dialSocketTimeout)
		defer cancel()
		d := net.Dialer{}
		dsock := Successful(d.DialContext(ctx, "unix", "/run/docker.sock"))
		defer dsock.Close()

		By("finding the ino of the Docker API socket from /proc/self/net/unix")
		netunix := Successful(os.Open("/proc/self/net/unix"))
		defer netunix.Close()
		var udsino uint64
		scanner := bufio.NewScanner(netunix)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasSuffix(line, " /run/docker.sock") {
				continue
			}
			fields := strings.Fields(line)
			if fields[netUnixFlagsField] != "00010000" { // only take the listing UDS, not any other connected UDS
				continue
			}
			udsino = uint64(Successful(strconv.ParseUint(fields[6], 10, 64)))
			break
		}
		Expect(udsino).NotTo(BeZero())

		By("searching the demon")
		var dpid model.PIDType
		Eventually(func() model.PIDType {
			dpid = findDaemon(1, "dockerd", udsino)
			return dpid
		}).Within(2*time.Second).ProbeEvery(100*time.Millisecond).
			ShouldNot(BeZero(), "didn't find a suitable dockerd process at all")
		Expect(string(Successful(os.ReadFile(fmt.Sprintf("/proc/%d/stat", dpid))))).
			To(ContainSubstring(" (dockerd) "), "didn't find the correct process")
	})

	It("returns a zero PID when the daemon could not be found", func() {
		Expect(findDaemon(1, "duhkr-deh", 0)).To(BeZero())
	})

})
