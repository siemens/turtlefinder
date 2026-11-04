// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package crio

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/thediveo/go-plugger/v3"
	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/nonstd/xslog"
	criengine "github.com/thediveo/whalewatcher/v2/engineclient/cri"
	"github.com/thediveo/whalewatcher/v2/watcher"
	"github.com/thediveo/whalewatcher/v2/watcher/cri"

	detect "github.com/siemens/turtlefinder/v2/detector"
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

// NewWatchers returns a (single) watcher for tracking alive containerd
// containers.
func (d *Detector) NewWatchers(ctx context.Context, pid model.PIDType, apis []string) []watcher.Watcher {
	sort.Strings(apis) // in-place
	for _, apipathname := range apis {
		slog.Debug("dialing CRI-O API endpoint", slog.String("api", apipathname))
		w, err := cri.New(apipathname, nil, criengine.WithPID(int(pid)))
		if err != nil {
			slog.Debug("CRI-O API endpoint failed",
				slog.String("api", apipathname),
				xslog.Error(err))
			continue
		}
		versionctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		version := w.Version(versionctx)
		err = versionctx.Err()
		cancel()
		if version != "" {
			return []watcher.Watcher{w}
		}
		slog.Debug("CRI-O API endpoint returned no version",
			slog.String("api", apipathname),
			xslog.Error(err))
		w.Close()
	}
	slog.Error("no working CRI-O API endpoint found")
	return nil
}
