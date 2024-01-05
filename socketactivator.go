// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/siemens/turtlefinder/activator"
	"github.com/thediveo/go-plugger/v3"
	"github.com/thediveo/lxkns/log"
	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/procfsroot"
	"github.com/thediveo/whalewatcher/watcher"
)

// socketActivatorProcess keeps track of the socket activation configuration and
// state of a process that does socket activation, such as “systemd”.
//
// Socket activators activate the well-known sockets they find and activate
// them, creating new watchers (as part of [Engine] objects) that connect to
// these sockets. Note that these watchers by their very activity of
// continuously watching workloads will keep the corresponding container engines
// alive.
//
// The watchers are life-cycle managed in the same way as for well-known process
// engines, using Engine objects and removing them when the watch ends when the
// engine process terminates (which it normally shouldn't).
type socketActivatorProcess struct {
	proc                 *model.Process                             // activator process.
	demonDetectorPlugins []*demonFinderPlugin                       // static list of socket-activated engine plugins.
	initialsyncwait      time.Duration                              // max. wait for engine watch coming online (sync) before proceeding.
	contexter            Contexter                                  // contexts for workload watching.
	createdWatcherFn     func(w watcher.Watcher, pid model.PIDType) // callback for newly created engine workload watchers

	mu       sync.Mutex          // protects the following fields
	hash     uint64              // xxhash over socket fds to detect reconfigurations.
	observed map[uint64]struct{} // sockets we processes one way or another and we should thus ignore.
}

// daemonFinderPlugin represents the information for identifying a
// socket-activatable container engine and creating suitable workload watchers.
type demonFinderPlugin struct {
	ident      activator.EngineIdentification
	finder     activator.EngineFinder
	pluginname string // for housekeeping and logging
}

var muDaemonDetectorPlugins sync.Mutex        // protects the next variable
var demonDetectorPlugins []*demonFinderPlugin // cached list of plugins

// newSocketActivator returns a new socketActivator and runs an initial
// discovery on it at the same time.
//
// Note: socketActivator objects do not need any explicit cleanup, just drop
// them onto the floor.
func newSocketActivator(
	proc *model.Process,
	initialsyncwait time.Duration,
	contexter Contexter,
	createdWatcherFn func(w watcher.Watcher, pid model.PIDType),
) *socketActivatorProcess {
	// If not done so yet, build a list of demon detectors for detecting
	// container engine processes based not only on their process name, but also
	// their listening API endpoint (unix domain socket).
	muDaemonDetectorPlugins.Lock()
	logPlugins := false
	detectorPlugins := demonDetectorPlugins
	if detectorPlugins == nil {
		demonfinders := plugger.Group[activator.EngineFinder]().PluginsSymbols()
		detectorPlugins = make([]*demonFinderPlugin, 0, len(demonfinders))
		for _, demonfinder := range demonfinders {
			ident := demonfinder.S.Ident()
			ident.APIEndpointSuffix = "/" + ident.APIEndpointSuffix
			detectorPlugins = append(detectorPlugins, &demonFinderPlugin{
				ident:      ident,
				finder:     demonfinder.S,
				pluginname: demonfinder.Plugin,
			})
		}
		demonDetectorPlugins = detectorPlugins
		logPlugins = true
	}
	muDaemonDetectorPlugins.Unlock()
	if logPlugins {
		log.Infof("available socket-activated engine process detector plugins: %s",
			strings.Join(plugger.Group[activator.EngineFinder]().Plugins(), ", "))
	}
	s := &socketActivatorProcess{
		proc:                 proc,
		demonDetectorPlugins: detectorPlugins,
		initialsyncwait:      initialsyncwait,
		contexter:            contexter,
		createdWatcherFn:     createdWatcherFn,
		observed:             map[uint64]struct{}{},
	}
	return s
}

// update scans this socket activator for newly appeared and well-known
// listening sockets for container engine APIs and then creates new workload
// watchers as necessary. When creating new workload watchers, it'll increase
// the wait group count as to allow the caller to wait for (the time-boxed)
// initial workload synchronization. The wait group counter will reach zero at
// the end of the time box, even if some workload synchronization might still be
// ongoing in the background. This is on purpose in order to not stall
// discoveries for too long in face of newly discovered container engines.
func (s *socketActivatorProcess) update(wg *sync.WaitGroup) {
	rawsox, hash, err := s.rawSocketFdsWithHash()
	if err != nil {
		log.Errorf("cannot update socket activator state, reason: %s", err.Error())
		return
	}
	newapis := s.discoverAPIPaths(rawsox, hash)
	if newapis == nil {
		return
	}
	s.activateAndWatch(
		newapis,
		wg,
		func(w watcher.Watcher, err error) {
			if err != nil || s.createdWatcherFn == nil {
				return
			}
			s.createdWatcherFn(w, model.PIDType(w.PID()))
		},
	)
}

