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

package internal

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimit is partially representing standard lib's rate limiter
type RateLimit interface {
	Wait(ctx context.Context) (err error)
}

// ConnectionRateLimit is an interface for per connection rate limiter
type ConnectionRateLimit interface {
	Wait(ctx context.Context, id string) (err error)
	Remove(id string)
}

// RateLimiter is wrapper around golang's rate.Limit
// it associates each limiter to a unique connection ID
// for more balanced rate limiting among multiple connections
type RateLimiter struct {
	connectionFreq       time.Duration
	connectionBurstSize  int
	perConnRateLimiter   map[string]RateLimit
	perConnRateLimiterMu sync.Mutex
}

// NewRateLimiter returns a new RateLimiter
func NewRateLimiter(freq time.Duration, burstSize int) *RateLimiter {
	return &RateLimiter{
		connectionFreq:      freq,
		connectionBurstSize: burstSize,
		perConnRateLimiter:  make(map[string]RateLimit),
	}
}

// Wait associtates a limiter to a Connection ID
// and calls wait with a given context
func (c *RateLimiter) Wait(ctx context.Context, id string) error {
	c.perConnRateLimiterMu.Lock()
	defer c.perConnRateLimiterMu.Unlock()
	if limiter, ok := c.perConnRateLimiter[id]; ok {
		return limiter.Wait(ctx)
	}
	c.perConnRateLimiter[id] = rate.NewLimiter(
		rate.Every(c.connectionFreq),
		c.connectionBurstSize)
	return c.perConnRateLimiter[id].Wait(ctx)
}

// Remove disassociate a rate limiter for a given ID
func (c *RateLimiter) Remove(id string) {
	c.perConnRateLimiterMu.Lock()
	defer c.perConnRateLimiterMu.Unlock()
	delete(c.perConnRateLimiter, id)
}
