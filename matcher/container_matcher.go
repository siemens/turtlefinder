// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

//go:build matchers
// +build matchers

package matcher

import (
	"fmt"

	"github.com/thediveo/lxkns/model"

	g "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
)

// HaveContainerNameID succeeds if ACTUAL is either a model.Container or
// *model.Container with the specified name or ID. Alternatively of a name/ID
// string, a GomegaMatcher can also be specified for matching the name or ID,
// such as ContainSubstring and MatchRegexp.
func HaveContainerNameID(nameorid interface{}) types.GomegaMatcher {
	var nameoridMatcher types.GomegaMatcher
	switch nameorid := nameorid.(type) {
	case string:
		nameoridMatcher = g.Equal(nameorid)
	case types.GomegaMatcher:
		nameoridMatcher = nameorid
	default:
		panic("nameorid argument must be string or GomegaMatcher")
	}
	return g.SatisfyAny(
		g.WithTransform(func(actual interface{}) (string, error) {
			switch container := actual.(type) {
			case *model.Container:
				return container.ID, nil
			case model.Container:
				return container.ID, nil
			}
			return "", fmt.Errorf("HaveContainerNameID expects a model.Container or *model.Container, but got %T", actual)
		}, nameoridMatcher),
		g.WithTransform(func(actual interface{}) (string, error) {
			switch container := actual.(type) {
			case *model.Container:
				return container.Name, nil
			case model.Container:
				return container.Name, nil
			}
			return "", fmt.Errorf("HaveContainerNameID expects a model.Container or *model.Container, but got %T", actual)
		}, nameoridMatcher),
	)
}
