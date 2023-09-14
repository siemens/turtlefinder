// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package crio

import (
	"context"
	"sort"
	"time"

	detect "github.com/siemens/turtlefinder/detector"

	"github.com/thediveo/go-plugger/v3"
	"github.com/thediveo/lxkns/log"
	"github.com/thediveo/lxkns/model"
	criengine "github.com/thediveo/whalewatcher/engineclient/cri"
	"github.com/thediveo/whalewatcher/watcher"
	"github.com/thediveo/whalewatcher/watcher/cri"
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
		log.Debugf("dialing CRI-O API endpoint '%s'", apipathname)
		w, err := cri.New(apipathname, nil, criengine.WithPID(int(pid)))
		if err != nil {
			log.Debugf("CRI-O API endpoint '%s' failed: %s", apipathname, err.Error())
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		version := w.Version(ctx)
		if err := ctx.Err(); err != nil || version == "" {
			log.Debugf("CRI-O API Info call context hit deadline: %s", err.Error())
		}
		cancel()
		if err == nil {
			return []watcher.Watcher{w}
		}
		w.Close()
	}
	log.Errorf("no working CRI-O API endpoint found.")
	return nil
}
