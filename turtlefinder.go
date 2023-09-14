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

	"github.com/siemens/turtlefinder/detector"
	_ "github.com/siemens/turtlefinder/detector/all" // pull in engine detector plugins
	"golang.org/x/exp/slices"
	"golang.org/x/sync/semaphore"

	"github.com/thediveo/go-plugger/v3"
	"github.com/thediveo/lxkns/containerizer"
	"github.com/thediveo/lxkns/log"
	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/procfsroot"
	"github.com/thediveo/whalewatcher/watcher"
)

// Overseer gives access to information about container engines currently monitored.
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
	contexter       Contexter           // contexts for workload watching.
	engineplugins   []engineplugin      // static list of engine plugins.
	numworkers      int                 // max number of parallel engine queries.
	workersem       *semaphore.Weighted // bounded pool.
	initialsyncwait time.Duration       // max. wait for engine watch coming online (sync) before proceeding.

	mux     sync.Mutex                  // protects the following fields.
	engines map[model.PIDType][]*Engine // engines by PID; individual engines may have failed.
}

// TurtleFinder implements the lxkns Containerizer interface.
var _ containerizer.Containerizer = (*TurtleFinder)(nil)

// engineplugin represents the process names of a container engine discovery
// plugin, as well as the plugin's Discover function.
type engineplugin struct {
	names      []string          // process names of interest
	detector   detector.Detector // detector plugin interface
	pluginname string            // for housekeeping and logging
}

// engineprocess represents an individual container engine process and the
// container engine discovery plugin responsible for it.
type engineprocess struct {
	proc   *model.Process
	engine *engineplugin
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
	engineplugins := make([]engineplugin, 0, len(namegivers))
	for _, namegiver := range namegivers {
		engineplugins = append(engineplugins, engineplugin{
			names:      namegiver.S.EngineNames(),
			detector:   namegiver.S,
			pluginname: namegiver.Plugin,
		})
	}
	f.engineplugins = engineplugins
	log.Infof("available engine detector plugins: %s",
		strings.Join(plugger.Group[detector.Detector]().Plugins(), ", "))
	return f
}

