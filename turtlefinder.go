// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/siemens/turtlefinder/activator"
	"github.com/siemens/turtlefinder/detector"
	"golang.org/x/exp/slices"
	"golang.org/x/sync/semaphore"

	_ "github.com/siemens/turtlefinder/activator/all" // pull in activator and socket-activated engine detector plugins
	_ "github.com/siemens/turtlefinder/detector/all"  // pull in engine detector plugins

	"github.com/thediveo/go-plugger/v3"
	"github.com/thediveo/lxkns/containerizer"
	"github.com/thediveo/lxkns/log"
	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/procfsroot"
	"github.com/thediveo/whalewatcher/watcher"
)

// Overseer gives access to information about container engines currently
// monitored.
//
// [turtlefinder.Turtlefinder] objects implement the Overseer interface to allow
// code given only a [containerizer.Containerizer] to query the currently
// monitored container engine instances.
//
//		var c containerizer.Containerizer
//		o, ok := c.(turtlefinder.Overseer)
//	 	if ok {
//		    engines := o.Engines()
//	 	}
type Overseer interface {
	Engines() []*model.ContainerEngine
}

// Contexter supplies a TurtleFinder with a suitable context for long-running
// container engine workload watching.
type Contexter func() context.Context

// TurtleFinder implements the lxkns Containerizer interface to discover alive
// containers from one or more container engines. It can be safely used from
// multiple goroutines.
//
// On demand, a TurtleFinder scans a process list for signs of container engines
// and then tries to contact the potential engines in order to watch their
// containers.
type TurtleFinder struct {
	contexter        Contexter           // contexts for workload watching.
	engineplugins    []enginePlugin      // static list of engine plugins.
	activatorplugins []activatorPlugin   // static list of activator plugins.
	numworkers       int                 // max number of parallel engine queries.
	workersem        *semaphore.Weighted // bounded pool.
	initialsyncwait  time.Duration       // max. wait for engine watch coming online (sync) before proceeding.

	mux        sync.Mutex                                // protects the following fields.
	engines    map[model.PIDType][]*Engine               // engines by PID; individual engines may have failed.
	activators map[model.PIDType]*socketActivatorProcess // socket activators we've found.
}

// TurtleFinder implements the lxkns Containerizer interface. And it's also an
// Overseer.
var _ containerizer.Containerizer = (*TurtleFinder)(nil)
var _ Overseer = (*TurtleFinder)(nil)

// enginePlugin represents the process names of a container engine discovery
// plugin, as well as the plugin's Discover function.
type enginePlugin struct {
	names      []string          // process names of interest.
	detector   detector.Detector // engine process detector plugin interface.
	pluginname string            // for housekeeping and logging.
}

// engineProcess represents an individual container engine process and the
// container engine discovery plugin responsible for it.
type engineProcess struct {
	proc   *model.Process // engine process
	engine *enginePlugin  // especially detector fn that acts as watcher factory
}

// activatorPlugin represents the process name of a socket activator as
// specified by an individual activator.Detector plugin.
type activatorPlugin struct {
	name       string // process name of activator.
	pluginname string // for housekeeping and logging.
}

