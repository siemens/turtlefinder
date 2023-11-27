// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package systemd

import (
	"context"
	"net"
	"strings"
	"time"

	detect "github.com/siemens/turtlefinder/detector"
	"github.com/siemens/turtlefinder/detector/systemd/sockact"
	"golang.org/x/sys/unix"

	"github.com/thediveo/go-plugger/v3"
	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/whalewatcher/watcher"
)

// Register this systemd discovery plugin. This statically ensures that the
// Detector interface is fully implemented.
func init() {
	plugger.Group[detect.Detector]().Register(
		&Detector{}, plugger.WithPlugin("systemd"))
}

// Detector implements the detect.Detector interface. This is automatically
// type-checked by the previous plugin registration (Generics can be sweet,
// sometimes *snicker*).
type Detector struct{}

// EngineNames returns the process name of the systemd (“1”) process.
func (d *Detector) EngineNames() []string {
	return []string{"systemd"}
}

var activationSockets []sockact.ActivationSocket
var activationSocketSuffixes []string

// NewWatchers returns watchers for socket-activated container engines.
//
// Note: the PID parameter more or less useless to us as it identifies a
// particular systemd instance, but we're interested in the PIDs of the
// container engines either already socket-activated or getting socket-activated
// as we're going to pull their legs.
func (d *Detector) NewWatchers(ctx context.Context, _ model.PIDType, apis []string) []watcher.Watcher {
	if activationSockets == nil {
		activationSockets = plugger.Group[sockact.ActivationSocket]().Symbols()
		for _, as := range activationSockets {
			activationSocketSuffixes = append(activationSocketSuffixes, "/"+as.Suffix())
		}
	}
	watchers := []watcher.Watcher{}
	for _, apipathname := range apis {
		for suffidx, suffix := range activationSocketSuffixes {
			if !strings.HasSuffix(apipathname, suffix) {
				continue
			}
			pid, closer := pidOfUDS(ctx, apipathname)
			if pid == 0 {
				break
			}
			w := activationSockets[suffidx].NewWatcher(ctx, pid, apipathname)
			closer()
			if w != nil {
				watchers = append(watchers, w)
			}
			break
		}
	}
	return watchers
}

func pidOfUDS(ctx context.Context, api string) (pid model.PIDType, closer func()) {
	d := net.Dialer{}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	conn, err := d.DialContext(ctx, "unix", api)
	cancel()
	if err != nil {
		return 0, func() {}
	}
	closer = func() { conn.Close() }
	sc, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return
	}
	var ucred *unix.Ucred
	if ctrlerr := sc.Control(func(fd uintptr) {
		ucred, err = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); ctrlerr != nil || err != nil {
		return
	}
	pid = model.PIDType(ucred.Pid)
	return
}
