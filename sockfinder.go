// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unsafe"

	"github.com/siemens/turtlefinder/unsorted"
	"github.com/thediveo/lxkns/model"
)

// soAcceptCon is the state bit mask to identify listening unix domain sockets.
// See also:
// https://elixir.bootlin.com/linux/v5.0.3/source/include/uapi/linux/net.h#L56.
const soAcceptCon = 1 << 16

// sockStream is the type enumeration value for a connection-oriented/streaming
// (unix domain) socket. See also:
// https://elixir.bootlin.com/linux/v5.0.3/source/include/linux/net.h#L64.
const sockStream = 1

// Index numbers of fields in /proc/[PID]/net/unix; please see also:
// https://man7.org/linux/man-pages/man5/proc.5.html, and the section about
// /proc/net/unix in particular.
const (
	netUnixNumField      = iota //nolint:unused
	netUnixRefCountField        //nolint:unused
	netUnixProtocolField        //nolint:unused
	netUnixFlagsField           //
	netUnixTypeField            //
	netUnixStField              //nolint:unused
	netUnixInodeField           //
	netUnixPathField            //
)

// socketFdPrefix is the prefix of the string returned when readlink-ing a file
// descriptor pseudo symlink of a process. The prefix is followed by the inode
// number of the socket, and a final closing square bracket. We define the
// prefix and its length here for once and all, so we aren't scattering around
// the same stuff in different places wherever we need it.
//
// See also: https://man7.org/linux/man-pages/man5/proc.5.html, description of
// the “/proc/pid/fd/” subdirectories.
const socketFdPrefix = "socket:["
const socketFdPrefixLen = len(socketFdPrefix)

// discoverAPISocketsOfProcess returns a list of listening unix domain sockets
// for a specific process that might be API endpoints. The PID of the process
// must be valid in the current mount namespace and a correct proc filesystem
// must have been (re)mounted in this mount namespace, otherwise only an empty
// list will be returned. The easiest way is to do this with a PID valid in the
// initial PID namespace and with a correct proc in the current mount namespace
// that has full "host:pid" view.
func discoverAPISocketsOfProcess(pid model.PIDType) []string {
	var listeningUDS = listeningUDSVisibleToProcess(pid)
	return listeningUDSPathsOfProcess(pid, listeningUDS)
}

// rawSocketFd represents a particular fd and the socket inode it references,
// still in “raw” string format. This allows us to use this information in
// situations where “cooking” or converting this information into their numbers
// would be just wasting CPU time, when we can do a wholly stupid string compare
// instead.
type rawSocketFd struct {
	fd        string // fd number as string
	socketino string // formatted as just the ino number
}

// rawSocketFdsOfProcess returns the list of sockets with their file
// descriptors, as used by the process with the specified PID. The returned
// sockets can be of arbitrary type at this point, so this will return not only
// unix domain sockets, but also, for instance, IP sockets, NETLINK sockets, as
// well as various other flavors.
//
// rawSocketFdsOfProcess is used in the context of socket activators where we
// first want to find out as quickly as possible which sockets an activator
// currently has open (unfortunately, regardless of their type and mode). Thus,
// we don't do any string-to-number conversions, just raw string processing.
func rawSocketFdsOfProcess(procfs string, pid model.PIDType) ([]rawSocketFd, error) {
	// We're going for the file descriptor pseudo symlink entries in the proc
	// filesystem of a particular process; see also
	// https://man7.org/linux/man-pages/man5/proc.5.html. In case of sockets
	// these pseudo symlinks won't reference anything in the VFS, but instead
	// reveal the type of thing referenced by an fd entry and its inode number.
	// Unfortunately, they don't reveal whether a particular socket is in
	// listening state or not.
	fdbase := procfs + "/proc/" + strconv.FormatUint(uint64(pid), 10) + "/fd"
	fds, err := unsorted.ReadDir(fdbase)
	if err != nil {
		return nil, fmt.Errorf("cannot determine fds for process with PID %d, reason: %w", pid, err)
	}
	sockets := make([]rawSocketFd, 0, len(fds))
	fdbase += "/"
	for _, fd := range fds {
		linksto, err := os.Readlink(fdbase + fd.Name())
		if err != nil {
			continue
		}
		if !strings.HasPrefix(linksto, socketFdPrefix) || len(linksto) == socketFdPrefixLen {
			continue
		}
		sockets = append(sockets, rawSocketFd{
			fd:        fd.Name(),
			socketino: linksto[socketFdPrefixLen : len(linksto)-1], // always assuming the procfs isn't returning crappy entries.
		})
	}
	return sockets, nil
}

