// (c) Siemens AG 2024
//
// SPDX-License-Identifier: MIT

//go:build matchers
// +build matchers

package test

import (
	"bytes"
	"log/slog"
	"sync"

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
	oldGw := GinkgoWriter
	DeferCleanup(func() { GinkgoWriter = oldGw })
	GinkgoWriter = newBuffer(oldGw)

	oldSlogger := slog.Default()
	DeferCleanup(func() { slog.SetDefault(oldSlogger) })
	slog.SetDefault(slog.New(
		slog.NewTextHandler(GinkgoWriter, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})))
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
