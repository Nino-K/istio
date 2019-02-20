// Copyright 2018 Istio Authors
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

package test

import (
	"context"
	"fmt"

	"istio.io/istio/pkg/mcp/internal"
)

type FakeRateLimiter struct {
	WaitErr chan error
	WaitCh  chan struct{}
}

func NewFakeRateLimiter() *FakeRateLimiter {
	return &FakeRateLimiter{
		WaitErr: make(chan error),
		WaitCh:  make(chan struct{}, 100),
	}
}

func (f *FakeRateLimiter) Wait(ctx context.Context) error {
	fmt.Println("WAIT called")
	f.WaitCh <- struct{}{}
	select {
	case err := <-f.WaitErr:
		return err
	default:
		return nil
	}
}

type FakePerConnLimiter struct {
	fakeLimiter *FakeRateLimiter
	CreateCh    chan struct{}
	WaitCh      chan struct{}
}

func NewFakePerConnLimiter() *FakePerConnLimiter {
	fakeLimiter := NewFakeRateLimiter()
	f := &FakePerConnLimiter{
		fakeLimiter: fakeLimiter,
		CreateCh:    make(chan struct{}, 100),
		WaitCh:      fakeLimiter.WaitCh,
	}
	return f
}

func (f *FakePerConnLimiter) Create() internal.RateLimit {
	fmt.Println("CREATE called")
	f.CreateCh <- struct{}{}
	return f.fakeLimiter
}
