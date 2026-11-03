// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thediveo/whalewatcher/v2/watcher"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const goroutinesUnwindTimeout = 2 * time.Second
const goroutinesUnwindPolling = 250 * time.Millisecond

func TestTurtlefinder(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "turtlefinder")
}

// countingWatcher wraps a watcher.Watcher and counts its Watch calls.
type countingWatcher struct {
	watcher.Watcher
	watches atomic.Int32
}

func (c *countingWatcher) Watch(ctx context.Context) error {
	c.watches.Add(1)
	return c.Watcher.Watch(ctx)
}
