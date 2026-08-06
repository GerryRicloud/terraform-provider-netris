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
