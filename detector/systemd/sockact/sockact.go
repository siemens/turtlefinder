// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package sockact

import (
	"context"

	"github.com/thediveo/lxkns/model"
	"github.com/thediveo/whalewatcher/watcher"
)

type ActivationSocket interface {
	Suffix() string
	NewWatcher(ctx context.Context, pid model.PIDType, api string) watcher.Watcher
}
