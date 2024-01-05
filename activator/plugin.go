// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package activator

import (
	"context"

	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/whalewatcher/watcher"
)

// Detector identifies a particular socket service activator by its well-known
// process name. For instance, “systemd”. Please note that the socket activator
// names must match the “COMM” field of a process status information (same as
// /proc/[PID]/comm, see [proc(5)]), and not the executable path or name.
//
// [proc(5)]: https://man7.org/linux/man-pages/man5/proc.5.html
type Detector interface {
	Name() string // process name of socket activator
}

// EngineFinder returns information for identifying socket-activatable container
// engine processes and also allows creating suitable (workload) watchers for
// these engines.
type EngineFinder interface {
	// Ident returns information in order to detect engine API endpoints and
	// their corresponding container engine processes.
	Ident() EngineIdentification

	// NewWatcher returns a watcher tracking the alive container workload of the
	// container engine accessible by the specified API path.
	//
	// On purpose, this supports only single API-ended engines and expects only
	// a single watcher to get created and returned.
	NewWatcher(ctx context.Context, pid model.PIDType, api string) watcher.Watcher
}

// EngineIdentification specifies the information needed to detect API endpoints
// for socket-activatable container engines, as well as the engine process name.
type EngineIdentification struct {
	APIEndpointSuffix string // API endpoint name such as "foo.sock", without any path.
	ProcessName       string // name of engine process.
}
