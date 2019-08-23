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
	"os"
	"testing"
	"time"

	xdsapi "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	kubeApiMeta "k8s.io/apimachinery/pkg/apis/meta/v1"

	"istio.io/istio/galley/pkg/metadata"
	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/echoboot"
	"istio.io/istio/pkg/test/framework/components/environment"
	"istio.io/istio/pkg/test/framework/components/galley"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/components/pilot"
	"istio.io/istio/pkg/test/kube"
	"istio.io/istio/pkg/test/util/structpath"
)

type testcase string

const (
	serviceEntry   testcase = "serviceEntry"
	serviceUpdate  testcase = "serviceEntry_service_update"
	endpointUpdate testcase = "serviceEntry_endpoint_update"
)

type testParam struct {
	env       environment.Name
	namespace string
	svcName   string
	clusterIP string
	portName  string
	port      string
	san       string
	endpoints []string
}

func newTestParams(ctx framework.TestContext, ns string) (t testParam) {
	ctx.Environment().Case(environment.Kube, func() {
		t = testParam{
			env:       environment.Kube,
			namespace: ns,
			svcName:   "hello-node",
			clusterIP: "10.7.245.79",
			portName:  "tcp",
			port:      "8880",
			san:       "default",
		}
	})
	ctx.Environment().Case(environment.Native, func() {
		t = testParam{
			env:       environment.Native,
			namespace: ns,
			svcName:   "kube-dns",
			clusterIP: "10.43.240.10",
			portName:  "dns-tcp",
			port:      "53",
			san:       "kube-dns",
			endpoints: []string{"10.40.0.5", "10.40.1.4"},
		}
	})
	return t
}

func (t *testParam) update() {
	if t.env == environment.Native {
		t.clusterIP = "10.43.240.11"
		return
	}
	t.port = "8889"
}

func TestSyntheticServiceEntry(t *testing.T) {
	ctx := framework.NewContext(t)
	defer ctx.Done()

	//TODO: is this how we get the kubeconfig??
	kubeConfig := os.Getenv("INTEGRATION_TEST_KUBECONFIG")
	accessor, err := kube.NewAccessor(kubeConfig, "")
	if err != nil {
		t.Fatal(err)
	}

	ns := namespace.NewOrFail(t, ctx, namespace.Config{Prefix: "sse", Inject: true})
	testParams := newTestParams(ctx, ns.Name())

	// apply a sse
	applyConfig(serviceEntry, ns, ctx, t)

	expectedAnnotations := []string{
		"networking.alpha.istio.io/serviceVersion",
		"networking.alpha.istio.io/endpointsVersion"}

	collection := metadata.IstioNetworkingV1alpha3SyntheticServiceentries.Collection.String()

	if err := g.WaitForSnapshot(collection, syntheticServiceEntryValidator(t, expectedAnnotations, testParams)); err != nil {
		t.Fatalf("failed waiting for %s:\n%v\n", collection, err)
	}

	ports := []echo.Port{
		{
			Name:     "http",
			Protocol: protocol.HTTP,
		},
		{
			Name:     "tcp",
			Protocol: protocol.TCP,
		},
	}
	var client echo.Instance
	echoboot.NewBuilderOrFail(t, ctx).
		With(&client, echo.Config{
			Ports:               ports,
			Service:             "echo",
			Namespace:           ns,
			Pilot:               p,
			Galley:              g,
			IncludeInboundPorts: "*",
		}).BuildOrFail(t)

	nodeID := client.WorkloadsOrFail(t)[0].Sidecar().NodeID()
	discoveryReq := pilot.NewDiscoveryRequest(nodeID, pilot.Listener)
	p.StartDiscoveryOrFail(t, discoveryReq)

	p.WatchDiscoveryOrFail(t, time.Second*10,
		func(response *xdsapi.DiscoveryResponse) (b bool, e error) {
			validator := structpath.ForProto(response)
			if validator.Select("{.resources[?(@.address.socketAddress.portValue==%v)]}", testParams.port).Check() != nil {
				return false, nil
			}
			validateSse(t, validator, testParams)
			endpoint := getEndpoints(ctx, t, testParams, accessor)
			// wait for the new instance to become ready
			if len(endpoint) == 0 {
				return false, nil
			}
			verifyEndpoints(t, client, testParams, endpoint)
			return true, nil
		})

	// update the service
	applyConfig(serviceUpdate, ns, ctx, t)
	testParams.update()

	p.WatchDiscoveryOrFail(t, time.Second*10,
		func(response *xdsapi.DiscoveryResponse) (b bool, e error) {
			validator := structpath.ForProto(response)
			if validator.Select("{.resources[?(@.address.socketAddress.portValue==%v)]}", testParams.port).Check() != nil {
				return false, nil
			}
			validateSse(t, validator, testParams)
			endpoint := getEndpoints(ctx, t, testParams, accessor)
			// wait for the new instance to become ready
			if len(endpoint) == 0 {
				return false, nil
			}
			verifyEndpoints(t, client, testParams, endpoint)
			return true, nil
		})

	// endpoint update
	applyConfig(endpointUpdate, ns, ctx, t)

	p.WatchDiscoveryOrFail(t, time.Second*10,
		func(response *xdsapi.DiscoveryResponse) (b bool, e error) {
			validator := structpath.ForProto(response)
			if validator.Select("{.resources[?(@.address.socketAddress.portValue==%v)]}", testParams.port).Check() != nil {
				return false, nil
			}
			validateSse(t, validator, testParams)
			endpoints := getEndpoints(ctx, t, testParams, accessor)
			// wait for the new replica to become ready
			if len(endpoints) != 2 {
				return false, nil
			}
			verifyEndpoints(t, client, testParams, endpoints)
			return true, nil
		})

	// check versionInfo when running native
	verifyVersionInfo(t, client, testParams, "/3")
}

