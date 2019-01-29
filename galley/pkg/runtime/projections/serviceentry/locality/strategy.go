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

package locality

import (
	"istio.io/istio/galley/pkg/runtime/log"
	"istio.io/istio/galley/pkg/runtime/projections/serviceentry/node"
	"istio.io/istio/galley/pkg/runtime/projections/serviceentry/pod"
)

// Strategy for determining locality of an IP.
type Strategy interface {
	GetLocalityForIP(ip string) string
}

var _ Strategy = &strategyImpl{}

type strategyImpl struct {
	pods  pod.Cache
	nodes node.Cache
}

// NewStrategy for locality backed by caches for nodes and pods.
func NewStrategy(pods pod.Cache, nodes node.Cache) Strategy {
	s := &strategyImpl{
		pods:  pods,
		nodes: nodes,
	}
	return s
}

func (s *strategyImpl) GetLocalityForIP(ip string) string {
	p := s.pods.GetPodByIP(ip)
	if p == nil {
		return ""
	}

	n := s.nodes.GetNodeByName(p.NodeName())
	if n == nil {
		log.Scope.Warnf("unable to get node %q for pod %q", p.NodeName(), p.FullName())
		return ""
	}

	return n.GetLocality()
}
