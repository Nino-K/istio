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

import "context"

type FakeRateLimiter struct {
	WaitErr chan error
}

func NewFakeRateLimiter() *FakeRateLimiter {
	return &FakeRateLimiter{
		WaitErr: make(chan error),
	}
}

func (f *FakeRateLimiter) Wait(ctx context.Context) error {
	select {
	case err := <-f.WaitErr:
		return err
	default:
		return nil
	}
}

type FakePerConnLimiter struct {
	WaitErr chan error
}

func NewFakePerConnLimiter() *FakePerConnLimiter {
	return &FakePerConnLimiter{
		WaitErr: make(chan error),
	}
}

func (f *FakePerConnLimiter) Wait(ctx context.Context, id string) error {
	select {
	case err := <-f.WaitErr:
		return err
	default:
		return nil
	}
}

func (f *FakePerConnLimiter) Remove(id string) {}
