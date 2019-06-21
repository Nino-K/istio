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

package coredatamodel_test

import (
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/onsi/gomega"

	networking "istio.io/api/networking/v1alpha3"
	"istio.io/istio/pilot/pkg/config/coredatamodel"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pkg/config/host"
	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/config/schemas"
)

//TODO: remove this once we have HDS or better solution
func TestNotReadyEndpoints(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	fx := NewFakeXDS()
	fx.EDSErr <- nil
	testControllerOptions.XDSUpdater = fx
	d := coredatamodel.NewMCPDiscovery()
	controller := coredatamodel.NewSyntheticServiceEntryController(testControllerOptions, d)

	message := convertToResource(g, schemas.SyntheticServiceEntry.MessageName, syntheticServiceEntry0)

	change := convertToChange([]proto.Message{message},
		[]string{"random-namespace/test-synthetic-se"},
		setVersion("1"),
		setAnnotations(map[string]string{
			"networking.alpha.istio.io/notReadyEndpoints": "1.1.1.1:2222,4.4.4.4:5555,6.6.6.6:7777",
		}),
		setCollection(schemas.SyntheticServiceEntry.Collection),
		setTypeURL(schemas.SyntheticServiceEntry.MessageName))

	err := controller.Apply(change)
	g.Expect(err).ToNot(gomega.HaveOccurred())

	entries, err := controller.List(schemas.SyntheticServiceEntry.Type, "")
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(entries).To(gomega.HaveLen(1))
	g.Expect(entries[0].Name).To(gomega.Equal("test-synthetic-se"))

	update := <-fx.Events
	g.Expect(update).To(gomega.Equal("ConfigUpdate"))

	proxyIP := "4.4.4.4"
	proxy := &model.Proxy{
		IPAddresses: []string{proxyIP},
	}

	svcInstances, err := d.GetProxyServiceInstances(proxy)
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(len(svcInstances)).To(gomega.Equal(2))

	for _, s := range svcInstances {
		g.Expect(s.Endpoint.Address).To(gomega.Equal(proxyIP))
		g.Expect(s.Endpoint.Port).To(gomega.Equal(5555))
		g.Expect(s.Endpoint.ServicePort.Protocol).To(gomega.Equal(protocol.Instance("http")))
		if s.Endpoint.ServicePort.Port == 80 {
			g.Expect(s.Endpoint.ServicePort.Name).To(gomega.Equal("http-port"))
		} else {
			g.Expect(s.Endpoint.ServicePort.Name).To(gomega.Equal("http-alt-port"))
		}
		g.Expect(s.Service.Ports).To(gomega.ConsistOf(convertToSePorts(syntheticServiceEntry0.Ports)))
		g.Expect(s.Service.Hostname).To(gomega.Equal(host.Name(syntheticServiceEntry0.Hosts[0])))
	}
}

func convertToSePorts(ports []*networking.Port) model.PortList {
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
