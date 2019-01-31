// Copyright 2019 Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sink

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/gogo/status"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"

	mcp "istio.io/api/mcp/v1alpha1"
	"istio.io/istio/pkg/mcp/internal/test"
	"istio.io/istio/pkg/mcp/testing/monitoring"
)

type fakeRateLimiter struct {
	waitErr chan error
}

func newFakeRateLimiter() *fakeRateLimiter {
	return &fakeRateLimiter{
		waitErr: make(chan error),
	}
}

func (f *fakeRateLimiter) Wait(ctx context.Context) error {
	select {
	case err := <-f.waitErr:
		return err
	default:
		return nil
	}
}

type serverHarness struct {
	grpc.ServerStream
	*sinkTestHarness
}

// avoid ambiguity between grpc.ServerStream and test.sinkTestHarness
func (h *serverHarness) Context() context.Context {
	return h.sinkTestHarness.Context()
}

func TestServerSinkRateLimitter(t *testing.T) {
	h := &serverHarness{
		sinkTestHarness: newSinkTestHarness(),
	}

	fakeLimiter := newFakeRateLimiter()
	authChecker := test.NewFakeAuthChecker()
	options := &Options{
		CollectionOptions: CollectionOptionsFromSlice(test.SupportedCollections),
		Updater:           h,
		ID:                test.NodeID,
		Metadata:          test.NodeMetadata,
		Reporter:          monitoring.NewInMemoryStatsContext(),
	}
	serverOpts := &ServerOptions{
		AuthChecker: authChecker,
	}
	s := NewServer(options, serverOpts)
	s.newConnectionLimiter = fakeLimiter

	// when rate limit returns an error
	errc := make(chan error)
	go func() {
		errc <- s.EstablishResourceStream(h)
	}()

	expectedErr := "something went wrong while waiting"

	fakeLimiter.waitErr <- errors.New(expectedErr)

	err := <-errc
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("Expected error from Wait: got %v want %v ", err, expectedErr)
	}

	// when rate limit is not reached
	go func() {
		errc <- s.EstablishResourceStream(h)
	}()

	h.resourcesChan <- &mcp.Resources{
		Collection: test.FakeType0Collection,
		Nonce:      "n0",
		Resources: []mcp.Resource{
			*test.Type0A[0].Resource,
		},
	}

	<-h.changeUpdatedChans
	h.mu.Lock()
	got := h.changes[test.FakeType0Collection]
	h.mu.Unlock()
	if got == nil {
		t.Fatalf("Expected to receive something: got %v", got)
	}

	h.recvErrorChan <- io.EOF

	err = <-errc
	if err != nil {
		t.Fatalf("Expected no error: got %v", err)
	}
}

func TestServerSink(t *testing.T) {
	h := &serverHarness{
		sinkTestHarness: newSinkTestHarness(),
	}

	authChecker := test.NewFakeAuthChecker()
	options := &Options{
		CollectionOptions: CollectionOptionsFromSlice(test.SupportedCollections),
		Updater:           h,
		ID:                test.NodeID,
		Metadata:          test.NodeMetadata,
		Reporter:          monitoring.NewInMemoryStatsContext(),
	}
	serverOpts := &ServerOptions{
		AuthChecker: authChecker,
	}
	s := NewServer(options, serverOpts)
	s.newConnectionLimiter = newFakeRateLimiter()

	errc := make(chan error)
	go func() {
		errc <- s.EstablishResourceStream(h)
	}()

	want := &Change{
		Collection: test.FakeType0Collection,
		Objects: []*Object{
			{
				TypeURL:  test.FakeType0TypeURL,
				Metadata: test.Type0A[0].Metadata,
				Body:     test.Type0A[0].Proto,
			},
			{
				TypeURL:  test.FakeType0TypeURL,
				Metadata: test.Type0B[0].Metadata,
				Body:     test.Type0B[0].Proto,
			},
		},
	}

	h.resourcesChan <- &mcp.Resources{
		Collection: test.FakeType0Collection,
		Nonce:      "n0",
		Resources: []mcp.Resource{
			*test.Type0A[0].Resource,
			*test.Type0B[0].Resource,
		},
	}

	<-h.changeUpdatedChans
	h.mu.Lock()
	got := h.changes[test.FakeType0Collection]
	h.mu.Unlock()
	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("wrong change on first update: \n got %v \nwant %v \ndiff %v", got, want, diff)
	}

	h.recvErrorChan <- io.EOF
	err := <-errc
	if err != nil {
		t.Fatalf("Stream exited with error: got %v", err)
	}

	h.sinkTestHarness = newSinkTestHarness()

	// multiple connections
	go func() {
		errc <- s.EstablishResourceStream(h)
	}()

	// wait for first connection to succeed and change to be applied
	h.resourcesChan <- &mcp.Resources{
		Collection: test.FakeType0Collection,
		Nonce:      "n0",
		Resources: []mcp.Resource{
			*test.Type0A[0].Resource,
		},
	}

	<-h.changeUpdatedChans

	h.setContext(peer.NewContext(context.Background(), &peer.Peer{
		Addr:     &net.IPAddr{IP: net.IPv4(192, 168, 1, 1)},
		AuthInfo: authChecker,
	}))

	// error disconnect
	h.recvErrorChan <- errors.New("unknown error")
	err = <-errc
	if err == nil {
		t.Fatal("Stream exited without error")
	}

	authChecker.AllowError = errors.New("not allowed")

	err = s.EstablishResourceStream(h)
	if s, ok := status.FromError(err); !ok || s.Code() != codes.Unauthenticated {
		t.Fatalf("Connection should have failed: got %v want %v", err, nil)
	}
}
