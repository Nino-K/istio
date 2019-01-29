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

package convert

import (
	"fmt"

	networking "istio.io/api/networking/v1alpha3"
	"istio.io/istio/galley/pkg/runtime/projections/serviceentry/annotations"
	"istio.io/istio/galley/pkg/runtime/projections/serviceentry/locality"
	"istio.io/istio/galley/pkg/runtime/resource"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/serviceregistry/kube"

	coreV1 "k8s.io/api/core/v1"
)

// Annotations augments the annotations for the k8s Service.
func Annotations(serviceAnnotations resource.Annotations) resource.Annotations {
	out := make(resource.Annotations)
	for k, v := range serviceAnnotations {
		out[k] = v
	}

	out[annotations.SyntheticResource] = "true"
	return out
}

// Service converts a k8s Service to a networking.ServiceEntry
func Service(svc *coreV1.ServiceSpec, metadata resource.Metadata, key resource.VersionedKey, domainSuffix string) *networking.ServiceEntry {
	addr, externalName := model.UnspecifiedIP, ""
	if svc.ClusterIP != "" && svc.ClusterIP != coreV1.ClusterIPNone {
		addr = svc.ClusterIP
	}

	resolution := networking.ServiceEntry_STATIC
	location := networking.ServiceEntry_MESH_INTERNAL
	endpoints := convertExternalServiceEndpoints(svc, metadata)

	if svc.Type == coreV1.ServiceTypeExternalName && svc.ExternalName != "" {
		externalName = svc.ExternalName
		resolution = networking.ServiceEntry_DNS
		location = networking.ServiceEntry_MESH_EXTERNAL
	}

	if addr == model.UnspecifiedIP && externalName == "" {
		// Headless services should not be load balanced
		resolution = networking.ServiceEntry_NONE
	}

	ports := make([]*networking.Port, 0, len(svc.Ports))
	for _, port := range svc.Ports {
		ports = append(ports, convertPort(port))
	}

	configScope := networking.ConfigScope_PUBLIC
	if metadata.Labels[kube.ServiceConfigScopeAnnotation] == networking.ConfigScope_name[int32(networking.ConfigScope_PRIVATE)] {
		configScope = networking.ConfigScope_PRIVATE
	}

	name, namespace := key.FullName.InterpretAsNamespaceAndName()
	se := &networking.ServiceEntry{
		Hosts:       []string{serviceHostname(name, namespace, domainSuffix)},
		Addresses:   []string{addr},
		Resolution:  resolution,
		Location:    location,
		Ports:       ports,
		ConfigScope: configScope,
		Endpoints:   endpoints,
	}
	return se
}

// Endpoints converts k8s Endpoints to networking.ServiceEntry_Endpoint resources.
func Endpoints(serviceMeta resource.Metadata, endpoints *coreV1.Endpoints, localityStrategy locality.Strategy) []*networking.ServiceEntry_Endpoint {
	out := make([]*networking.ServiceEntry_Endpoint, 0)
	for _, subset := range endpoints.Subsets {

		// Convert the ports for this subset.
		ports := make(map[string]uint32)
		for _, port := range subset.Ports {
			ports[port.Name] = uint32(port.Port)
		}

		// Convert
		for _, address := range subset.Addresses {
			ep := &networking.ServiceEntry_Endpoint{
				Labels:   serviceMeta.Labels,
				Address:  address.IP,
				Ports:    ports,
				Locality: localityStrategy.GetLocalityForIP(address.IP),
				// TODO(nmittler): Network: "",
			}
			out = append(out, ep)
		}
	}
	return out
}

func convertExternalServiceEndpoints(svc *coreV1.ServiceSpec, serviceMeta resource.Metadata) []*networking.ServiceEntry_Endpoint {
	endpoints := make([]*networking.ServiceEntry_Endpoint, 0)
	if svc.Type == coreV1.ServiceTypeExternalName && svc.ExternalName != "" {
		// Generate endpoints for the external service.
		ports := make(map[string]uint32)
		for _, port := range svc.Ports {
			ports[port.Name] = uint32(port.Port)
		}
		addr := svc.ExternalName
		endpoints = append(endpoints, &networking.ServiceEntry_Endpoint{
			Address: addr,
			Ports:   ports,
			Labels:  serviceMeta.Labels,
		})
	}
	return endpoints
}

// serviceHostname produces FQDN for a k8s service
func serviceHostname(name, namespace, domainSuffix string) string {
	return fmt.Sprintf("%s.%s.svc.%s", name, namespace, domainSuffix)
}

func convertPort(port coreV1.ServicePort) *networking.Port {
	return &networking.Port{
		Name:     port.Name,
		Number:   uint32(port.Port),
		Protocol: string(kube.ConvertProtocol(port.Name, port.Protocol)),
	}
}
