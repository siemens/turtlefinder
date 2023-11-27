// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"os"
	"strconv"
	"strings"

	"github.com/siemens/turtlefinder/unsorted"
	"github.com/thediveo/lxkns/model"
)

// findDaemon finds the (socket-activated) child process that services the
// specified (unix domain) socket, returning the process PID. If a suitable
// child process cannot be found, a zero PID is returned instead.
//
// In order to slightly optimize, findDaemon only looks at the direct child
// processes of the additionally specified parent, or socket activator.
//
// Unfortunately, we have to take this longer route, as the peer credentials
// returned when connecting to a daemon API socket are specifying the PID of the
// socket activator, not the PID of the daemon. It could have been so easy...
//
// Now, the proc filesystem really isn't about hierarchy but instead provides a
// flat view. While Linux since kernel 3.5 has a “children” pseudo element that
// lists the children task TIDs of a task, the [proc(5) man page] explicitly
// warns that it can be used reliably only then all child tasks have been
// frozen; that's exactly what we cannot have or do in our situation. So we have
// to resort to the somewhat jumbled search in the order of what we get
// presented by the proc filesystem in combination with Go's stdlib directory
// read function.
//
// Since we're dealing with socket activated processes, the daemon process we're
// looking for is most probably not yet included in the recent process
// discovery. So we unfortunately need to run a new (somewhat limited)
// discovery, were the try to do as few as possible in parsing the proc
// filesystem stat(us) information of processes.
//
// And finally, how's that old adage? “Don't call it daemon”
//
// [proc(5) man page]: https://man7.org/linux/man-pages/man5/proc.5.html
func findDaemon(ppid model.PIDType, name string, udsino uint64) model.PIDType {
	// It's quicker to compare the fd (pseudo) link target strings than to parse
	// each one individually and converting them to numbers.
	sockettext := "socket:[" + strconv.FormatUint(udsino, 10) + "]"
	// In the same vein, a string compare for the PPID is simpler than all the
	// string to number conversions...
	ppidtext := strconv.FormatInt(int64(ppid), 10) + " "

	pids, err := unsorted.ReadDir("/proc")
	if err != nil {
		return 0
	}
	for _, pid := range pids {
		base := "/proc/" + pid.Name() + "/"
		stat, err := os.ReadFile(base + "stat")
		if err != nil {
			continue
		}
		if !processStatusMatch(string(stat), name, ppidtext) {
			continue
		}
		// Now check that it is in fact the correct daemon process, that is, the
		// one that serves the specified (listening) unix domain socket...
		fdbase := base + "fd"
		fds, err := unsorted.ReadDir(fdbase)
		if err != nil {
			continue
		}
		fdbase += "/"
		for _, fd := range fds {
			link, err := os.Readlink(fdbase + fd.Name())
			if err != nil {
				continue
			}
			if link != sockettext {
				continue
			}
			// It's a match, but now we need to return the PID...
			pid, err := strconv.Atoi(pid.Name())
			if err != nil {
				return 0
			}
			return model.PIDType(pid)
		}
	}
	return 0
}

// processStatusMatch takes a proc filesystem process “stat” line and checks it
// against the sought-after process name with the specified PPID (in text format
// for reasons of speed, so we don't need text-to-int conversions), returning
// true for a match, false otherwise.
func processStatusMatch(statline string, name string, ppidtext string) bool {
	// Get the process name, or "comm" field #2 and check if it is the name
	// we're looking for...
	idx := strings.Index(statline, " ")
	if idx < 0 {
		return false
	}
	// skip " (" that embraces field #2 and look for the last closing
	// bracket (note that process names may contain closing brackets
	// themselves, fwiw)...
	idx += 2
	lastidx := strings.LastIndex(statline[idx:], ")")
	if lastidx < 0 {
		return false
	}
	if statline[idx:idx+lastidx] != name {
		return false
	}
	// ...with the correct parent process...? For this, we need to look at
	// field #4, skipping the "state" field.
	idx += lastidx + 2
	if idx > len(statline) {
		return false
	}
	lastidx = strings.Index(statline[idx:], " ")
	if lastidx < 0 {
		return false
	}
	return strings.HasPrefix(statline[idx+lastidx+1:], ppidtext)
}
