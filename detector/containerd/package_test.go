// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package containerd

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDetectorContainerd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "turtlefinder/detector/containerd")
}
