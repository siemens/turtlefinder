// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package crio

import (
	"context"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDetectorCRIO(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "turtlefinder/detector/crio")
}

// fakeRuntimeService is a CRI runtime service that only answers version
// requests, optionally failing them except for the client's initial probe.
type fakeRuntimeService struct {
	runtime.UnimplementedRuntimeServiceServer
	failVersion atomic.Bool
}

func (f *fakeRuntimeService) Version(_ context.Context, req *runtime.VersionRequest) (*runtime.VersionResponse, error) {
	// The CRI client probes without a version, the watcher's Version with one.
	if f.failVersion.Load() && req.Version != "" {
		return nil, status.Error(codes.Unavailable, "no version for you")
	}
	return &runtime.VersionResponse{RuntimeName: "fake-o", RuntimeVersion: "0.0.1"}, nil
}
