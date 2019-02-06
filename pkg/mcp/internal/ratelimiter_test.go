// Copyright 2019  Istio Authors
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
	"testing"
)

var conIDs = []string{
	"conID1",
	"conID2",
	"conID3",
	"conID4",
	"conID1",
	"conID3",
}

func TestConnRateLimit_NewConnection(t *testing.T) {
	limiter := NewRateLimiter(0, 0)

	for _, id := range conIDs {
		err := limiter.Wait(context.Background(), id)
		if err != nil {
			t.Fatalf("Wait() should not return an error: got %v", err)
		}
	}

	totalUniqueIds := 4
	if len(limiter.perConnRateLimiter) != totalUniqueIds {
		t.Fatalf("Wait(): expected:%d rate limiters got:%d", totalUniqueIds, len(limiter.perConnRateLimiter))
	}
}

func TestConnRateLimit_Remove(t *testing.T) {
	limiter := NewRateLimiter(0, 0)

	for _, id := range conIDs {
		err := limiter.Wait(context.Background(), id)
		if err != nil {
			t.Fatalf("Wait() should not return an error: got %v", err)
		}
	}

	limiter.Remove(conIDs[0])
	limiter.Remove(conIDs[3])

	totalUniqueIds := 2
	if len(limiter.perConnRateLimiter) != totalUniqueIds {
		t.Fatalf("Wait(): expected:%d rate limiters got:%d", totalUniqueIds, len(limiter.perConnRateLimiter))
	}

}
