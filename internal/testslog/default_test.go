// (c) Siemens AG 2025
//
// SPDX-License-Identifier: MIT

package testslog

import (
	"log/slog"

	"github.com/thediveo/safe"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("capturing structured logging in tests", func() {

	It("captures the log into a safe buffer", func() {
		oldDefault := slog.Default()
		DeferCleanup(slog.SetDefault, oldDefault)
		b := SetDefault(slog.LevelDebug, nil)
		Expect(b).NotTo(BeNil())
		Expect(slog.Default()).NotTo(BeIdenticalTo(oldDefault))

		slog.Debug("fooh!")

		Expect(b.String()).To(ContainSubstring("level=DEBUG msg=fooh!"))
	})

	It("duplicates the log into an additional writer", func() {
		DeferCleanup(slog.SetDefault, slog.Default())

		var repl safe.Buffer
		b := SetDefault(slog.LevelDebug, &repl)
		Expect(b).NotTo(BeNil())

		slog.Debug("fooh!")

		Expect(repl.String()).To(ContainSubstring("level=DEBUG msg=fooh!"))
	})

})
