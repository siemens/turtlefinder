// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package all

import (
	_ "github.com/siemens/turtlefinder/detector/containerd" // detect containerd
	_ "github.com/siemens/turtlefinder/detector/crio"       // detect cri-o
	_ "github.com/siemens/turtlefinder/detector/moby"       // detect Docker
)