// socketPathsByIno maps the inode numbers of (unix domain) sockets to their
// corresponding path names. This map will not contain unix domain sockets from
// Linux' "abstract namespace" (see also:
// http://man7.org/linux/man-pages/man7/unix.7.html).
type socketPathsByIno map[uint64]string

// listeningUDSPathsOfProcess returns the paths of listening unix domain sockets
// (“UDS”) for the process specified by its PID. For this, it scans the open
// file descriptors ("fd") of the specified process, looking for known listening
// unix domain sockets in the map of inode numbers to socket paths, as passed in
// listeningUDS.
//
// The PID specified must be correct for the procfs instance mounted for the
// calling process (or task).
func listeningUDSPathsOfProcess(pid model.PIDType, listeningUDS socketPathsByIno) (socketpaths []string) {
	// We're going for the file descriptor pseudo symlink entries in the proc
	// filesystem of a particular process; see also
	// https://man7.org/linux/man-pages/man5/proc.5.html. In case of sockets
	// these pseudo symlinks won't reference anything in the VFS, but instead
	// reveal the type of thing referenced by an fd entry and its inode number.
	fdbase := "/proc/" + strconv.FormatUint(uint64(pid), 10) + "/fd"
	fdentries, err := unsorted.ReadDir(fdbase)
	if err != nil {
		return
	}
	fdbase += "/"
	// Scan all directory entries below the process's /proc/[PID]/fd directory:
	// these represent the individual open file descriptors of this process.
	// They are links (rather: pseudo-symbolic links) to their corresponding
	// resources, such as file names, sockets, et cetera. For sockets, we can
	// only learn a socket's inode number, but neither its type, nor state. Thus
	// we need the sockets-by-inode dictionary to check whether a fd references
	// something of interest to us and the filesystem path it points to (as
	// usual, subject to the current mount namespace).
	for _, fdentry := range fdentries {
		fdlink, err := os.Readlink(fdbase + fdentry.Name())
		if err != nil || !strings.HasPrefix(fdlink, socketFdPrefix) {
			continue
		}
		ino, err := strconv.ParseUint(fdlink[socketFdPrefixLen:len(fdlink)-1], 10, 64)
		if err != nil {
			continue
		}
		soxpath, ok := listeningUDS[ino]
		if !ok {
			continue
		}
		socketpaths = append(socketpaths, soxpath)
	}
	return
}

// listeningUDSPaths takes the raw socket fd information and filters it against
// the known listening unix domain sockets (in “listeningUDS”), returning only
// the sockets from the rawSocketFd list that are listening. The listening
// sockets are then returned as a map from (socket) inode numbers to their
// associated filesystem paths.
//
// This function is similar to what listeningUDSPathsOfProcess does, but it
// instead works on a list of “raw” socket fd information, instead of scanning
// the proc filesystem itself. It is used in the context of socket activator
// scanning, whereas listeningUDSPathsOfProcess is instead used in the context
// of “always-on” container engine process detection.
func listeningUDSPaths(rawfds []rawSocketFd, listeningUDS socketPathsByIno) socketPathsByIno {
	listening := socketPathsByIno{}
	for _, rawsockfd := range rawfds {
		ino, err := strconv.ParseUint(rawsockfd.socketino, 10, 64)
		if err != nil {
			continue
		}
		soxpath, ok := listeningUDS[ino]
		if !ok {
			continue
		}
		listening[ino] = soxpath
	}
	return listening
}

