// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
	"time"

	"github.com/docker/docker/client"
	"github.com/siemens/turtlefinder/activator"
	"github.com/thediveo/go-plugger/v3"
	"github.com/thediveo/lxkns/model"
	mobyengine "github.com/thediveo/whalewatcher/engineclient/moby"
	"github.com/thediveo/whalewatcher/watcher"
	"github.com/thediveo/whalewatcher/watcher/moby"

	. "github.com/onsi/ginkgo/v2"
)

// register for testing purpose a socket-activatable container engine finder
// for Docker; in PROD we don't use a finder plugin for Docker, but always
// only the original container engine daemon process detector plugin. This
// test plugin allows us to run socket activator-related test against a
// Docker that is present anyway.
func dockerEngineFinderOnly() {
	g := plugger.Group[activator.EngineFinder]()
	backup := g.Backup()
	DeferCleanup(func() {
		g.Restore(backup)
	})
	g.Clear()
	g.Register(&dockerdEngineFinder{}, plugger.WithPlugin("test-dockerd"))
}

// dockerEngineFinder finds socket-activated Docker demons; this finder is for
// test only, not for PROD.
type dockerdEngineFinder struct{}

const dockerInfoTimeout = 5 * time.Second

var _ activator.EngineFinder = (*dockerdEngineFinder)(nil) // ensure plugin interface is implemented

func (e *dockerdEngineFinder) Ident() activator.EngineIdentification {
	return activator.EngineIdentification{
		APIEndpointSuffix: "docker.sock",
		ProcessName:       "dockerd",
	}
}

func (e *dockerdEngineFinder) NewWatcher(ctx context.Context, pid model.PIDType, api string) watcher.Watcher {
	var err error
	var w watcher.Watcher
	defer func() {
		if err != nil && w != nil {
			w.Close()
		}
	}()
	w, err = moby.New("unix://"+api, nil, mobyengine.WithPID(int(pid)))
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, dockerInfoTimeout)
	defer cancel()
	_, err = w.Client().(*client.Client).Info(ctx)
	if ctxerr := ctx.Err(); ctxerr != nil {
		err = ctxerr
		return nil
	}
	if err != nil {
		return nil
	}
	return w
}
