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

package locality_test

import (
	"testing"

	"github.com/onsi/gomega"

	"istio.io/istio/galley/pkg/runtime/projections/serviceentry/locality"
	"istio.io/istio/galley/pkg/runtime/projections/serviceentry/node"
	"istio.io/istio/galley/pkg/runtime/projections/serviceentry/pod"
	"istio.io/istio/galley/pkg/runtime/resource"
)

const (
	nodeName = "node1"
	zone     = "fakezone"
)

func TestIPFound(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	pods := newPodCache(true)
	nodes := newNodeCache(true)

	s := locality.NewStrategy(pods, nodes)
	actual := s.GetLocalityForIP("unused")
	g.Expect(actual).To(gomega.Equal("fakezone"))
}

func TestPodNotFound(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	pods := newPodCache(false)
	nodes := newNodeCache(true)

	s := locality.NewStrategy(pods, nodes)
	actual := s.GetLocalityForIP("unused")
	g.Expect(actual).To(gomega.Equal(""))
}

func TestNodeNotFound(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	pods := newPodCache(true)
	nodes := newNodeCache(false)

	s := locality.NewStrategy(pods, nodes)
	actual := s.GetLocalityForIP("unused")
	g.Expect(actual).To(gomega.Equal(""))
}

func newPodCache(found bool) *fakePodCache {
	return &fakePodCache{
		found:    found,
		nodeName: nodeName,
	}
}

func newNodeCache(found bool) *fakeNodeCache {
	return &fakeNodeCache{
		found:    found,
		locality: zone,
	}
}

type fakeNodeCache struct {
	locality string
	found    bool
}

func (c *fakeNodeCache) GetNodeByName(name string) node.Info {
	if c.found {
		return c
	}
	return nil
}

func (c *fakeNodeCache) GetLocality() string {
	return c.locality
}

type fakePodCache struct {
	nodeName string
	found    bool
}

func (c *fakePodCache) GetPodByIP(ip string) pod.Info {
	if c.found {
		return c
	}
	return nil
}

func (c *fakePodCache) FullName() resource.FullName {
	return resource.FullName{}
}

func (c *fakePodCache) NodeName() string {
	return c.nodeName
}
