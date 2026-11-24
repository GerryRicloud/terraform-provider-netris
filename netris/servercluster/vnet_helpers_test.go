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
	"reflect"
	"sort"
	"testing"

	"github.com/netrisai/netriswebapi/v2/types/vnet"
)

func TestGroupGatewaysByPostfix(t *testing.T) {
	tests := []struct {
		name    string
		entries []interface{}
		want    map[string][]string
	}{
		{
			name:    "empty",
			entries: nil,
			want:    map[string][]string{},
		},
		{
			name: "single entry",
			entries: []interface{}{
				map[string]interface{}{"postfix": "frontend", "gateway": "10.0.1.1/24"},
			},
			want: map[string][]string{
				"frontend": {"10.0.1.1/24"},
			},
		},
		{
			name: "dual-stack same postfix",
			entries: []interface{}{
				map[string]interface{}{"postfix": "frontend", "gateway": "10.0.1.1/24"},
				map[string]interface{}{"postfix": "frontend", "gateway": "2001:db8::1/64"},
			},
			want: map[string][]string{
				"frontend": {"10.0.1.1/24", "2001:db8::1/64"},
			},
		},
		{
			name: "multiple postfixes",
			entries: []interface{}{
				map[string]interface{}{"postfix": "frontend", "gateway": "10.0.1.1/24"},
				map[string]interface{}{"postfix": "backend", "gateway": "10.0.2.1/24"},
			},
			want: map[string][]string{
				"frontend": {"10.0.1.1/24"},
				"backend":  {"10.0.2.1/24"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := groupGatewaysByPostfix(tt.entries)
			for _, v := range got {
				sort.Strings(v)
			}
			for _, v := range tt.want {
				sort.Strings(v)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("groupGatewaysByPostfix() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeGateways(t *testing.T) {
	gw := func(postfix, gateway string) interface{} {
		return map[string]interface{}{"postfix": postfix, "gateway": gateway}
	}
	tests := []struct {
		name           string
		primary, extra []interface{}
		want           map[string][]string
	}{
		{
			name:    "no extras",
			primary: []interface{}{gw("frontend", "10.0.1.1/24")},
			want:    map[string][]string{"frontend": {"10.0.1.1/24"}},
		},
		{
			name:    "the created gateway first, whatever the set order",
			primary: []interface{}{gw("frontend", "22.142.252.1/24")},
			extra:   []interface{}{gw("frontend", "22.142.248.1/22")},
			want:    map[string][]string{"frontend": {"22.142.252.1/24", "22.142.248.1/22"}},
		},
		{
			name:    "extras sorted, other postfixes untouched",
			primary: []interface{}{gw("frontend", "10.0.1.1/24"), gw("backend", "10.0.2.1/24")},
			extra:   []interface{}{gw("frontend", "10.0.9.1/24"), gw("frontend", "10.0.8.1/24")},
			want: map[string][]string{
				"frontend": {"10.0.1.1/24", "10.0.8.1/24", "10.0.9.1/24"},
				"backend":  {"10.0.2.1/24"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeGateways(tt.primary, tt.extra); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeGateways() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeGatewayList(t *testing.T) {
	host := vnet.VNetUpdateGateway{Prefix: "22.142.252.1/24", Vlan: "untagged", DHCPEnabled: true, DHCPLeaseCount: 3}
	got := mergeGatewayList([]vnet.VNetUpdateGateway{host},
		[]string{"22.142.252.1/24", "22.142.248.1/22", "198.51.100.1/24", "203.0.113.1/24"})
	want := []vnet.VNetUpdateGateway{
		host, // kept whole, DHCP fields included
		{Prefix: "22.142.248.1/22", Vlan: "untagged"},
		{Prefix: "198.51.100.1/24", Vlan: "untagged"},
		{Prefix: "203.0.113.1/24", Vlan: "untagged"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeGatewayList() = %+v, want %+v", got, want)
	}
	// The controller's order stands, whatever order the wanted list has
	// (mergeGateways sorts, and "2001:…" sorts before "22.…").
	v6 := vnet.VNetUpdateGateway{Prefix: "2001:db8::1/64", Vlan: "untagged"}
	got = mergeGatewayList([]vnet.VNetUpdateGateway{host, v6},
		[]string{"2001:db8::1/64", "22.142.252.1/24", "22.142.248.1/22"})
	want = []vnet.VNetUpdateGateway{host, v6, {Prefix: "22.142.248.1/22", Vlan: "untagged"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeGatewayList() = %+v, want %+v", got, want)
	}
	// A gateway no longer wanted is dropped.
	got = mergeGatewayList([]vnet.VNetUpdateGateway{host, v6}, []string{"22.142.252.1/24"})
	if !reflect.DeepEqual(got, []vnet.VNetUpdateGateway{host}) {
		t.Errorf("mergeGatewayList() = %+v, want the host gateway alone", got)
	}
	if got := mergeGatewayList([]vnet.VNetUpdateGateway{host}, nil); len(got) != 0 {
		t.Errorf("no wanted prefixes clears the list, got %+v", got)
	}
}