func getEndpoints(ctx framework.TestContext, t *testing.T, params testParam, accessor *kube.Accessor) (addresses []string) {
	env := ctx.Environment().EnvironmentName()
	if env == environment.Native {
		return params.endpoints
	}
	eps, err := accessor.GetEndpoints(params.namespace, params.svcName, kubeApiMeta.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range eps.Subsets[0].Addresses {
		addresses = append(addresses, addr.IP)
	}
	return addresses
}

func verifyEndpoints(t *testing.T, c echo.Instance, params testParam, endpoints []string) {
	workloads, _ := c.Workloads()
	for _, w := range workloads {
		if w.Sidecar() != nil {
			msg, err := w.Sidecar().Clusters()
			if err != nil {
				t.Fatal(err)
			}
			validator := structpath.ForProto(msg)
			for _, endpoint := range endpoints {
				t.Run(fmt.Sprintf("verify endpoints for outbound|%s||%s.%s.svc.cluster.local", params.port, params.svcName, params.namespace), func(t *testing.T) {
					validator.
						Select("{.clusterStatuses[?(@.name=='%v')]}", fmt.Sprintf("outbound|%s||%s.%s.svc.cluster.local", params.port, params.svcName, params.namespace)).
						Equals("true", "{.addedViaApi}").
						ContainSubstring(endpoint, "{.hostStatuses}").
						ContainSubstring(params.port, "{.hostStatuses}").
						ContainSubstring("HEALTHY", "{.hostStatuses}").
						CheckOrFail(t)
				})
			}
		}
	}
}

func verifyVersionInfo(t *testing.T, c echo.Instance, params testParam, svcVersion string) {
	if params.env == environment.Native {
		workloads, _ := c.Workloads()
		for _, w := range workloads {
			if w.Sidecar() != nil {
				cfg, err := w.Sidecar().Config()
				if err != nil {
					t.Fatal(err)
				}
				clusterName := fmt.Sprintf("%s_%s", params.clusterIP, params.port)
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
}

func applyConfig(testName testcase, namespace namespace.Instance, ctx framework.TestContext, t *testing.T) {
	var resources []string
	ctx.Environment().Case(environment.Kube, func() {
		switch testName {
		case serviceEntry:
			resources = []string{
				"testdata/kube/service.yaml",
				"testdata/kube/deployment.yaml"}
		case serviceUpdate:
			resources = []string{
				"testdata/kube/service_update.yaml"}
		case endpointUpdate:
			resources = []string{
				"testdata/kube/deployment_update.yaml"}
		}
	})
	ctx.Environment().Case(environment.Native, func() {
		switch testName {
		case serviceEntry:
			resources = []string{
				"testdata/native/nodes.yaml",
				"testdata/native/pods.yaml",
				"testdata/native/service.yaml",
				"testdata/native/endpoints.yaml"}
		case serviceUpdate:
			resources = []string{
				"testdata/native/nodes.yaml",
				"testdata/native/pods.yaml",
				"testdata/native/service_update.yaml",
				"testdata/native/endpoints.yaml"}
		case endpointUpdate:
			resources = []string{
				"testdata/native/nodes.yaml",
				"testdata/native/pods.yaml",
				"testdata/native/service_update.yaml",
				"testdata/native/endpoint_update.yaml"}
		}
	})
	for _, config := range resources {
		if err := g.ApplyConfigDir(namespace, config); err != nil {
			t.Fatal(err)
		}
	}
}

func validateSse(t *testing.T, response *structpath.Instance, params testParam) {
	t.Run("SynthetiServiceEntry-listener", func(t *testing.T) {
		response.
			Select("{.resources[?(@.address.socketAddress.portValue==%s)]}", params.port).
			Equals(fmt.Sprintf("%s_%s", params.clusterIP, params.port), "{.name}").
			Equals(params.clusterIP, "{.address.socketAddress.address}").
			ContainSubstring(fmt.Sprintf("outbound|%s||%s", params.port, params.svcName), "{.filterChains[0]}").
			CheckOrFail(t)
	})
}

func annotationsExist(t *testing.T, annos []string, instance *structpath.Instance) {
	for _, anno := range annos {
		instance.ContainSubstring(anno, "{.Metadata.annotations}").CheckOrFail(t)
	}
}

func syntheticServiceEntryValidator(t *testing.T, annotations []string, params testParam) galley.SnapshotValidatorFunc {
	return galley.NewSingleObjectSnapshotValidator(params.namespace, func(ns string, actual *galley.SnapshotObject) error {
		v := structpath.ForProto(actual)
		// check for svc and endpoint version annotations
		annotationsExist(t, annotations, v)
		if err := v.Equals(metadata.IstioNetworkingV1alpha3SyntheticServiceentries.TypeURL.String(), "{.TypeURL}").
			Equals(fmt.Sprintf("%s/%s", params.namespace, params.svcName), "{.Metadata.name}").
			Check(); err != nil {
			return err
		}
		// Compare the body
		if err := v.Select("{.Body}").
			Equals(params.clusterIP, "{.addresses[0]}").
			Equals(fmt.Sprintf("%s.%s.svc.cluster.local", params.svcName, params.namespace), "{.hosts[0]}").
			Equals(1, "{.location}").
			Equals(1, "{.resolution}").
			Equals(fmt.Sprintf("spiffe://cluster.local/ns/%s/sa/%s", params.namespace, params.san), "{.subject_alt_names[0]}").
			Check(); err != nil {
			return err
		}
		// Compare Port
		if err := v.Select("{.Body.ports[0]}").
			Equals(params.portName, "{.name}").
			Equals(params.port, "{.number}").
			Equals("TCP", "{.protocol}").
			Check(); err != nil {
			return err
		}
		return nil
	})
}
