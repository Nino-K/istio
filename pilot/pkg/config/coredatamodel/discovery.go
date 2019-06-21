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

package coredatamodel

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"

	networking "istio.io/api/networking/v1alpha3"

	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pkg/config/host"
	"istio.io/istio/pkg/config/labels"
	"istio.io/istio/pkg/config/protocol"
	"istio.io/pkg/log"
)

var (
	notReadyEndpointkey                        = "networking.alpha.istio.io/notReadyEndpoints"
	_                   model.Controller       = &MCPDiscovery{}
	_                   model.ServiceDiscovery = &MCPDiscovery{}
)

// MCPDiscovery provides storage for NotReadyEndpoints
type MCPDiscovery struct {
	// [ip:port]config
	notReadyEndpoints   map[string]*model.Config
	notReadyEndpointsMu sync.RWMutex
}

// NewMCPDiscovery provides a new instance of Discovery
func NewMCPDiscovery() *MCPDiscovery {
	return &MCPDiscovery{
		notReadyEndpoints: make(map[string]*model.Config),
	}
}

// RegisterNotReadyEndpoints registers newly received NotReadyEndpoints
// via MCP annotations
func (r *MCPDiscovery) RegisterNotReadyEndpoints(conf *model.Config) {
	r.notReadyEndpointsMu.Lock()
	defer r.notReadyEndpointsMu.Unlock()

	if nrEps, ok := conf.Annotations[notReadyEndpointkey]; ok {
		addrs := strings.Split(nrEps, ",")
		for _, addr := range addrs {
			r.notReadyEndpoints[addr] = conf
		}
	}
}

//TODO: (Nino-k) We have to include ServiceDiscovery interface and model controller interface
// for now to be able to handle NotReadyEndpoints however this should go away
// once we have either HDS ( health check by envoy), or by computing listeners on labels

// GetProxyServiceInstances returns service instances co-located with a given proxy
func (r *MCPDiscovery) GetProxyServiceInstances(proxy *model.Proxy) ([]*model.ServiceInstance, error) {
	out := make([]*model.ServiceInstance, 0)

	// There is only one IP for kube registry
	proxyIP := proxy.IPAddresses[0]

	r.notReadyEndpointsMu.Lock()
	defer r.notReadyEndpointsMu.Unlock()
	for addr, conf := range r.notReadyEndpoints {
		se, ok := conf.Spec.(*networking.ServiceEntry)
		if !ok {
			return nil, errors.New("getProxyServiceInstances: wrong type")
		}
		ip, port, err := net.SplitHostPort(addr)
		if err != nil {
			continue
		}
		if proxyIP == ip {
			portNum, err := strconv.Atoi(port)
			if err != nil {
				continue
			}

			for _, h := range se.Hosts {
				for _, port := range se.Ports {
					out = append(out, &model.ServiceInstance{
						Endpoint: model.NetworkEndpoint{
							Address: ip,
							Port:    portNum,
							ServicePort: &model.Port{
								Name:     port.Name,
								Port:     int(port.Number),
								Protocol: protocol.Instance(port.Protocol),
							},
						},
						// ServiceAccount is retrieved in Kube/Controller
						// using IdentityPodAnnotation
						//ServiceAccount: "",
						Service: &model.Service{
							Hostname:   host.Name(h),
							Ports:      convertServicePorts(se.Ports),
							Resolution: model.Resolution(int(se.Resolution)),
						},
					})
				}
			}
		}
	}
	return out, nil
}

func convertServicePorts(ports []*networking.Port) model.PortList {
	out := make(model.PortList, 0)
	for _, port := range ports {
		out = append(out, &model.Port{
			Name:     port.Name,
			Port:     int(port.Number),
			Protocol: protocol.Instance(port.Protocol),
		})
	}

	return out
}

// Services Not Supported
func (r *MCPDiscovery) Services() ([]*model.Service, error) {
	log.Warnf("Services %s", errUnsupported)
	return nil, nil
}

// GetService Not Supported
func (r *MCPDiscovery) GetService(hostname host.Name) (*model.Service, error) {
	log.Warnf("GetService %s", errUnsupported)
	return nil, nil
}

// InstancesByPort Not Supported
func (r *MCPDiscovery) InstancesByPort(svc *model.Service, servicePort int, labels labels.Collection) ([]*model.ServiceInstance, error) {
	log.Warnf("InstancesByPort %s", errUnsupported)
	return nil, nil
}

// ManagementPorts Not Supported
func (r *MCPDiscovery) ManagementPorts(addr string) model.PortList {
	log.Warnf("ManagementPorts %s", errUnsupported)
	return nil
}

// WorkloadHealthCheckInfo Not Supported
func (r *MCPDiscovery) WorkloadHealthCheckInfo(addr string) model.ProbeList {
	log.Warnf("WorkloadHealthCheckInfo %s", errUnsupported)
	return nil
}

// GetIstioServiceAccounts Not Supported
func (r *MCPDiscovery) GetIstioServiceAccounts(svc *model.Service, ports []int) []string {
	log.Warnf("GetIstioServiceAccounts %s", errUnsupported)
	return nil
}

// GetProxyWorkloadLabels Not Supported
func (r *MCPDiscovery) GetProxyWorkloadLabels(*model.Proxy) (labels.Collection, error) {
	log.Warnf("GetProxyWorkloadLabels %s", errUnsupported)
	return nil, nil
}

// model Controller

// AppendServiceHandler Not Supported
func (r *MCPDiscovery) AppendServiceHandler(f func(*model.Service, model.Event)) error {
	log.Warnf("AppendServiceHandler %s", errUnsupported)
	return nil
}

// AppendInstanceHandler Not Supported
func (r *MCPDiscovery) AppendInstanceHandler(f func(*model.ServiceInstance, model.Event)) error {
	log.Warnf("AppendInstanceHandler %s", errUnsupported)
	return nil
}

// Run until a signal is received
func (r *MCPDiscovery) Run(stop <-chan struct{}) {
	log.Warnf("Run %s", errUnsupported)
}
