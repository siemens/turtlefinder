// (c) Siemens AG 2024
//
// SPDX-License-Identifier: MIT

package all

import (
	_ "github.com/siemens/turtlefinder/activator/systemd" // detect systemd socket activator

	_ "github.com/siemens/turtlefinder/activator/podman" // detect socket-activated podman engine
)
