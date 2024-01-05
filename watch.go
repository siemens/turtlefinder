// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/thediveo/lxkns/log"
	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/whalewatcher/watcher"
)

const (
	findAttempts = 10
	findPolling  = 100 * time.Millisecond
)

// startWatch starts the watch on the specified watcher, shortly waiting (as
// specified) for the watcher to synchronize to the workload of the container
// engine watched. startWatch will always return after at most the specified
// maxwait duration, while any watch including the initial synchronization
// always continues on a “background” go routine.
//
// Errors are reported, “as usual”, through the watcher. The rationale is that
// they can happen anyway at any later time, so having two separate error
// reporting “channels” would just complicate matters.
//
// The idea of the maxwait duration is to allow a time-boxed synchronous
// behavior without blocking too long on slow engines. This allows “typical”
// discoveries to get the discovered workload information in the same request
// where a container engine was discovered.
//
// Please note that the end of the initial synchronization phase of any watcher
// can always be determined by a watcher's ready channel as returned by Ready()
// becoming closed.
//
// startWatch emits informational log messages about the synchronization start
// and end.
func startWatch(ctx context.Context, w watcher.Watcher, maxwait time.Duration) {
	log.Infof("beginning synchronization to '%s' engine (PID %d) at API %s",
		w.Type(), w.PID(), w.API())
	// Start the watch including the initial synchronization on a separate go
	// routine and controlled by the context given to us.
	go func() {
		err := w.Watch(ctx)
		if err == nil {
			return
		}
		log.Warnf("terminated watch for '%s' container engine (PID %d), reason: %s",
			w.Type(), w.PID(), err.Error())
	}()
	// Wait in the background for the synchronization to complete and then
	// report the engine ID. The ready channel of a whale watcher also closes in
	// case of a synchronization or other error, so this transient go routine is
	// bound to terminate for any outcome sooner or later.
	go func() {
		<-w.Ready()
		// Getting the engine ID should be carried out swiftly, so we timebox
		// it.
		idctx, idcancel := context.WithTimeout(ctx, 2*time.Second)
		defer idcancel()
		log.Infof("synchronized to '%s' container engine (PID %d) with ID '%s'",
			w.Type(), w.PID(), w.ID(idctx))
	}()
	// Give the watcher a (short) chance to get in sync, but do not hang around
	// for too long if the container engine is slow...
	//
	// Oh, well: time.After is kind of hard to use without small leaks. Now, a
	// 5s timer will be GC'ed after 5s anyway, but let's do it properly for once
	// and all, to get the proper habit. For more background information, please
	// see, for instance:
	// https://www.arangodb.com/2020/09/a-story-of-a-memory-leak-in-go-how-to-properly-use-time-after/
	wecker := time.NewTimer(maxwait)
	select {
	case <-w.Ready():
		if !wecker.Stop() { // drain the timer, if necessary.
			<-wecker.C
		}
	case <-wecker.C:
		log.Warnf("'%s' container engine (PID %d) not yet synchronized ... continuing in background",
			w.Type(), w.PID())
	}
}

// activateAndStartWatch first connects to the specified API endpoint in order
// to determine the PID of the container engine serving the API. If successful,
// it creates a workload watcher and tells it to start watching the workload.
// activateAndStartWatch will always return after at most the specified maxwait
// duration. If connecting was successful, the watcher will synchronize in the
// background even after maxwait.
func activateAndStartWatch(
	ctx context.Context,
	apipath string, // path(!) within current mount namespace, not an URL.
	listeningsockino uint64,
	activatorPID model.PIDType,
	enginename string,
	creatorfn func(apipath string, pid model.PIDType) (watcher.Watcher, error),
	outcomefn func(w watcher.Watcher, err error),
	maxwait time.Duration,
) {
	// Use a buffered channel, as our consumer go routine might have already
	// moved on by the time we've through all the motions to activate the engine
	// and connect a watcher to it.
	synched := make(chan struct{}, 1)

	go func() {
		// Ensure to notify the time-boxed "outer" go routine of any outcome of
		// our attempt to activate and contact a container engine, including the
		// passed outcomefn.
		var w watcher.Watcher
		var err error
		defer func() {
			close(synched)
			outcomefn(w, err)
		}()

		// attempt a time-boxed connect to the engine's API endpoint in order to
		// determine the PID of the serving process.
		log.Infof("activating '%s' container engine at API endpoint %s",
			enginename, apipath)
		started := time.Now()
		var d net.Dialer
		connectctx, connectcancel := context.WithTimeout(ctx, maxwait)
		defer connectcancel()
		conn, err := d.DialContext(connectctx, "unix", apipath)
		if err != nil {
			log.Errorf("cannot activate container engine at API %s, reason: %s",
				apipath, err.Error())
			return
		}
		defer conn.Close()
		log.Infof("activated '%s' container engine at API endpoint %s",
			enginename, apipath)

		// next, try to find the newly activated engine process; unfortunately,
		// the API socket's peer credential won't give us the engine's PID, but
		// instead the PID of the activator (as the activator created the
		// listening API socket).
		var pid model.PIDType
	NextAttempt:
		for attempt := 1; attempt <= findAttempts; attempt++ {
			pid = findDaemon(activatorPID, enginename, listeningsockino)
			if pid != 0 {
				break
			}
			sleep := time.NewTimer(findPolling)
			select {
			case <-sleep.C:
				log.Infof("retrying to find activated '%s' container engine process for API endpoint %s",
					enginename, apipath)
			case <-ctx.Done():
				if !sleep.Stop() {
					<-sleep.C
				}
				break NextAttempt
			}
		}
		if pid == 0 {
			err = fmt.Errorf("cannot find activated container engine process '%s' for API endpoint %s",
				enginename, apipath)
			log.Errorf(err.Error())
			return
		}
		log.Infof("activated container engine process '%s' with API endpoint %s has PID %d",
			enginename, apipath, pid)

		// now attempt to create and start the watcher, also connected to the
		// API endpoint.
		w, err = creatorfn(apipath, pid)
		if err != nil {
			return
		}
		remmaxwait := maxwait - time.Since(started)
		if remmaxwait < 0 {
			remmaxwait = 0
		}
		startWatch(ctx, w, remmaxwait)
	}()

	// Time-boxed wait for the engine to get started (if not already so), then a
	// watcher to connect to it, and finally to become synchronized to the
	// engine's workload ... and simply move on if the the synchronization isn't
	// finished in a moment, but takes slightly longer, so we don't block a
	// discovery for too long.
	wecker := time.NewTimer(maxwait)
	select {
	case <-synched:
		if !wecker.Stop() {
			<-wecker.C
		}
	case <-wecker.C:
		log.Warnf("engine endpoint %s still in activation ... continuing in background", apipath)
	}
}
