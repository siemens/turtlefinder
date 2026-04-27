// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package crio

import (
	"context"
	"log/slog"
	"sort"
	"time"

	detect "github.com/siemens/turtlefinder/detector"

	"github.com/thediveo/go-plugger/v3"
	"github.com/thediveo/lxkns/model"
	criengine "github.com/thediveo/whalewatcher/v2/engineclient/cri"
	"github.com/thediveo/whalewatcher/v2/watcher"
	"github.com/thediveo/whalewatcher/v2/watcher/cri"
)

// Register this CRI-O container (engine) discovery plugin. This statically
// ensures that the Detector interface is fully implemented.
func init() {
	plugger.Group[detect.Detector]().Register(
		&Detector{}, plugger.WithPlugin("cri-o"))
}

// Detector implements the detect.Detector interface. This is automatically
// type-checked by the previous plugin registration (Generics can be sweet,
// sometimes *snicker*).
type Detector struct{}

// EngineNames returns the process name of the containerd engine process.
func (d *Detector) EngineNames() []string {
	return []string{"crio"} // it's crio, not criod, or cri-o, ...
}

// NewWatcher returns a watcher for tracking alive containerd containers.
func (d *Detector) NewWatchers(ctx context.Context, pid model.PIDType, apis []string) []watcher.Watcher {
	sort.Strings(apis) // in-place
	for _, apipathname := range apis {
		slog.Debug("dialing CRI-O API endpoint", slog.String("api", apipathname))
		w, err := cri.New(apipathname, nil, criengine.WithPID(int(pid)))
		if err != nil {
			slog.Debug("CRI-O API endpoint failed",
				slog.String("api", apipathname), slog.String("err", err.Error()))
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		version := w.Version(ctx)
		if err := ctx.Err(); err != nil || version == "" {
			slog.Debug("CRI-O API Info call context hit deadline", slog.String("err", err.Error()))
		}
		cancel()
		if err == nil {
			return []watcher.Watcher{w}
		}
		w.Close()
	}
	slog.Error("no working CRI-O API endpoint found")
	return nil
}
