// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package all

import (
	"github.com/siemens/turtlefinder/detector"
	"github.com/thediveo/go-plugger/v3"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("detector plugins", func() {

	It("has all the engine detector plugins registered", func() {
		namers := plugger.Group[detector.Detector]().Symbols()
		names := []string{}
		for _, namer := range namers {
			names = append(names, namer.EngineNames()...)
		}
		Expect(names).To(ConsistOf(
			"containerd", "dockerd", "crio",
		))
	})

})
