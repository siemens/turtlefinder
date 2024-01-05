// (c) Siemens AG 2024
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"time"

	"github.com/thediveo/whalewatcher/watcher"
)

type slowWatcher struct {
	watcher.Watcher
	ready chan struct{}
}

// slowWatch wraps a watcher.Watcher and simulates it being slow to become
// Ready().
func newSlowwatch(w watcher.Watcher, dawdle time.Duration) watcher.Watcher {
	s := &slowWatcher{
		Watcher: w,
		ready:   make(chan struct{}),
	}
	time.AfterFunc(dawdle, func() {
		close(s.ready)
	})
	return s
}

func (s *slowWatcher) Ready() <-chan struct{} {
	select {
	case <-s.ready: // only after slowReady report true state of things
		return s.Watcher.Ready()
	default: // still dawdling around
		return s.ready
	}
}