// New returns a TurtleFinder object for further use. The supplied contexter is
// called whenever a new container engine has been found and its workload is to
// be watched: this contexter should return a suitable (long-running) context it
// preferably has control over, in order to properly shut down the "background"
// goroutine resources (indirectly) used by a TurtleFinder.
//
// Further options ([NewOption], such as [WithWorkers] and
// [WithGettingOnlineWait]) allow to customize the TurtleFinder object returned.
func New(contexter Contexter, opts ...NewOption) *TurtleFinder {
	f := &TurtleFinder{
		contexter:       contexter,
		engines:         map[model.PIDType][]*Engine{},
		activators:      map[model.PIDType]*socketActivatorProcess{},
		initialsyncwait: 2 * time.Second,
	}
	for _, opt := range opts {
		opt(f)
	}
	if f.numworkers <= 0 {
		f.numworkers = runtime.GOMAXPROCS(0)
	}
	f.workersem = semaphore.NewWeighted(int64(f.numworkers))
	// Query the available turtle finder plugins for the names of processes to
	// look for, in order to later optimize searching the processes; as we're
	// working only with a static set of plugins we only need to query the basic
	// information once.
	namegivers := plugger.Group[detector.Detector]().PluginsSymbols()
	engineplugins := make([]enginePlugin, 0, len(namegivers))
	for _, namegiver := range namegivers {
		engineplugins = append(engineplugins, enginePlugin{
			names:      namegiver.S.EngineNames(),
			detector:   namegiver.S,
			pluginname: namegiver.Plugin,
		})
	}
	f.engineplugins = engineplugins
	log.Infof("available engine process detector plugins: %s",
		strings.Join(plugger.Group[detector.Detector]().Plugins(), ", "))
	// Query the available activator finder plugins.
	activators := plugger.Group[activator.Detector]().PluginsSymbols()
	activatorplugins := make([]activatorPlugin, 0, len(activators))
	for _, activator := range activators {
		activatorplugins = append(activatorplugins, activatorPlugin{
			name:       activator.S.Name(),
			pluginname: activator.Plugin,
		})
	}
	log.Infof("available socket activator detector plugins: %s",
		strings.Join(plugger.Group[activator.Detector]().Plugins(), ", "))
	f.activatorplugins = activatorplugins
	return f
}

// Containers returns the current container state of (alive) containers from all
// discovered container engines.
func (f *TurtleFinder) Containers(
	ctx context.Context, procs model.ProcessTable, pidmap model.PIDMapper,
) []*model.Container {
	// Do some quick housekeeping first: remove engines (watchers) whose
	// processes have vanished. Also remove vanished socket activators like
	// "systemd" in containers.
	f.prune(procs)
	// Then look for new engine processes and/or socket activators.
	f.update(ctx, procs)
	// Now query the available engines for containers that are alive...
	f.mux.Lock()
	allEngines := make([]*Engine, 0, len(f.engines))
	for _, engines := range f.engines {
		// create copies of the engine objects in order to not trash the
		// original engine objects.
		allEngines = append(allEngines, slices.Clone(engines)...)
	}
	f.mux.Unlock()
	allcontainers := []*model.Container{}
	if len(allEngines) == 0 {
		return allcontainers
	}
	// Feel the heat and query the engines in parallel; to collect the results
	// we use a buffered channel of the size equal the number of engines to
	// query. Please note that the number of parallel engine queries is bounded
	// over *all parallel calls* to this method, and not just within a single
	// call.
	log.Infof("consulting %d container engines ... in parallel", len(allEngines))
	enginecontainers := make(chan []*model.Container, len(allEngines))
	var theendisnear atomic.Int64 // track amount of engine results
	theendisnear.Add(int64(len(allEngines)))
	for _, engine := range allEngines {
		if err := f.workersem.Acquire(ctx, 1); err != nil {
			return allcontainers
		}
		go func(engine *Engine) {
			defer f.workersem.Release(1)
			containers := engine.Containers(ctx)
			enginecontainers <- containers
			if theendisnear.Add(-1) > 0 {
				return
			}
			close(enginecontainers)
		}(engine)
	}
	// Wait for all engine results to come in one after another and the engine
	// result channel to finally close for good.
	for containers := range enginecontainers {
		allcontainers = append(allcontainers, containers...)
	}
	// Fill in the engine hierarchy, if necessary: note that we can't use this
	// without knowing the containers and especially their names.
	stackEngines(allcontainers, allEngines, procs)

	return allcontainers
}

// Close closes all resources associated with this turtle finder. This is an
// asynchronous process. Make sure to also cancel or have already cancelled the
// context
func (f *TurtleFinder) Close() {
	f.mux.Lock()
	defer f.mux.Unlock()
	for _, engines := range f.engines {
		for _, engine := range engines {
			engine.Close()
		}
	}
	f.engines = nil
}

// Engines returns information about the container engines currently being
// monitored.
func (f *TurtleFinder) Engines() []*model.ContainerEngine {
	f.mux.Lock()
	defer f.mux.Unlock()
	allEngines := make([]*model.ContainerEngine, 0, len(f.engines))
	for _, engines := range f.engines {
		for _, engine := range engines {
			select {
			case <-engine.Done:
				continue // already Done, so ignore this engine.
			default:
				// not Done, so let's move on and add it to the list of available
				// engines.
			}
			allEngines = append(allEngines, &model.ContainerEngine{
				ID:      engine.ID,
				Type:    engine.Type(),
				Version: engine.Version,
				API:     engine.API(),
				PID:     model.PIDType(engine.PID()),
			})
		}
	}
	return allEngines
}

