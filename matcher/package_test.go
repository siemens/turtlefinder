// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package matcher

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestUtil(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "turtlefinder/util")
}
