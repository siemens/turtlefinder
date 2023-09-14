// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package all

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDetectorAll(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "turtlefinder/detector/all")
}