// EngineCount returns the number of container engines currently under watch.
// Callers might want to use the Engines method instead as EngineCount bases on
// it (because we don't store an explicit engine count anywhere).
func (f *TurtleFinder) EngineCount() int {
	f.mux.Lock()
	defer f.mux.Unlock()
	return len(f.engines)
}

// prune any terminated watchers, either because the watcher terminated itself
// or we can't find the associated engine process anymore. This covers both
// engines once detected by their well-known process names, as well as engines
// detected to be socket-activated.
//
// Also prune any socket activator processes that have gone missing.
func (f *TurtleFinder) prune(procs model.ProcessTable) {
	f.mux.Lock()
	defer f.mux.Unlock()
	// Prune engine watchers...
	for pid, engines := range f.engines {
		if procs[pid] != nil {
			continue
		}
		// This particular container engine process has gone, so we need to
		// remove all individual watchers for for it.
		engines = deleteAndZeroFunc(engines, func(engine *Engine) bool {
			if engine.IsAlive() {
				return false
			}
			engine.Close() // ...if not already done so.
			return true
		})
		// Update the engines (watchers) for this (albeit gone) container engine
		// process, as long as there are still watchers alive. If all watchers
		// also have gone, then remove this engine process completely from our
		// inventory.
		if len(engines) == 0 {
			delete(f.engines, pid)
			continue
		}
		f.engines[pid] = engines
	}
	// Prune socket activators...
	for pid := range f.activators {
		if procs[pid] != nil {
			continue
		}
		delete(f.activators, pid)
		// Note: socket activators do not need explicit cleanup, just don't
		// reference them anymore.
		//
		// Note: we don't forcefully delete any activated watchers, but instead
		// they should be handled through the above engine watcher pruning.
	}
}

// update our knowledge about container engines if necessary, given the current
// process table and by asking engine discovery plugins for any signs of engine
// life.
func (f *TurtleFinder) update(ctx context.Context, procs model.ProcessTable) {
	var wg sync.WaitGroup
	f.updateDaemons(ctx, procs, &wg)
	f.updateActivators(procs, &wg)
	// Wait for either all engine workload synchronizations to finish within the
	// time box or the time box to end. In both cases we'll finally proceed with
	// the discovery.
	wg.Wait()
}

