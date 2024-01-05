// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package unsorted

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/thediveo/fdooze"
	. "github.com/thediveo/success"
)

var _ = Describe("unsorted readdir", func() {

	BeforeEach(func() {
		goodfds := Filedescriptors()
		DeferCleanup(func() {
			Expect(Filedescriptors()).NotTo(HaveLeakedFds(goodfds))
		})
	})

	It("reports an error when not being able to read a directory", func() {
		Expect(ReadDir("./_test/readdir-non-existing")).Error().To(HaveOccurred())
	})

	It("returns directory entries", func() {
		entries := Successful(ReadDir("./_test/readdir"))
		Expect(entries).To(ConsistOf(
			And(HaveField("Name()", "123"), HaveField("IsDir()", true)),
			And(HaveField("Name()", "ABC"), HaveField("IsDir()", false)),
		))
	})

})
