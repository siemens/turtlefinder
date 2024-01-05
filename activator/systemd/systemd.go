// (c) Siemens AG 2024
//
// SPDX-License-Identifier: MIT

package systemd

import (
	"github.com/siemens/turtlefinder/activator"
	"github.com/thediveo/go-plugger/v3"
)

// Register this systemd socket service activator discovery plugin. This
// statically ensures that the Detector interface is fully implemented.
func init() {
	plugger.Group[activator.Detector]().Register(
		&Detector{}, plugger.WithPlugin("systemd"))
}

type Detector struct{}

// Name returns the process name for systemd to look for.
func (a *Detector) Name() string { return "systemd" }
