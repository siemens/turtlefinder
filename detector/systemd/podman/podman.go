// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package podman

import (
	"context"
	"time"

	"github.com/docker/docker/client"
	"github.com/siemens/turtlefinder/detector/systemd/sockact"
	"github.com/thediveo/go-plugger/v3"
	"github.com/thediveo/lxkns/log"
	"github.com/thediveo/lxkns/model"
	mobyengine "github.com/thediveo/whalewatcher/engineclient/moby"
	"github.com/thediveo/whalewatcher/watcher"
	"github.com/thediveo/whalewatcher/watcher/moby"
)

func init() {
	plugger.Group[sockact.ActivationSocket]().Register(
		&Detector{}, plugger.WithPlugin("podman"))
}

type Detector struct{}

func (d *Detector) Suffix() string { return "podman.sock" }

func (d *Detector) NewWatcher(ctx context.Context, pid model.PIDType, api string) watcher.Watcher {
	log.Debugf("dialing podman endpoint 'unix://%s'", api)
	w, err := moby.New("unix://"+api, nil, mobyengine.WithPID(int(pid)))
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, err = w.Client().(*client.Client).Info(ctx)
	cancel()
	if ctxerr := ctx.Err(); ctxerr != nil {
		w.Close()
		log.Debugf("podman Docker API Info call context hit deadline, reason: %s", ctxerr.Error())
		return nil
	}
	if err != nil {
		w.Close()
		log.Debugf("podman Docker API endpoint 'unix://%s' failed, reason: %s", api, err.Error())
		return nil
	}
	return w
}
