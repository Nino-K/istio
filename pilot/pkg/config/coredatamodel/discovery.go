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
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	meshconfig "istio.io/api/mesh/v1alpha1"
	networking "istio.io/api/networking/v1alpha3"

	"github.com/yl2chen/cidranger"

	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/serviceregistry/kube"
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

// namedRangerEntry for holding network's CIDR and name
type namedRangerEntry struct {
	name    string
	network net.IPNet
}

// returns the IPNet for the network
func (n namedRangerEntry) Network() net.IPNet {
	return n.network
}

// DiscoveryOptions stores the configurable attributes of a Control
type DiscoveryOptions struct {
	XDSUpdater   model.XDSUpdater
	Env          *model.Environment
	ClusterID    string
	DomainSuffix string
}

// mixerEnabled checks to see if mixer is enabled in the environment
// so we can set the UID on eds endpoints
func (o *DiscoveryOptions) mixerEnabled() bool {
	return o.Env != nil && o.Env.Mesh != nil && (o.Env.Mesh.MixerCheckServer != "" || o.Env.Mesh.MixerReportServer != "")
}

// MCPDiscovery provides storage for NotReadyEndpoints
type MCPDiscovery struct {
	// [ip:port]config
	notReadyEndpoints   map[string]*model.Config
	notReadyEndpointsMu sync.RWMutex
	options             *DiscoveryOptions
	networkForRegistry  string
	ranger              cidranger.Ranger
}

// NewMCPDiscovery provides a new instance of Discovery
func NewMCPDiscovery(options *DiscoveryOptions) *MCPDiscovery {
	return &MCPDiscovery{
		notReadyEndpoints: make(map[string]*model.Config),
		options:           options,
	}
}

// InitNetworkLookup will read the mesh networks configuration from the environment
// and initialize CIDR rangers for an efficient network lookup when needed
func (d *MCPDiscovery) InitNetworkLookup(meshNetworks *meshconfig.MeshNetworks) {
	if meshNetworks == nil || len(meshNetworks.Networks) == 0 {
		return
	}

	d.ranger = cidranger.NewPCTrieRanger()

	for n, v := range meshNetworks.Networks {
		for _, ep := range v.Endpoints {
			if ep.GetFromCidr() != "" {
				_, network, err := net.ParseCIDR(ep.GetFromCidr())
				if err != nil {
					log.Warnf("unable to parse CIDR %q for network %s", ep.GetFromCidr(), n)
					continue
				}
				rangerEntry := namedRangerEntry{
					name:    n,
					network: *network,
				}
				_ = d.ranger.Insert(rangerEntry)
			}
			if ep.GetFromRegistry() != "" && ep.GetFromRegistry() == d.options.ClusterID {
				d.networkForRegistry = n
			}
		}
	}
}

func (d *MCPDiscovery) endpointNetwork(endpointIP string) string {
	// If networkForRegistry is set then all endpoints discovered by this registry
	// belong to the configured network so simply return it
	if len(d.networkForRegistry) != 0 {
		return d.networkForRegistry
	}

	// Try to determine the network by checking whether the endpoint IP belongs
	// to any of the configure networks' CIDR ranges
	if d.ranger == nil {
		return ""
	}
	entries, err := d.ranger.ContainingNetworks(net.ParseIP(endpointIP))
	if err != nil {
		log.Errora(err)
		return ""
	}
	if len(entries) == 0 {
		return ""
	}
	if len(entries) > 1 {
		log.Warnf("Found multiple networks CIDRs matching the endpoint IP: %s. Using the first match.", endpointIP)
	}

	return (entries[0].(namedRangerEntry)).name
}

// RegisterNotReadyEndpoints registers newly received NotReadyEndpoints
// via MCP annotations
func (d *MCPDiscovery) RegisterNotReadyEndpoints(conf *model.Config) {
	d.notReadyEndpointsMu.Lock()
	defer d.notReadyEndpointsMu.Unlock()

	if nrEps, ok := conf.Annotations[notReadyEndpointkey]; ok {
		addrs := strings.Split(nrEps, ",")
		for _, addr := range addrs {
			d.notReadyEndpoints[addr] = conf
		}
	}
}

func (d *MCPDiscovery) GetProxyServiceInstances(proxy *model.Proxy) ([]*model.ServiceInstance, error) {
	out := make([]*model.ServiceInstance, 0)

	// There is only one IP for kube registry
	proxyIP := proxy.IPAddresses[0]

	d.notReadyEndpointsMu.Lock()
	defer d.notReadyEndpointsMu.Unlock()
	for addr, conf := range d.notReadyEndpoints {
		notReadyIP, port, err := net.SplitHostPort(addr)
		if err != nil {
			continue
		}
		notReadyPort, err := strconv.Atoi(port)
		if proxyIP != notReadyIP || err != nil {
			continue
		}
		endpoints := make([]*model.IstioEndpoint, 0)
		hostname := kube.ServiceHostname(conf.Name, conf.Namespace, d.options.DomainSuffix)
		se, ok := conf.Spec.(*networking.ServiceEntry)
		if !ok {
			return nil, errors.New("getProxyServiceInstances: wrong type")
		}

		for _, h := range se.Hosts {
			for _, svcPort := range se.Ports {
				// add not ready endpoint
				out = append(out, notReadyServiceInstance(se, svcPort, conf, h, notReadyIP, notReadyPort))
				// add other endpoints
				out = append(out, serviceInstances(se, svcPort, conf, h)...)
				var uid string
				if d.options.mixerEnabled() {
					uid = fmt.Sprintf("kubernetes://%s.%s", conf.Name, conf.Namespace)
				}
				// create an EDSEndpoint for notReady IP:Port
				edsEndpoint := &model.IstioEndpoint{
					Address:         notReadyIP,
					EndpointPort:    uint32(notReadyPort),
					ServicePortName: svcPort.Name,
					Labels:          conf.Labels,
					UID:             uid,
					//	ServiceAccount:  ???, //TODO: nino-k
					Network: d.endpointNetwork(notReadyIP),
					//Locality:   ???, //TODO: nino-k
					Attributes: model.ServiceAttributes{Name: conf.Name, Namespace: conf.Namespace},
				}
				endpoints = append(endpoints, edsEndpoint)
			}
			_ = d.options.XDSUpdater.EDSUpdate(d.options.ClusterID, string(hostname), conf.Namespace, endpoints)
		}
	}
	return out, nil
}