// listeningUDSVisibleToProcess returns a map of (named) unix domain sockets in
// listening state in the mount namespace to which the specified process is
// attached to. The map specifies for each listening unix domain socket both its
// inode number as the key and its path as value.
func listeningUDSVisibleToProcess(pid model.PIDType) socketPathsByIno {
	sox := socketPathsByIno{}
	// Try to open the list of unix domain sockets currently present in the
	// system.
	//
	// Note 1: please note that this list is subject to mount namespace this
	// process is joined to. Some documentation and blog posts erroneously
	// indicate that this list is controlled by the current network namespace
	// (as "/proc/net/" might suggest), but without ever checking. However, when
	// thinking about it, this doesn't make any sense at all, as unix domain
	// sockets have names that are filesystem paths, so it does make sense that
	// the mount namespace gets control, but not the network namespace. /rant
	//
	// Note 2: lesser known, files in a different mount namespaces can be
	// directly accessed via the proc filesystem if there's a process attached
	// to the mount namespace. These wormholes are "/proc/[PID]/root/" and
	// predate Linux mount namespaces by quite some eons, dating back to
	// "chroot". The wormholes save us from needing to re-execute in order to
	// access a container engine API endpoint in a different mount namespace.
	// This improves performance, as we can keep in-process and even
	// aggressively parallelize talking to engines.
	//
	// It's "incontinentainers", after all.
	netunixf, err := os.Open("/proc/" + strconv.FormatUint(uint64(pid), 10) +
		"/net/unix")
	if err != nil {
		return nil
	}
	defer netunixf.Close()
	// Each line from /proc/[PID]/net/unix lists one socket with its state
	// ("flags"), type, etc. For precise field semantics, please see:
	// https://elixir.bootlin.com/linux/v5.0.3/source/net/unix/af_unix.c#L2831
	// -- this line of code generates a single line in /proc/net/unix. In
	// particular, this is "%pK: %08X %08X %08X %04X %02X %5lu". Pay special
	// attention to the field width(s). Please note that the "Path" field is not
	// included in the formatting string.
	socketscanner := bufio.NewScanner(netunixf)
	for socketscanner.Scan() {
		// For increased "fun", the Linux kernel sometimes separates at least
		// some fields by multiple(!) whitespaces when their field contents are
		// "narrow". For instance, the inode number is smaller than 5 digits
		// ("%5lu"). Now, many inode numbers are actually 6 digits and more, so
		// this padding might be some ancient leftover. Yet, it laid a trap
		// undetected in this code for multiple years. The fix: use
		// strings.Fields instead of strings.Split.
		//
		// Note: we're now painting racing stripes on our Gophers: in order to
		// speed up parsing by roughly 25% and almost halving the number of
		// required allocations (and reducing memory consumption by almost 33%)
		// we use the non-allocating Scanner.Bytes() method and then convert the
		// resulting byte slice into a string without allocating and copying the
		// line contents. The string is basically aliasing the byte slice and we
		// thus must not keep any substrings around that survive a single loop.
		// For this reason we clone the unix domain socket path and only return
		// this deep copy.
		//
		// For more background information, please see also:
		//  - using non-allocating Scanner.Bytes(): https://stackoverflow.com/a/64643397
		//  - deep cloning a string: https://stackoverflow.com/a/68972665
		//  - issue #53003: unsafe: add StringData, String, SliceData;
		//    https://github.com/golang/go/issues/53003
		fields := strings.Fields(asString(socketscanner.Bytes()))
		if len(fields) <= netUnixPathField {
			continue
		}
		// Ignore sockets from the "abstract namespace" (yet another namespace,
		// totally unrelated to the Linux kernel namespaces described in
		// https://man7.org/linux/man-pages/man7/namespaces.7.html).
		if fields[netUnixPathField] != "" && fields[netUnixPathField][0] == '@' {
			continue
		}
		flags, err := strconv.ParseUint(fields[netUnixFlagsField], 16, 32)
		if err != nil { // also skips header line
			continue
		}
		soxtype, err := strconv.ParseUint(fields[netUnixTypeField], 16, 16)
		if err != nil {
			continue
		}
		// A string "deep" copy avoids the whole text line hanging around as
		// long as someone keeps the path result alive, see also:
		// https://stackoverflow.com/a/68972665
		path := strings.Clone(fields[netUnixPathField])
		// If this ain't ;) a unix socket in listening mode, then skip it.
		if soxtype != sockStream || flags != soAcceptCon {
			continue
		}
		ino, err := strconv.ParseUint(fields[netUnixInodeField], 10, 64)
		if err != nil {
			continue
		}
		sox[ino] = path // finally map the socket's inode number to its path.
	}
	return sox
}

// asString returns a string for the specified byte slice, without allocating
// memory and without copying the contents. In consequence, the underlying byte
// slice must not be changed while the returned string is alive. Moreover, this
// variant of alloc-free byte slice to string conversion expects the caller to
// always pass non-nil byte slices (which is easily guaranteed in our specific
// contexts).
//
// As for the correct usage of unsafe.String please also see
// https://go101.org/article/unsafe.html.
func asString(b []byte) string { return unsafe.String(unsafe.SliceData(b), len(b)) }
