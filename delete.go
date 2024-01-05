// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import "golang.org/x/exp/slices"

// deleteAndZeroFunc is like slices.DeleteFunc, but sets the remaining now
// unused elements to zero. This serves as a stop-gap measure until implemented
// https://github.com/golang/go/issues/63393 finally trickles down to us as part
// of two Go releases.
func deleteAndZeroFunc[S ~[]E, E any](s S, del func(E) bool) S {
	i := slices.IndexFunc(s, del)
	if i == -1 {
		return s
	}
	for j := i + 1; j < len(s); j++ {
		if v := s[j]; !del(v) {
			s[i] = v
			i++
		}
	}
	var zero E
	for j := i; j < len(s); j++ {
		s[j] = zero
	}
	return s[:i]
}
