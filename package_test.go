// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"testing"
	"time"

	_ "github.com/thediveo/lxkns/log/logrus"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const goroutinesUnwindTimeout = 2 * time.Second
const goroutinesUnwindPolling = 250 * time.Millisecond

func TestTurtlefinder(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "turtlefinder")
}
