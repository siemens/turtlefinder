// (c) Siemens AG 2026
//
// SPDX-License-Identifier: MIT

package containerd

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("containerd detector API endpoints", func() {

	It("skips ttrpc and rejects unreachable API endpoints", func(ctx context.Context) {
		Expect((&Detector{}).NewWatchers(ctx, 0, []string{
			"/nowhere/containerd.sock.ttrpc",
			"/nowhere/containerd.sock",
		})).To(BeEmpty())
	})

	It("rejects unresponsive API endpoints", func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		Expect((&Detector{}).NewWatchers(ctx, 0, []string{silentUDS()})).To(BeEmpty())
	})

})
