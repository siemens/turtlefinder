// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

//go:build matchers
// +build matchers

package matcher

import (
	"github.com/thediveo/lxkns/model"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("matchers", func() {

	Context("HaveContainerNameID", func() {

		It("doesn't accept anything other than string and GomegaMatcher when creating the matcher", func() {
			Expect(func() {
				_ = HaveContainerNameID(42)
			}).To(PanicWith(ContainSubstring("argument must be string or GomegaMatcher")))
			Expect(func() {
				_ = HaveContainerNameID("foo")
			}).NotTo(Panic())
			Expect(func() {
				_ = HaveContainerNameID(Equal("foo"))
			}).NotTo(Panic())
		})

		It("requires an actual Container or *Container", func() {
			m := HaveContainerNameID("foo")
			cntr := model.Container{
				Name: "foo",
				ID:   "42",
			}
			Expect(m.Match(cntr)).To(BeTrue())
			Expect(m.Match(&cntr)).To(BeTrue())

			m = HaveContainerNameID("42")
			Expect(m.Match(cntr)).To(BeTrue())
			Expect(m.Match(&cntr)).To(BeTrue())

			Expect(HaveContainerNameID("bar").Match(cntr)).To(BeFalse())
		})

	})

})
