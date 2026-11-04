// (c) Siemens AG 2026
//
// SPDX-License-Identifier: MIT

package podman

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("podman engine finder", func() {

	It("rejects unreachable API endpoints", func(ctx context.Context) {
		Expect((&Engine{}).NewWatcher(ctx, 0, "/nowhere/podman.sock")).To(BeNil())
	})

	It("rejects unresponsive API endpoints", func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		Expect((&Engine{}).NewWatcher(ctx, 0, silentUDS())).To(BeNil())
	})

})
