// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package unsorted

import "os"

// ReadDir reads the specified directory, returning all its directory entries,
// but not taking the time to sort them. It complements the stdlib's
// [io.ReadDir] (see also the [go-nuts] discussion).
//
// [go-nuts]:
// https://groups.google.com/g/golang-nuts/c/Q7hYQ9GdX9Q/m/fwYRMIbNDgsJ
func ReadDir(name string) ([]os.DirEntry, error) {
	d, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadDir(-1)
}
