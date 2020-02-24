// Copyright Istio Authors
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

package docs

import (
	"testing"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/environment"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/istioio"
)

var (
	inst istio.Instance
)

func TestMain(m *testing.M) {
	framework.
		NewSuite("policy_enforcement", m).
		SetupOnEnv(environment.Kube, istio.Setup(&inst, nil)).
		RequireEnvironment(environment.Kube).
		Run()

}

//https://istio.io/docs/examples/bookinfo/
//https://preliminary.istio.io/docs/tasks/policy-enforcement/rate-limiting/
func TestRateLimitTask(t *testing.T) {
	framework.
		NewTest(t).
		Run(func(ctx framework.TestContext) {
			istioio.NewBuilder("policy_enforcement__rate_limiting").
				Add(istioio.Script{
					Input: istioio.Path("scripts/deploy_bookinfo.txt"),
				}).
				Defer(istioio.Script{
					Input: istioio.Inline{
						FileName: "cleanup.sh",
						Value: `
				kubectl delete -n default -f samples/bookinfo/platform/kube/bookinfo.yaml || true`,
					},
				}).
				BuildAndRun(ctx)
		})
}
