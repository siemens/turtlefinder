// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
	"time"

	"github.com/thediveo/lxkns/log"
	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/whalewatcher/watcher"
)

// Engine watches a single container engine process for signs of container
// workload life, using the supplied "whale watcher".
//
// Engine objects then can be queried for their workload, that is, the list of
// currently alive (running/paused) containers they manage.
//
// An Engine can be “done” at any time when the container engine process
// terminates or otherwise disconnects the watcher. In this case, the Done
// channel will be closed.
type Engine struct {
	watcher.Watcher               // engine watcher (doubles as engine adapter).
	ID              string        // engine ID.
	Version         string        // engine version.
	Done            chan struct{} // closed when watch is done/has terminated.
	PPIDHint        model.PIDType // PID of engine's process; for container PID translation.
}

// NewEngine returns a new Engine given the specified watcher. As NewEngine
// returns, the Engine is already "warming up" and has started watching (using
// the given context).
//
// ppidhint optionally specifies a container engine's immediate parent process.
// This information is later necessary for lxkns to correctly translate
// container PIDs. When activating a socket-activated engine, the process tree
// scan does never include the engine, as this is only activated after the scan.
// In order to still allow lxkns to translate container PIDs related to newly
// socket-activated engines, we assume that the engine's parent process PID is
// in the same PID namespace, so we can also use that for correct PID
// translation.
func NewEngine(ctx context.Context, w watcher.Watcher, ppidhint model.PIDType) *Engine {
	idctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	e := &Engine{
		Watcher:  w,
		ID:       w.ID(idctx),
		Version:  w.Version(idctx),
		Done:     make(chan struct{}, 1), // might never be picked up in some situations
		PPIDHint: ppidhint,
	}
	cancel() // ensure to quickly release cancel, silence linter
	log.Infof("watching %s container engine (PID %d) with ID '%s', version '%s'",
		w.Type(), w.PID(), e.ID, e.Version)
	go func() {
		err := e.Watcher.Watch(ctx)
		log.Infof("stopped watching container engine (PID %d), reason: %s",
			w.PID(), err.Error())
		close(e.Done)
		e.Close()
	}()
	return e
}

// Containers returns the alive containers managed by this engine, using the
// associated watcher.
//
// The containers returned will reference a model.ContainerEngine and thus are
// decoupled from a turtlefinder's (container) Engine object.
func (e *Engine) Containers(ctx context.Context) []*model.Container {
	eng := &model.ContainerEngine{
		ID:       e.ID,
		Type:     e.Watcher.Type(),
		Version:  e.Version,
		API:      e.Watcher.API(),
		PID:      model.PIDType(e.Watcher.PID()),
		PPIDHint: e.PPIDHint,
	}
	// Adapt the whalewatcher container model to the lxkns container model,
	// where the latter takes container engines and groups into account of its
	// information model. We only need to set the container engine, as groups
	// will be handled separately by the various (lxkns) decorators.
	for _, projname := range append(e.Watcher.Portfolio().Names(), "") {
		project := e.Watcher.Portfolio().Project(projname)
		if project == nil {
			continue
		}
		for _, container := range project.Containers() {
			// Ouch! Make sure to clone the Labels map and not simply pass it
			// directly on to our ontainer objects. Otherwise decorators adding
			// labels would modify the labels shared through the underlying
			// container label source. So, clone the labels (top-level only) and
			// then happy decorating.
			clonedLabels := model.Labels{}
			for k, v := range container.Labels {
				clonedLabels[k] = v
			}
			cntr := &model.Container{
				ID:     container.ID,
				Name:   container.Name,
				Type:   eng.Type,
				Flavor: eng.Type,
				PID:    model.PIDType(container.PID),
				Paused: container.Paused,
				Labels: clonedLabels,
				Engine: eng,
			}
			eng.AddContainer(cntr)
		}
	}
	return eng.Containers
}

// IsAlive returns true as long as the engine watcher is operational and hasn't
// permanently failed/terminated.
func (e *Engine) IsAlive() bool {
	select {
	case <-e.Done:
		return false
	default:
		// nothing to see, move on!
	}
	return true
}