// updateDaemons updates our knowledge about running container engines if
// necessary, given the current process table and by asking engine discovery
// plugins for any signs of engine life.
//
// The referenced wait group count will be increased by the number of container
// engines detected. updateDaemons will run any workload watcher creation and
// synchronization in the background on separate go routines. As soon as the
// watcher creation and synchronization fails or hits the initial
// synchronization time box, the referenced wait group will be decreased
// automatically. This ensures that waiting on the wait group will always be
// time-boxed.
func (f *TurtleFinder) updateDaemons(ctx context.Context, procs model.ProcessTable, wg *sync.WaitGroup) {
	// Look for potential signs of engine life, based on process names...
	engineprocs := []engineProcess{}
NextProcess:
	for _, proc := range procs {
		procname := proc.Name
		for engidx := range f.engineplugins {
			// We need to reference the single authoritative engine item, not a
			// loop var copy.
			engine := &f.engineplugins[engidx]
			for _, enginename := range engine.names {
				if procname != enginename {
					continue
				}
				engineprocs = append(engineprocs, engineProcess{
					proc:   proc,
					engine: engine,
				})
				continue NextProcess
			}
		}
	}
	// Next, throw out all engine processes we already know of and keep only the
	// new ones to look into them further. This way we keep the lock as short as
	// possible.
	newengineprocs := make([]engineProcess, 0, len(engineprocs))
	f.mux.Lock()
	for _, engineproc := range engineprocs {
		// Is this an engine PID we already know and watch?
		if _, ok := f.engines[engineproc.proc.PID]; ok {
			continue
		}
		newengineprocs = append(newengineprocs, engineproc)
	}
	f.mux.Unlock()
	if len(newengineprocs) == 0 {
		return
	}
	// Finally look into each new engine process: try to figure out its
	// potential API socket endpoint pathname and then try to contact the engine
	// via this (these) pathname(s). Again, we aggressively go parallel in
	// contacting new engines. This also bases on the probably sane assumptions
	// that a host isn't "infested" with tens or hundreds of container engine
	// daemons...
	wg.Add(len(newengineprocs))
	for _, engineproc := range newengineprocs {
		go func(engineproc engineProcess) {
			defer wg.Done()
			log.Debugf("scanning new potential engine process %s (%d) for API endpoints...",
				engineproc.proc.Name, engineproc.proc.PID)
			// Does this process have any listening unix sockets that might act as
			// API endpoints?
			apisox := discoverAPISocketsOfProcess(engineproc.proc.PID)
			if apisox == nil {
				log.Debugf("process %d no API endpoint found", engineproc.proc.PID)
				return
			}
			// Translate the API pathnames so that we can access them from our
			// namespace via procfs wormholes; to make this reliably work we need to
			// evaluate paths for symbolic links...
			for idx, apipath := range apisox {
				wormhole := "/proc/" + strconv.FormatUint(uint64(engineproc.proc.PID), 10) +
					"/root"
				apipath, err := procfsroot.EvalSymlinks(apipath, wormhole, procfsroot.EvalFullPath)
				if err != nil {
					log.Warnf("invalid API endpoint at %s in the context of %s",
						apipath, wormhole)
					apisox[idx] = ""
					continue
				}
				apisox[idx] = wormhole + apipath
			}
			// Ask the contexter to give us a long-living engine workload
			// watching context; just using the background context (or even a
			// request's context) will be a bad idea as it doesn't give the
			// users of a Turtlefinder the means to properly spin down workload
			// watchers when retiring a Turtlefinder.
			enginectx := f.contexter()
			for _, w := range engineproc.engine.detector.NewWatchers(enginectx, engineproc.proc.PID, apisox) {
				// We've got a new watcher! Or two *snicker*
				startWatch(enginectx, w, f.initialsyncwait)
				eng := NewEngine(enginectx, w)
				f.mux.Lock()
				f.engines[engineproc.proc.PID] = append(f.engines[engineproc.proc.PID], eng)
				f.mux.Unlock()
			}
		}(engineproc)
	}
}

func (f *TurtleFinder) updateActivators(procs model.ProcessTable, wg *sync.WaitGroup) {
	// Look for potential signs of socket activators, based on their process names...
	activatorprocs := []*model.Process{}
NextProcess:
	for _, proc := range procs {
		procName := proc.Name
		for actidx := range f.activatorplugins {
			if procName != f.activatorplugins[actidx].name {
				continue
			}
			activatorprocs = append(activatorprocs, proc)
			continue NextProcess
		}
	}
	// Update our map of socket activators in one go, under lock...
	f.mux.Lock()
	for _, activatorproc := range activatorprocs {
		if _, ok := f.activators[activatorproc.PID]; ok {
			continue
		}
		log.Infof("found new socket activator process '%s' with PID %d",
			activatorproc.Name, activatorproc.PID)
		f.activators[activatorproc.PID] = newSocketActivator(activatorproc,
			f.initialsyncwait,
			f.contexter,
			func(w watcher.Watcher, pid model.PIDType) {
				// As this comes in from a different "background" go routine, we
				// need to make sure that we're not trashing our engine map.
				f.mux.Lock()
				defer f.mux.Unlock()
				f.engines[pid] = []*Engine{
					NewEngine(f.contexter(), w),
				}
			},
		)
	}
	f.mux.Unlock()
	// Now iterate over all the socket activators currently known and tell them
	// to update: the activators are responsible for discovering (new)
	// activatable API endpoints and creating new watchers as necessary, hiding
	// the more complex activation and discovery mechanism. New watchers are
	// then reported via the createdWatcherFn callback function registered above
	// when we created new socket activator (proxy) objects.
	for _, activator := range f.activators {
		activator.update(wg)
	}
}
