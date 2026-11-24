/*
Copyright 2026. RiCloud, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package servercluster

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
	api "github.com/netrisai/netriswebapi/v2"
	"github.com/netrisai/netriswebapi/v2/types/vnet"
)

// fakeNetris is the part of the controller a server cluster with extra
// gateways touches: the cluster creates its V-Net with the gateways it is
// given (one per V-Net and family, as the real controller does), and a V-Net
// update replaces the gateway list as sent.
type fakeNetris struct {
	sync.Mutex
	t        *testing.T
	created  bool
	gateways []vnet.VNetUpdateGateway // the V-Net's, as the controller keeps them
	creates  []map[string]interface{} // server cluster POST bodies
	updates  [][]vnet.VNetUpdateGateway
}

const (
	fakeClusterID = 7
	fakeVNetID    = 70
	fakeCluster   = "sc-byo-gpu"
)

func (f *fakeNetris) reply(w http.ResponseWriter, data interface{}) {
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": data, "isSuccess": true})
}

func (f *fakeNetris) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.Lock()
	defer f.Unlock()
	body, _ := io.ReadAll(r.Body)
	path := r.URL.Path
	switch {
	case r.Method == "POST" && path == "/api/v2/server-cluster":
		var add map[string]interface{}
		_ = json.Unmarshal(body, &add)
		f.creates = append(f.creates, add)
		f.created = true
		f.gateways = nil
		for _, g := range createdGateways(add) {
			gw := g.(map[string]interface{})
			// the controller's own gateway: untagged, with its DHCP fields
			f.gateways = append(f.gateways, vnet.VNetUpdateGateway{
				Prefix: gw["address"].(string), Vlan: "untagged", DHCPLeaseCount: 3})
		}
		f.reply(w, map[string]int{"id": fakeClusterID})
	case r.Method == "GET" && path == fmt.Sprintf("/api/v2/server-cluster/%d", fakeClusterID):
		if !f.created {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		f.reply(w, map[string]interface{}{
			"id": fakeClusterID, "name": fakeCluster,
			"admin": map[string]interface{}{"id": 1}, "site": map[string]interface{}{"id": 1},
			"vpc":                map[string]interface{}{"id": 5},
			"srvClusterTemplate": map[string]interface{}{"id": 1},
			"resources":          map[string]interface{}{"vnets": []map[string]interface{}{{"id": fakeVNetID, "name": fakeCluster + "-frontend"}}},
		})
	case r.Method == "PUT" && path == fmt.Sprintf("/api/v2/server-cluster/%d", fakeClusterID):
		// the cluster's own fields; its V-Nets' gateways go by V-Net update
		f.reply(w, nil)
	case r.Method == "DELETE" && path == fmt.Sprintf("/api/v2/server-cluster/%d", fakeClusterID):
		f.created = false
		f.reply(w, nil)
	case r.Method == "GET" && path == fmt.Sprintf("/api/v2/vnet/%d", fakeVNetID):
		gws := make([]map[string]interface{}, len(f.gateways))
		for i, g := range f.gateways {
			gws[i] = map[string]interface{}{"prefix": g.Prefix, "vlan": g.Vlan,
				"dhcpEnabled": g.DHCPEnabled, "dhcpLeaseCount": g.DHCPLeaseCount}
		}
		f.reply(w, map[string]interface{}{"id": fakeVNetID, "name": fakeCluster + "-frontend",
			"vlan": 100, "vxlanID": 1100, "gateways": gws})
	case r.Method == "GET" && path == "/api/v2/vnet":
		f.reply(w, []map[string]interface{}{{"id": fakeVNetID, "name": fakeCluster + "-frontend"}})
	case r.Method == "PUT" && path == fmt.Sprintf("/api/v2/vnet/%d", fakeVNetID):
		var u vnet.VNetUpdate
		_ = json.Unmarshal(body, &u)
		f.updates = append(f.updates, u.Gateways)
		f.gateways = u.Gateways
		f.reply(w, nil)
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, path)
		http.Error(w, "unexpected", http.StatusNotFound)
	}
}

// createdGateways is the IPv4 gateways a server cluster POST asks for (the
// body nests them in its template reference).
func createdGateways(add map[string]interface{}) []interface{} {
	tmpl, _ := add["srvClusterTemplate"].(map[string]interface{})
	gws, _ := tmpl["ipv4Gateways"].([]interface{})
	return gws
}

func (f *fakeNetris) prefixes() []string {
	f.Lock()
	defer f.Unlock()
	out := make([]string, len(f.gateways))
	for i, g := range f.gateways {
		out[i] = g.Prefix
	}
	return out
}

func extraGatewayConfig(extras ...string) string {
	var b strings.Builder
	b.WriteString(`
resource "netris_servercluster" "gpu" {
  name       = "` + fakeCluster + `"
  adminid    = 1
  siteid     = 1
  vpcid      = 5
  templateid = 1
  vnet_gateway {
    postfix = "frontend"
    gateway = "22.142.252.1/24"
  }
`)
	for _, e := range extras {
		b.WriteString(`  vnet_extra_gateway {
    postfix = "frontend"
    gateway = "` + e + `"
  }
`)
	}
	b.WriteString("}\n")
	return b.String()
}

// A cluster created with three extra gateways (four in all), then changed:
// one extra dropped and another added. Each step's second plan is empty
// (the SDK harness checks it), so state reads back without drift.
func TestServerClusterExtraGateways(t *testing.T) {
	fake := &fakeNetris{t: t}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	provider := &schema.Provider{
		ResourcesMap: map[string]*schema.Resource{"netris_servercluster": Resource()},
		ConfigureFunc: func(*schema.ResourceData) (interface{}, error) {
			return api.Client(srv.URL, "test", "test", 5)
		},
	}

	gateways := func(want ...string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			got := fake.prefixes()
			if strings.Join(got, ",") != strings.Join(want, ",") {
				return fmt.Errorf("V-Net gateways %v, want %v", got, want)
			}
			return nil
		}
	}

	resource.UnitTest(t, resource.TestCase{
		Providers: map[string]terraform.ResourceProvider{"netris": provider},
		Steps: []resource.TestStep{
			{
				Config: extraGatewayConfig("22.142.248.1/22", "198.51.100.1/24", "203.0.113.1/24"),
				Check: resource.ComposeTestCheckFunc(
					// created with the host gateway only, the rest added after,
					// the host gateway first and kept whole
					func(*terraform.State) error {
						gws := createdGateways(fake.creates[0])
						if len(gws) != 1 || gws[0].(map[string]interface{})["address"] != "22.142.252.1/24" {
							return fmt.Errorf("cluster created with %v, want the host gateway alone", gws)
						}
						first := fake.updates[0][0]
						if first.Prefix != "22.142.252.1/24" || first.Vlan != "untagged" || first.DHCPLeaseCount != 3 {
							return fmt.Errorf("the host gateway changed on the update: %+v", first)
						}
						for _, g := range fake.updates[0][1:] {
							if g.Vlan != "untagged" {
								return fmt.Errorf("an extra gateway without the segment's vlan: %+v", g)
							}
						}
						return nil
					},
					gateways("22.142.252.1/24", "198.51.100.1/24", "203.0.113.1/24", "22.142.248.1/22"),
					resource.TestCheckResourceAttr("netris_servercluster.gpu", "vnet_gateway.#", "1"),
					resource.TestCheckResourceAttr("netris_servercluster.gpu", "vnet_extra_gateway.#", "3"),
				),
			},
			{
				Config: extraGatewayConfig("22.142.248.1/22", "192.0.2.1/24"),
				Check: resource.ComposeTestCheckFunc(
					// the gateways kept stay where they were, the new one after
					gateways("22.142.252.1/24", "22.142.248.1/22", "192.0.2.1/24"),
					resource.TestCheckResourceAttr("netris_servercluster.gpu", "vnet_extra_gateway.#", "2"),
				),
			},
			{
				// no extras: back to the host gateway alone
				Config: extraGatewayConfig(),
				Check: resource.ComposeTestCheckFunc(
					gateways("22.142.252.1/24"),
					resource.TestCheckResourceAttr("netris_servercluster.gpu", "vnet_extra_gateway.#", "0"),
				),
			},
		},
	})
}
