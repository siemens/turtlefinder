// (c) Siemens AG 2024
//
// SPDX-License-Identifier: MIT

package podman

import (
	"context"
	"time"

	"github.com/docker/docker/client" // priceless
	"github.com/siemens/turtlefinder/activator"
	"github.com/thediveo/go-plugger/v3"
	"github.com/thediveo/lxkns/log"
	"github.com/thediveo/lxkns/model"
	mobyengine "github.com/thediveo/whalewatcher/engineclient/moby"
	"github.com/thediveo/whalewatcher/watcher"
	"github.com/thediveo/whalewatcher/watcher/moby"
)

// Type identifying podman workloads and as returned by Watcher.Type().
const Type = "podman.io"

// Register this socket service activator container engine discovery plugin.
// This statically ensures that the Detector interface is fully implemented.
func init() {
	plugger.Group[activator.EngineFinder]().Register(
		&Engine{}, plugger.WithPlugin("podman"))
}

type Engine struct{}

// Ident returns information in order to detect engine API endpoints and
// their corresponding container engine processes.
func (e *Engine) Ident() activator.EngineIdentification {
	return activator.EngineIdentification{
		APIEndpointSuffix: "podman.sock",
		ProcessName:       "podman", // don't call it "podmand"...!
	}
}

// NewWatcher returns a watcher tracking the alive container workload of the
// container engine accessible by the specified API path.
func (e *Engine) NewWatcher(ctx context.Context, pid model.PIDType, api string) watcher.Watcher {
	var err error
	var w watcher.Watcher
	defer func() {
		if err != nil && w != nil {
			w.Close()
		}
	}()

	// We use the Docker API on podman, not least as the podman-specific API is
	// very-very hard to use in production (as the @thediveo/sealwatcher
	// experiment unfortunately has shown) and the podman developers basically
	// told us to stick with the Docker API, as podman-specific features were
	// never really adapted by users (such as pods on a non-k8s engine).
	//
	// As Docker's go client will accept any API pathname we throw at it and
	// throw up only when actually trying to communicate with the engine, it's
	// not sufficient to just create the watcher, we also need to check that we
	// actually can successfully talk with the daemon. Querying the daemon's
	// info sufficies and ensures that a partiular API path is useful.
	log.Debugf("dialing podman endpoint 'unix://%s'", api)
	w, err = moby.New("unix://"+api, nil,
		mobyengine.WithPID(int(pid)),
		mobyengine.WithDemonType(Type))
	if err != nil {
		log.Debugf("podman API endpoint 'unix://%s' failed: %s", api, err.Error())
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = w.Client().(*client.Client).Info(ctx)
	if ctxerr := ctx.Err(); ctxerr != nil {
		err = ctxerr
		log.Debugf("Docker API Info call context hit deadline: %s", err.Error())
		return nil
	}
	if err != nil {
		log.Debugf("podman API endpoint 'unix://%s' failed: %s", api, err.Error())
		return nil
	}
	return w
}
