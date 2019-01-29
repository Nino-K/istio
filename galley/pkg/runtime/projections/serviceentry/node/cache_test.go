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

package node_test

import (
	"testing"

	. "github.com/onsi/gomega"

	"istio.io/istio/galley/pkg/metadata"
	"istio.io/istio/galley/pkg/runtime/projections/serviceentry/node"
	"istio.io/istio/galley/pkg/runtime/resource"

	"k8s.io/kubernetes/pkg/kubelet/apis"
)

const (
	name = "node1"
)

var (
	id = resource.VersionedKey{
		Key: resource.Key{
			Collection: metadata.Node.Collection,
			FullName:   resource.FullNameFromNamespaceAndName("", name),
		},
	}
)

func TestBasicEvents(t *testing.T) {
	g := NewGomegaWithT(t)

	c, h := node.NewCache()

	t.Run("Add", func(t *testing.T) {
		h.Handle(resource.Event{
			Kind:  resource.Added,
			Entry: entry("region1", "zone1"),
		})
		n := c.GetNodeByName(name)
		g.Expect(n).ToNot(BeNil())
		g.Expect(n.GetLocality()).To(Equal("region1/zone1"))
	})

	t.Run("Update", func(t *testing.T) {
		h.Handle(resource.Event{
			Kind:  resource.Updated,
			Entry: entry("region1", "zone2"),
		})
		n := c.GetNodeByName(name)
		g.Expect(n).ToNot(BeNil())
		g.Expect(n.GetLocality()).To(Equal("region1/zone2"))
	})

	t.Run("Delete", func(t *testing.T) {
		h.Handle(resource.Event{
			Kind:  resource.Deleted,
			Entry: entry("region1", "zone2"),
		})
		n := c.GetNodeByName(name)
		g.Expect(n).To(BeNil())
	})
}

func TestNodeWithOnlyRegion(t *testing.T) {
	g := NewGomegaWithT(t)

	c, h := node.NewCache()

	h.Handle(resource.Event{
		Kind:  resource.Added,
		Entry: entry("region1", ""),
	})

	n := c.GetNodeByName(name)
	g.Expect(n).ToNot(BeNil())
	g.Expect(n.GetLocality()).To(Equal("region1/"))
}

func TestNodeWithNoLocality(t *testing.T) {
	g := NewGomegaWithT(t)

	c, h := node.NewCache()

	h.Handle(resource.Event{
		Kind:  resource.Added,
		Entry: entry("", ""),
	})

	n := c.GetNodeByName(name)
	g.Expect(n).ToNot(BeNil())
	g.Expect(n.GetLocality()).To(Equal(""))
}

func labels(region, zone string) resource.Labels {
	labels := make(resource.Labels)
	if region != "" {
		labels[apis.LabelZoneRegion] = region
	}
	if zone != "" {
		labels[apis.LabelZoneFailureDomain] = zone
	}
	return labels
}

func entry(region, zone string) resource.Entry {
	return resource.Entry{
		ID: id,
		Metadata: resource.Metadata{
			Labels: labels(region, zone),
		},
	}
}
