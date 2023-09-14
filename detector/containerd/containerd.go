// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package containerd

import (
	"context"
	"sort"
	"strings"
	"time"

	detect "github.com/siemens/turtlefinder/detector"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	cdclient "github.com/containerd/containerd"
	"github.com/thediveo/go-plugger/v3"
	"github.com/thediveo/lxkns/log"
	"github.com/thediveo/lxkns/model"
	cdengine "github.com/thediveo/whalewatcher/engineclient/containerd"
	criengine "github.com/thediveo/whalewatcher/engineclient/cri"
	"github.com/thediveo/whalewatcher/watcher"
	"github.com/thediveo/whalewatcher/watcher/containerd"
	"github.com/thediveo/whalewatcher/watcher/cri"
)

// Register this containerd container (engine) discovery plugin. This statically
// ensures that the Detector interface is fully implemented.
func init() {
	plugger.Group[detect.Detector]().Register(
		&Detector{}, plugger.WithPlugin("containerd"))
}

// Detector implements the detect.Detector interface. This is automatically
// type-checked by the previous plugin registration (Generics can be sweet,
// sometimes *snicker*).
type Detector struct{}

// EngineNames returns the process name of the containerd engine process.
func (d *Detector) EngineNames() []string {
	return []string{"containerd"}
}

// NewWatcher returns a watcher for tracking alive containerd containers.
func (d *Detector) NewWatchers(ctx context.Context, pid model.PIDType, apis []string) []watcher.Watcher {
	sort.Strings(apis) // in-place
	for _, apipathname := range apis {
		if strings.HasSuffix(apipathname, ".ttrpc") {
			continue
		}

		// Remember: containerd not only has its own native API, but might also
		// have CRI enabled.
		watchers := []watcher.Watcher{}

		// As containerd's go client will accept more or less any API pathname
		// we throw at it and throw up only when actually trying to communicate
		// with the engine and only after some time, it's not sufficient to just
		// create the watcher, we also need to check that we actually can
		// successfully talk with the daemon. Querying the daemon's version
		// information sufficies and ensures that a partiular API path is
		// useful.
		log.Debugf("dialing containerd endpoint '%s'", apipathname)
		w, err := containerd.New(apipathname, nil, cdengine.WithPID(int(pid)))
		if err != nil {
			log.Debugf("containerd API endpoint '%s' failed: %s", apipathname, err.Error())
			continue
		}
		versionctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err = w.Client().(*cdclient.Client).Version(versionctx)
		if ctxerr := ctx.Err(); ctxerr != nil {
			cancel()
			log.Debugf("containerd API Info call context hit deadline: %s", err.Error())
			w.Close()
			continue
		}
		cancel()
		if err != nil {
			w.Close()
			continue
		}
		watchers = append(watchers, w)

		// Do we get the bonus CRI API...?
		criw, err := cri.New(apipathname, nil, criengine.WithPID(int(pid)))
		if err != nil {
			log.Debugf("containerd CRI API disabled: %s", err.Error())
			return watchers // NOPE!
		}
		// Creating the engine client usually succeeds, even if the CRI API
		// isn't enabled, because that's not really checked yet. So we try
		// some CRI API function in order to see if that succeeds...
		versionctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		_, err = criw.Client().(*criengine.Client).RuntimeService().
			Version(versionctx, &runtime.VersionRequest{Version: "0.1.0"})
		cancel()
		if err != nil {
			criw.Close()
			log.Debugf("containerd CRI API disabled: %s", err.Error())
			return watchers // NOPE!
		}

		watchers = append(watchers, criw)
		return watchers
	}
	log.Errorf("no working containerd API endpoint found.")
	return nil
}