// rawSocketFdsWithHash returns a list of sockets this socket activator process
// currently has open, together with a hash value calculated from the socket fd
// and socket inode numbers. The hash can be used to detect changes in the
// fd-socket configuration.
func (s *socketActivatorProcess) rawSocketFdsWithHash() (rawsocketfds []rawSocketFd, hash uint64, err error) {
	rawsocketfds, err = rawSocketFdsOfProcess("", s.proc.PID)
	if err != nil {
		return nil, 0, err
	}

	d := xxhash.New()
	for _, rawsocketfd := range rawsocketfds {
		_, _ = d.WriteString(rawsocketfd.fd)
		_, _ = d.WriteString(rawsocketfd.socketino)
	}
	return rawsocketfds, d.Sum64(), nil
}

// discoverAPIPaths prunes and updates the known activator socket map, returning
// a map of newly found API endpoint paths and their inode numbers.
func (s *socketActivatorProcess) discoverAPIPaths(rawsocketfds []rawSocketFd, hash uint64) socketPathsByIno {
	s.mu.Lock()
	if hash == s.hash {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	sox := listeningUDSPaths(rawsocketfds, listeningUDSVisibleToProcess(s.proc.PID))

	s.mu.Lock()
	defer s.mu.Unlock()
	if hash == s.hash { // bad luck: someone else was faster...
		return nil
	}
	s.hash = hash
	// prune our map of "observed" listening sockets...
	for ino := range s.observed {
		if _, ok := sox[ino]; ok {
			continue
		}
		delete(s.observed, ino)
	}

	// ...and get only the newly discovered listening socket paths.
	newpaths := socketPathsByIno{}
	for ino, soxpath := range sox {
		if _, ok := s.observed[ino]; ok {
			continue
		}
		s.observed[ino] = struct{}{} // immediately block so no double watcher creation
		newpaths[ino] = soxpath
	}
	return newpaths
}

// activateAndWatch takes a bunch of newly discovered container engine API
// endpoints and then tries to activate the serving container engines and attach
// new workload watchers to these container engines. It'll return as soon as all
// necessary activation and workload synchronization steps have been started in
// the background, incrementing the specified wait group by the number of
// container engines in the process of being activated and watched.
//
// Please note that the “outcomefn” is called asynchronously at “any” later time
// from another go routine than the caller's go routine. The reason is that
// activating the container engine serving an API endpoint might take some time,
// as well as creating a new watcher for it. activateAndWatch might well have
// returned by then.
func (s *socketActivatorProcess) activateAndWatch(
	apis socketPathsByIno,
	wg *sync.WaitGroup,
	outcomefn func(w watcher.Watcher, err error),
) {
	// Note: the API endpoint paths are relative to the mount namespace of this
	// socket activator. In order to always correctly access them even when
	// we're in a different mount namespace (that is, container), we need to go
	// through the proc filesystem "root" element "wormholes".
	wormhole := "/proc/" + strconv.FormatUint(uint64(s.proc.PID), 10) + "/root"
	for ino, api := range apis {
		idx := slices.IndexFunc(s.demonDetectorPlugins, func(f *demonFinderPlugin) bool {
			return strings.HasSuffix(api, f.ident.APIEndpointSuffix)
		})
		if idx < 0 {
			continue
		}
		api, err := procfsroot.EvalSymlinks(api, wormhole, procfsroot.EvalFullPath)
		if err != nil {
			log.Errorf("invalid API endpoint path %s in context of %s",
				api, wormhole)
			continue
		}
		api = wormhole + api
		wg.Add(1)
		ctx := s.contexter()
		go func(ino uint64, api string, enginename string, creatorfn func(apipath string, pid model.PIDType) (watcher.Watcher, error)) {
			defer wg.Done()
			activateAndStartWatch(
				ctx,
				api,
				ino,
				s.proc.PID,
				enginename,
				creatorfn,
				outcomefn,
				s.initialsyncwait,
			)
		}(ino, api,
			s.demonDetectorPlugins[idx].ident.ProcessName,
			func(apipath string, pid model.PIDType) (watcher.Watcher, error) {
				return s.demonDetectorPlugins[idx].finder.NewWatcher(ctx, pid, apipath), nil
			})
	}
}
