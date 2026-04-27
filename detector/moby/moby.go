// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package moby

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/moby/moby/client"
	"github.com/thediveo/go-plugger/v3"
	"github.com/thediveo/lxkns/model"
	mobyengine "github.com/thediveo/whalewatcher/v2/engineclient/moby"
	"github.com/thediveo/whalewatcher/v2/watcher"
	"github.com/thediveo/whalewatcher/v2/watcher/moby"

	detect "github.com/siemens/turtlefinder/v2/detector"
)

// Register this Docker container (engine) discovery plugin. This statically
// ensures that the Detector interface is fully implemented.
func init() {
	plugger.Group[detect.Detector]().Register(
		&Detector{}, plugger.WithPlugin("dockerd"))
}

// Detector implements the detect.Detector interface. This is automatically
// type-checked by the previous plugin registration (Generics can be sweet,
// sometimes *snicker*).
type Detector struct{}

// EngineNames returns the process name of the Docker/moby engine process.
func (d *Detector) EngineNames() []string {
	return []string{"dockerd"}
}

// NewWatchers returns a single watcher for tracking alive Docker containers.
func (d *Detector) NewWatchers(ctx context.Context, pid model.PIDType, apis []string) []watcher.Watcher {
	sort.Strings(apis) // in-place
	for _, apipathname := range apis {
		// As Docker's go client will accept any API pathname we throw at it and
		// throw up only when actually trying to communicate with the engine,
		// it's not sufficient to just create the watcher, we also need to check
		// that we actually can successfully talk with the daemon. Querying the
		// daemon's info sufficies and ensures that a partiular API path is
		// useful.
		slog.Debug("dialing Docker endpoint", slog.String("api", apipathname))
		w, err := moby.New("unix://"+apipathname, nil, mobyengine.WithPID(int(pid)))
		if err == nil {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, err = w.Client().(*client.Client).Info(ctx, client.InfoOptions{})
			if ctxerr := ctx.Err(); ctxerr != nil {
				slog.Debug("Docker API Info call context hit deadline", slog.String("err", ctxerr.Error()))
			}
			cancel()
			if err == nil {
				return []watcher.Watcher{w}
			}
			w.Close()
		}
		slog.Debug("Docker API endpoint", slog.String("api", apipathname), slog.String("err", err.Error()))
	}
	slog.Error("no working Docker API endpoint found")
	return nil
}
