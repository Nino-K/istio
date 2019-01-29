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

package pod_test

import (
	"testing"

	. "github.com/onsi/gomega"

	"istio.io/istio/galley/pkg/metadata"
	"istio.io/istio/galley/pkg/runtime/projections/serviceentry/pod"
	"istio.io/istio/galley/pkg/runtime/resource"

	coreV1 "k8s.io/api/core/v1"
)

const (
	podName   = "pod1"
	nodeName  = "node1"
	namespace = "ns"
)

var (
	fullName = resource.FullNameFromNamespaceAndName(namespace, podName)

	id = resource.VersionedKey{
		Key: resource.Key{
			Collection: metadata.Pod.Collection,
			FullName:   fullName,
		},
	}
)

func TestBasicEvents(t *testing.T) {
	g := NewGomegaWithT(t)

	c, h := pod.NewCache()

	t.Run("Add", func(t *testing.T) {
		h.Handle(resource.Event{
			Kind:  resource.Added,
			Entry: entry("1.2.3.4", coreV1.PodPending),
		})
		p := c.GetPodByIP("1.2.3.4")
		g.Expect(p).ToNot(BeNil())
		g.Expect(p.NodeName()).To(Equal("node1"))
		g.Expect(p.FullName()).To(Equal(fullName))
	})

	t.Run("Update", func(t *testing.T) {
		h.Handle(resource.Event{
			Kind:  resource.Updated,
			Entry: entry("1.2.3.4", coreV1.PodRunning),
		})
		p := c.GetPodByIP("1.2.3.4")
		g.Expect(p).ToNot(BeNil())
		g.Expect(p.NodeName()).To(Equal("node1"))
		g.Expect(p.FullName()).To(Equal(fullName))
	})

	t.Run("Delete", func(t *testing.T) {
		h.Handle(resource.Event{
			Kind:  resource.Deleted,
			Entry: entry("1.2.3.4", coreV1.PodRunning),
		})
		n := c.GetPodByIP("1.2.3.4")
		g.Expect(n).To(BeNil())
	})
}

func TestInvalidPodPhase(t *testing.T) {
	g := NewGomegaWithT(t)

	c, h := pod.NewCache()

	for _, phase := range []coreV1.PodPhase{coreV1.PodSucceeded, coreV1.PodFailed, coreV1.PodUnknown} {
		t.Run(string(phase), func(t *testing.T) {
			h.Handle(resource.Event{
				Kind:  resource.Added,
				Entry: entry("1.2.3.4", phase),
			})
			p := c.GetPodByIP("1.2.3.4")
			g.Expect(p).To(BeNil())
		})
	}
}

func TestUpdateWithInvalidPhaseShouldDelete(t *testing.T) {
	g := NewGomegaWithT(t)

	c, h := pod.NewCache()

	// Add it.
	h.Handle(resource.Event{
		Kind:  resource.Added,
		Entry: entry("1.2.3.4", coreV1.PodPending),
	})
	p := c.GetPodByIP("1.2.3.4")
	g.Expect(p).ToNot(BeNil())

	h.Handle(resource.Event{
		Kind:  resource.Updated,
		Entry: entry("1.2.3.4", coreV1.PodUnknown),
	})
	p = c.GetPodByIP("1.2.3.4")
	g.Expect(p).To(BeNil())
}

func TestDeleteWithNoItemShouldUseFullName(t *testing.T) {
	g := NewGomegaWithT(t)

	c, h := pod.NewCache()

	// Add it.
	h.Handle(resource.Event{
		Kind:  resource.Added,
		Entry: entry("1.2.3.4", coreV1.PodPending),
	})
	p := c.GetPodByIP("1.2.3.4")
	g.Expect(p).ToNot(BeNil())

	// Delete it, but with a nil Item to force a lookup by fullName.
	h.Handle(resource.Event{
		Kind: resource.Deleted,
		Entry: resource.Entry{
			ID: id,
		},
	})
	p = c.GetPodByIP("1.2.3.4")
	g.Expect(p).To(BeNil())
}

func TestDeleteNotFoundShouldNotPanic(t *testing.T) {
	_, h := pod.NewCache()

	// Delete it, but with a nil Item to force a lookup by fullName.
	h.Handle(resource.Event{
		Kind:  resource.Deleted,
		Entry: entry("1.2.3.4", coreV1.PodRunning),
	})
}

func TestDeleteNotFoundWithMissingItemShouldNotPanic(t *testing.T) {
	_, h := pod.NewCache()

	// Delete it, but with a nil Item to force a lookup by fullName.
	h.Handle(resource.Event{
		Kind: resource.Deleted,
		Entry: resource.Entry{
			ID: id,
		},
	})
}

func TestNoIP(t *testing.T) {
	g := NewGomegaWithT(t)

	c, h := pod.NewCache()

	h.Handle(resource.Event{
		Kind:  resource.Added,
		Entry: entry("", coreV1.PodRunning),
	})
	p := c.GetPodByIP("")
	g.Expect(p).To(BeNil())
}

func entry(ip string, phase coreV1.PodPhase) resource.Entry {
	return resource.Entry{
		ID: id,
		Item: &coreV1.Pod{
			Spec: coreV1.PodSpec{
				NodeName: nodeName,
			},
			Status: coreV1.PodStatus{
				PodIP: ip,
				Phase: phase,
			},
		},
	}
}
