// (c) Siemens AG 2026
//
// SPDX-License-Identifier: MIT

package moby

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Docker detector API endpoints", func() {

	It("rejects unresponsive API endpoints", func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		Expect((&Detector{}).NewWatchers(ctx, 0, []string{silentUDS()})).To(BeEmpty())
	})

})
