// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package containerd

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const goroutinesUnwindTimeout = 5 * time.Second
const goroutinesUnwindPolling = 250 * time.Millisecond

func TestDetectorContainerd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "turtlefinder/detector/containerd")
}