// Containers returns the current container state of (alive) containers from all
// discovered container engines.
func (f *TurtleFinder) Containers(
	ctx context.Context, procs model.ProcessTable, pidmap model.PIDMapper,
) []*model.Container {
	// Do some quick housekeeping first: remove engines whose processes have
	// vanished.
	if !f.prune(procs) {
		return nil // sorry, we're closed.
	}
	// Then look for new engine processes.
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
	// Feel the heat and query the engines in parallel; to collect the results
	// we use a buffered channel of the size equal the number of engines to
	// query. Please note that the number of parallel engine queries is bounded
	// over *all parallel calls* to this method, and not just within a single
	// call.
	log.Infof("consulting %d container engines ... in parallel", len(allEngines))
	enginecontainers := make(chan []*model.Container, len(allEngines))
	allcontainers := []*model.Container{}
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
// or we can't find the associated engine process anymore.
func (f *TurtleFinder) prune(procs model.ProcessTable) bool {
	f.mux.Lock()
	defer f.mux.Unlock()
	if f.engines == nil {
		return false
	}
	for pid, engines := range f.engines {
		if procs[pid] != nil {
			continue
		}
		engines = deleteFunc(engines, func(engine *Engine) bool {
			if engine.IsAlive() {
				return false
			}
			engine.Close() // ...if not already done so.
			return true
		})
		if len(engines) == 0 {
			delete(f.engines, pid)
			continue
		}
		f.engines[pid] = engines
	}
	return true
}

// deleteFunc is like slices.DeleteFunc, but sets the remaining now unused
// elements to zero.
func deleteFunc[S ~[]E, E any](s S, del func(E) bool) S {
	i := slices.IndexFunc(s, del)
	if i == -1 {
		return s
	}
	for j := i + 1; j < len(s); j++ {
		if v := s[j]; !del(v) {
			s[i] = v
			i++
		}
	}
	var zero E
	for j := i; j < len(s); j++ {
		s[j] = zero
	}
	return s[:i]
}

// update our knowledge about container engines if necessary, given the current
// process table and by asking engine discovery plugins for any signs of engine
// life.
func (f *TurtleFinder) update(ctx context.Context, procs model.ProcessTable) {
	// Look for potential signs of engine life, based on process names...
	engineprocs := []engineprocess{}
NextProcess:
	for _, proc := range procs {
		procname := proc.Name
		for engidx, engine := range f.engineplugins {
			for _, enginename := range engine.names {
				if procname == enginename {
					engineprocs = append(engineprocs, engineprocess{
						proc:   proc,
						engine: &f.engineplugins[engidx], // ...we really don't want to address the loop variable here
					})
					continue NextProcess
				}
			}
		}
	}
	// Next, throw out all engine processes we already know of and keep only the
	// new ones to look into them further. This way we keep the lock as short as
	// possible.
	newengineprocs := make([]engineprocess, 0, len(engineprocs))
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
	var wg sync.WaitGroup
	wg.Add(len(newengineprocs))
	for _, engineproc := range newengineprocs {
		go func(engineproc engineprocess) {
			defer wg.Done()
			log.Debugf("scanning new potential engine process %s (%d) for API endpoints...",
				engineproc.proc.Name, engineproc.proc.PID)
			// Does this process have any listening unix sockets that might act as
			// API endpoints?
			apisox := discoverAPISockets(engineproc.proc.PID)
			if apisox == nil {
				log.Debugf("process %d no API endpoint found", engineproc.proc.PID)
				return
			}
			// Translate the API pathnames so that we can access them from our
			// namespace via procfs wormholes; to make this reliably work we need to
			// evaluate paths for symbolic links...
			for idx, apipath := range apisox {
				root := "/proc/" + strconv.FormatUint(uint64(engineproc.proc.PID), 10) +
					"/root"
				if p, err := procfsroot.EvalSymlinks(apipath, root, procfsroot.EvalFullPath); err == nil {
					apisox[idx] = root + p
				} else {
					log.Warnf("invalid API endpoint at %s", apipath)
					apisox[idx] = ""
				}
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
	wg.Wait()
}

// startWatch starts the watch and then shortly waits for a watcher to
// synchronize and then watches in the background (spinning off a separate go
// routine) the watcher synchronizing to its engine state, logging begin and end
// as informational messages.
func startWatch(ctx context.Context, w watcher.Watcher, maxwait time.Duration) {
	log.Infof("beginning synchronization to %s engine (PID %d) at API %s",
		w.Type(), w.PID(), w.API())
	// Start the watch including the initial synchronization...
	errch := make(chan error, 1)
	go func() {
		errch <- w.Watch(ctx)
		close(errch)
	}()
	// Wait in the background for the synchronization to complete and then
	// report the engine ID.
	go func() {
		<-w.Ready()
		// Getting the engine ID should be carried out swiftly, so we timebox
		// it.
		idctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		log.Infof("synchronized to %s engine (PID %d) with ID %s",
			w.Type(), w.PID(), w.ID(idctx))
		cancel() // ensure to quickly release cancel
	}()
	// Give the watcher a (short) chance to get in sync, but do not hang around
	// for too long...
	//
	// Oh, well: time.After is kind of hard to use without small leaks.
	// Now, a 5s timer will be GC'ed after 5s anyway, but let's do it
	// properly for once and all, to get the proper habit. For more
	// background information, please see, for instance:
	// https://www.arangodb.com/2020/09/a-story-of-a-memory-leak-in-go-how-to-properly-use-time-after/
	wecker := time.NewTimer(maxwait)
	select {
	case <-w.Ready():
		if !wecker.Stop() { // drain the timer, if necessary.
			<-wecker.C
		}
	case <-wecker.C:
		log.Warnf("%s engine (PID %d) not yet synchronized ... continuing in background",
			w.Type(), w.PID())
	}
}
