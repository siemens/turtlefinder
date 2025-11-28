// (c) Siemens AG 2025
//
// SPDX-License-Identifier: MIT

package testslog

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTestslog(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "turtlefinder/internal/testslog")
}
