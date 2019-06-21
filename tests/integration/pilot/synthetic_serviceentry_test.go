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

package pilot

import (
	"fmt"
	"testing"
	"time"

	xdsapi "github.com/envoyproxy/go-control-plane/envoy/api/v2"

	"istio.io/istio/galley/pkg/metadata"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/echoboot"
	"istio.io/istio/pkg/test/framework/components/environment"
	"istio.io/istio/pkg/test/framework/components/galley"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/components/pilot"
	"istio.io/istio/pkg/test/util/structpath"
)

type testcase string

const (
	serviceEntry   testcase = "serviceEntry"
	serviceUpdate  testcase = "serviceEntry_service_update"
	endpointUpdate testcase = "serviceEntry_endpoint_update"
)

func TestSyntheticServiceEntry(t *testing.T) {
	ctx := framework.NewContext(t)
	defer ctx.Done()

	ns := namespace.NewOrFail(t, ctx, namespace.Config{Prefix: "sse", Inject: true})
	// apply a sse
	applyConfig(serviceEntry, ns, ctx, t)

	expectedAnnotations := []string{
		"networking.alpha.istio.io/serviceVersion",
		"networking.alpha.istio.io/endpointsVersion"}

	collection := metadata.IstioNetworkingV1alpha3SyntheticServiceentries.Collection.String()

	if err := g.WaitForSnapshot(collection, syntheticServiceEntryValidator(t, ns.Name(), expectedAnnotations)); err != nil {
		t.Fatalf("failed waiting for %s:\n%v\n", collection, err)
	}

	var client echo.Instance
	echoboot.NewBuilderOrFail(t, ctx).
		With(&client, echo.Config{
			Domain:    "testns.cluster.local",
			Service:   "echo",
			Namespace: ns,
			Pilot:     p,
			Galley:    g,
		}).BuildOrFail(t)

	nodeID := client.WorkloadsOrFail(t)[0].Sidecar().NodeID()
	discoveryReq := pilot.NewDiscoveryRequest(nodeID, pilot.Listener)
	p.StartDiscoveryOrFail(t, discoveryReq)

	p.WatchDiscoveryOrFail(t, time.Second*10,
		func(response *xdsapi.DiscoveryResponse) (b bool, e error) {
			validator := structpath.ForProto(response)
			if validator.Select("{.resources[?(@.address.socketAddress.portValue==%v)]}", 53).Check() != nil {
				return false, nil
			}
			validateSse(t, validator, "10.43.240.10")
			verifyEndpoint(t, client, []string{"10.40.0.5", "10.40.1.4"}, ns.Name())
			return true, nil
		})

	// update the service
	applyConfig(serviceUpdate, ns, ctx, t)

	p.WatchDiscoveryOrFail(t, time.Second*10,
		func(response *xdsapi.DiscoveryResponse) (b bool, e error) {
			validator := structpath.ForProto(response)
			if validator.Select("{.resources[?(@.address.socketAddress.portValue==%v)]}", 53).Check() != nil {
				return false, nil
			}
			validateSse(t, validator, "10.43.240.11")
			verifyEndpoint(t, client, []string{"10.40.0.5", "10.40.1.4"}, ns.Name())
			return true, nil
		})

	// endpoint update
	applyConfig(endpointUpdate, ns, ctx, t)

	p.WatchDiscoveryOrFail(t, time.Second*10,
		func(response *xdsapi.DiscoveryResponse) (b bool, e error) {
			validator := structpath.ForProto(response)
			if validator.Select("{.resources[?(@.address.socketAddress.portValue==%v)]}", 53).Check() != nil {
				return false, nil
			}
			validateSse(t, validator, "10.43.240.11")
			verifyEndpoint(t, client, []string{"10.40.0.6", "10.40.1.4"}, ns.Name())
			return true, nil
		})

	// check versionInfo
	verifyVersionInfo(t, client, "10.43.240.11_53", "/3")
}

func verifyEndpoint(t *testing.T, c echo.Instance, endpoints []string, nsName string) {
	workloads, _ := c.Workloads()
	for _, w := range workloads {
		if w.Sidecar() != nil {
			msg, err := w.Sidecar().Clusters()
			if err != nil {
				t.Fatal(err)
			}
			validator := structpath.ForProto(msg)
			for _, endpoint := range endpoints {
				t.Run(fmt.Sprintf("verify endpoints for outbound|53||kube-dns.%s.svc.cluster.local", nsName), func(t *testing.T) {
					validator.
						Select("{.clusterStatuses[?(@.name=='%v')]}", fmt.Sprintf("outbound|53||kube-dns.%s.svc.cluster.local", nsName)).
						Equals("true", "{.addedViaApi}").
						ContainSubstring(endpoint, "{.hostStatuses}").
						ContainSubstring("53", "{.hostStatuses}").
						ContainSubstring("HEALTHY", "{.hostStatuses}").
						CheckOrFail(t)
				})
			}
		}
	}
}