func notReadyServiceInstance(se *networking.ServiceEntry, svcPort *networking.Port, conf *model.Config, hostname, ip string, port int) *model.ServiceInstance {
	return &model.ServiceInstance{
		Endpoint: model.NetworkEndpoint{
			UID:     fmt.Sprintf("kubernetes://%s.%s", conf.Name, conf.Namespace),
			Address: ip,
			Port:    port,
			ServicePort: &model.Port{
				Name:     svcPort.Name,
				Port:     int(svcPort.Number),
				Protocol: protocol.Instance(svcPort.Protocol),
			},
		},
		// ServiceAccount is retrieved in Kube/Controller
		// using IdentityPodAnnotation
		//ServiceAccount: ???, //TODO: nino-k
		Service: &model.Service{
			Hostname:   host.Name(hostname),
			Ports:      convertServicePorts(se.Ports),
			Resolution: model.Resolution(int(se.Resolution)),
		},
		Labels: labels.Instance(conf.Labels),
	}
}

func serviceInstances(se *networking.ServiceEntry, svcPort *networking.Port, conf *model.Config, hostname string) (out []*model.ServiceInstance) {
	for _, ep := range se.Endpoints {
		for _, epPort := range ep.Ports {
			si := &model.ServiceInstance{
				Endpoint: model.NetworkEndpoint{
					UID:      fmt.Sprintf("kubernetes://%s.%s", conf.Name, conf.Namespace),
					Address:  ep.Address,
					Port:     int(epPort),
					Locality: ep.Locality,
					LbWeight: ep.Weight,
					Network:  ep.Network,
					ServicePort: &model.Port{
						Name:     svcPort.Name,
						Port:     int(svcPort.Number),
						Protocol: protocol.Instance(svcPort.Protocol),
					},
				},
				// ServiceAccount is retrieved in Kube/Controller
				// using IdentityPodAnnotation
				//ServiceAccount: ???, //TODO: nino-k
				Service: &model.Service{
					Hostname:   host.Name(hostname),
					Ports:      convertServicePorts(se.Ports),
					Resolution: model.Resolution(int(se.Resolution)),
				},
				Labels: labels.Instance(conf.Labels),
			}
			out = append(out, si)
		}
	}
	return out
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
func (d *MCPDiscovery) Services() ([]*model.Service, error) {
	log.Warnf("Services %s", errUnsupported)
	return nil, nil
}

// GetService Not Supported
func (d *MCPDiscovery) GetService(hostname host.Name) (*model.Service, error) {
	log.Warnf("GetService %s", errUnsupported)
	return nil, nil
}

// InstancesByPort Not Supported
func (d *MCPDiscovery) InstancesByPort(svc *model.Service, servicePort int, labels labels.Collection) ([]*model.ServiceInstance, error) {
	log.Warnf("InstancesByPort %s", errUnsupported)
	return nil, nil
}

// ManagementPorts Not Supported
func (d *MCPDiscovery) ManagementPorts(addr string) model.PortList {
	log.Warnf("ManagementPorts %s", errUnsupported)
	return nil
}

// WorkloadHealthCheckInfo Not Supported
func (d *MCPDiscovery) WorkloadHealthCheckInfo(addr string) model.ProbeList {
	log.Warnf("WorkloadHealthCheckInfo %s", errUnsupported)
	return nil
}

// GetIstioServiceAccounts Not Supported
func (d *MCPDiscovery) GetIstioServiceAccounts(svc *model.Service, ports []int) []string {
	log.Warnf("GetIstioServiceAccounts %s", errUnsupported)
	return nil
}

// GetProxyWorkloadLabels Not Supported
func (d *MCPDiscovery) GetProxyWorkloadLabels(*model.Proxy) (labels.Collection, error) {
	log.Warnf("GetProxyWorkloadLabels %s", errUnsupported)
	return nil, nil
}

// model Controller

// AppendServiceHandler Not Supported
func (d *MCPDiscovery) AppendServiceHandler(f func(*model.Service, model.Event)) error {
	log.Warnf("AppendServiceHandler %s", errUnsupported)
	return nil
}

// AppendInstanceHandler Not Supported
func (d *MCPDiscovery) AppendInstanceHandler(f func(*model.ServiceInstance, model.Event)) error {
	log.Warnf("AppendInstanceHandler %s", errUnsupported)
	return nil
}

// Run until a signal is received
func (d *MCPDiscovery) Run(stop <-chan struct{}) {
	log.Warnf("Run %s", errUnsupported)
}
