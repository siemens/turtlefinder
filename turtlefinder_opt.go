// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import "time"

// NewOption represents options to New when creating a new turtle finder.
type NewOption func(*TurtleFinder)

// WithWorkers sets the maximum number of parallel container engine queries on
// the same TurtleFinder. A maximum number of zero or less is taken as
// GOMAXPROCS instead. Please note that this maximum applies to all concurrent
// [TurtleFinder.Containers] calls, and not to individual
// [TurtleFinder.Containers] calls separately.
func WithWorkers(num int) NewOption {
	return func(f *TurtleFinder) {
		f.numworkers = num
	}
}

// WithGettingOnlineWait sets the maximum duration to wait for our workload view
// of a newly discovered container engine to become synchronized before
// proceeding with a container discovery. If the initial synchronisation phase
// takes longer, it won't be aborted. This option instead controls the maximum
// wait before proceeding with discovering containers from the already known
// engine workloads.
func WithGettingOnlineWait(d time.Duration) NewOption {
	return func(f *TurtleFinder) {
		f.initialsyncwait = d
	}
}