func verifyVersionInfo(t *testing.T, c echo.Instance, clusterName, svcVersion string) {
	workloads, _ := c.Workloads()
	for _, w := range workloads {
		if w.Sidecar() != nil {
			cfg, err := w.Sidecar().Config()
			if err != nil {
				t.Fatal(err)
			}
			validator := structpath.ForProto(cfg)
			t.Run(fmt.Sprintf("verify versionInfo for cluster %s", clusterName), func(t *testing.T) {
				validator.
					Select("{.configs[*].dynamicActiveListeners[?(@.listener.name == '%s')]}", clusterName).
					ContainSubstring(svcVersion, "{.versionInfo}").
					CheckOrFail(t)
			})
		}
	}
}

func applyConfig(testName testcase, namespace namespace.Instance, ctx framework.TestContext, t *testing.T) {
	// apply service and deployment in k8s
	var resources []string
	ctx.Environment().Case(environment.Kube, func() {
		switch testName {
		case serviceEntry:
			resources = []string{
				"testdata/deployment.yaml",
				"testdata/service.yaml"}
		case serviceUpdate:
			resources = []string{
				"testdata/deployment.yaml",
				"testdata/service_update.yaml"}
		case endpointUpdate:
			// TODO: how to test endpoint changes in k8s since can't apply endpoint
			resources = []string{
				"testdata/deployment.yaml",
				"testdata/service_update.yaml"}
		}
	})
	// apply all resources in native
	ctx.Environment().Case(environment.Native, func() {
		switch testName {
		case serviceEntry:
			resources = []string{
				"testdata/nodes.yaml",
				"testdata/pods.yaml",
				"testdata/service.yaml",
				"testdata/endpoints.yaml"}
		case serviceUpdate:
			resources = []string{
				"testdata/nodes.yaml",
				"testdata/pods.yaml",
				"testdata/service_update.yaml",
				"testdata/endpoints.yaml"}
		case endpointUpdate:
			resources = []string{
				"testdata/nodes.yaml",
				"testdata/pods.yaml",
				"testdata/service_update.yaml",
				"testdata/endpoint_update.yaml"}
		}
	})
	for _, config := range resources {
		if err := g.ApplyConfigDir(namespace, config); err != nil {
			t.Fatal(err)
		}
	}
}

func validateSse(t *testing.T, response *structpath.Instance, clusterIP string) {
	t.Run("SynthetiServiceEntry-listener", func(t *testing.T) {
		response.
			Select("{.resources[?(@.address.socketAddress.portValue==53)]}").
			Equals(fmt.Sprintf("%s_53", clusterIP), "{.name}").
			Equals(clusterIP, "{.address.socketAddress.address}").
			Equals("envoy.tcp_proxy", "{.filterChains[0].filters[*].name}").
			ContainSubstring("outbound|53||kube-dns.sse", "{.filterChains[0].filters[0].config.cluster}").
			ContainSubstring("outbound|53||kube-dns.sse", "{.filterChains[0].filters[0].config.stat_prefix}").
			CheckOrFail(t)
	})
}

func annotationsExist(t *testing.T, annos []string, instance *structpath.Instance) {
	for _, anno := range annos {
		instance.ContainSubstring(anno, "{.Metadata.annotations}").CheckOrFail(t)
	}
}

func syntheticServiceEntryValidator(t *testing.T, ns string, annotations []string) galley.SnapshotValidatorFunc {
	return galley.NewSingleObjectSnapshotValidator(ns, func(ns string, actual *galley.SnapshotObject) error {
		v := structpath.ForProto(actual)
		if err := v.Equals(metadata.IstioNetworkingV1alpha3SyntheticServiceentries.TypeURL.String(), "{.TypeURL}").
			Equals(fmt.Sprintf("%s/kube-dns", ns), "{.Metadata.name}").
			Check(); err != nil {
			return err
		}

		annotationsExist(t, annotations, v)

		// Compare the body
		if err := v.Select("{.Body}").
			Equals("10.43.240.10", "{.addresses[0]}").
			Equals(fmt.Sprintf("kube-dns.%s.svc.cluster.local", ns), "{.hosts[0]}").
			Equals(1, "{.location}").
			Equals(1, "{.resolution}").
			Equals(fmt.Sprintf("spiffe://cluster.local/ns/%s/sa/kube-dns", ns), "{.subject_alt_names[0]}").
			Check(); err != nil {
			return err
		}

		// Compare Port
		if err := v.Select("{.Body.ports[0]}").
			Equals("dns-tcp", "{.name}").
			Equals(53, "{.number}").
			Equals("TCP", "{.protocol}").
			Check(); err != nil {
			return err
		}

		// Compare Endpoints
		if err := v.Select("{.Body.endpoints[0]}").
			Equals("10.40.0.5", "{.address}").
			Equals("us-central1/us-central1-a", "{.locality}").
			Equals(53, "{.ports['dns-tcp']}").
			Equals("kube-dns", "{.labels['k8s-app']}").
			Equals("123", "{.labels['pod-template-hash']}").
			Check(); err != nil {
			return err
		}

		if err := v.Select("{.Body.endpoints[1]}").
			Equals("10.40.1.4", "{.address}").
			Equals("us-central1/us-central1-a", "{.locality}").
			Equals(53, "{.ports['dns-tcp']}").
			Equals("kube-dns", "{.labels['k8s-app']}").
			Equals("456", "{.labels['pod-template-hash']}").
			Check(); err != nil {
			return err
		}

		return nil
	})
}
