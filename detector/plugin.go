// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package detector

import (
	"context"

	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/whalewatcher/watcher"
)

// Detector allows specialized container engine detector plugins to interface
// with the generic engine discovery mechanism, by describing how to detect a
// particular container engine using its process name and creating the correct
// watcher(s) to monitor the container workload.
type Detector interface {
	// EngineNames returns one or more process name(s) of a specific type of
	// container engine.
	EngineNames() []string

	// NewWatchers returns one or more watchers for tracking the alive container
	// workload of the container engine accessible by at least one of the
	// specified API paths. Usually, this will be only a single watcher per
	// engine, but in case of containerd we want to return multiple watchers,
	// one for plain containerd and one for its CRI view.
	NewWatchers(ctx context.Context, pid model.PIDType, apis []string) []watcher.Watcher
}
