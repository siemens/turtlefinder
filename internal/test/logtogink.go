// (c) Siemens AG 2024
//
// SPDX-License-Identifier: MIT

//go:build matchers
// +build matchers

package test

import (
	"bytes"
	"sync"

	"github.com/sirupsen/logrus"

	. "github.com/onsi/ginkgo/v2"
)

// LogToGinkgo sends any log output to Ginkgo, so that the latter can show it to
// us when a test fails. If a test succeeds we won't get bothered by any log
// output. Additionally, it wraps the current GinkgoWriter so that it supports
// the [fmt.Stringer] interface, thus giving tests access to log output
// accumulated during an individual test.
//
// Usage:
//
//	BeforeEach(test.LogToGinkgo)
//
//	Eventually(GinkgoWriter.(fmt.Stringer).String).Should(...)
func LogToGinkgo() {
	// temporarily send log output to Ginkgo, so the latter can show it to
	// us when a test fails.
	std := logrus.StandardLogger()
	stdout := std.Out
	stdformatter := std.Formatter
	formatter := &logrus.TextFormatter{
		TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
		FullTimestamp:   true,
	}
	gw := GinkgoWriter
	GinkgoWriter = newBuffer(GinkgoWriter)
	std.Out = GinkgoWriter
	std.Formatter = formatter
	DeferCleanup(func() {
		GinkgoWriter = gw
		std.Out = stdout
		std.Formatter = stdformatter
	})
}

// buffer is “-race”-safe, can be queried for its contents, and wraps a
// GinkgoWriter.
type buffer struct {
	GinkgoWriterInterface
	mu sync.Mutex
	b  bytes.Buffer
}

func newBuffer(gw GinkgoWriterInterface) GinkgoWriterInterface {
	return &buffer{
		GinkgoWriterInterface: gw,
	}
}

func (b *buffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.GinkgoWriterInterface.Write(p)
	return b.b.Write(p)
}

func (b *buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
