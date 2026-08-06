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
	"fmt"
	"log"

	api "github.com/netrisai/netriswebapi/v2"
	"github.com/netrisai/netriswebapi/v2/types/vnet"
)

// findVNetByName looks up a V-Net by exact name. Returns error if not found.
func findVNetByName(clientset *api.Clientset, name string) (int, error) {
	vnets, err := clientset.VNet().Get()
	if err != nil {
		return 0, fmt.Errorf("listing V-Nets: %w", err)
	}
	for _, v := range vnets {
		if v.Name == name {
			return v.ID, nil
		}
	}
	return 0, fmt.Errorf("V-Net %q not found", name)
}

// replaceVNetGateways replaces all gateways on a V-Net with the given gateways,
// preserving all other fields (ports, sites, tags, etc.).
// Supports multiple gateways per V-Net for dual-stack (IPv4 + IPv6).
func replaceVNetGateways(clientset *api.Clientset, vnetID int, gateways []string) error {
	detail, err := clientset.VNet().GetByID(vnetID)
	if err != nil {
		return fmt.Errorf("fetching V-Net %d: %w", vnetID, err)
	}

	update := detailedToVNetUpdate(detail)
	gwList := make([]vnet.VNetUpdateGateway, len(gateways))
	for i, gw := range gateways {
		gwList[i] = vnet.VNetUpdateGateway{Prefix: gw}
	}
	update.Gateways = gwList

	reply, err := clientset.VNet().Update(vnetID, &update)
	if err != nil {
		return fmt.Errorf("updating V-Net gateways: %w", err)
	}
	if reply.StatusCode != 200 {
		return fmt.Errorf("updating V-Net gateways (HTTP %d): %s", reply.StatusCode, string(reply.Data))
	}

	log.Printf("[DEBUG] replaceVNetGateways: V-Net %d gateways set to %v", vnetID, gateways)
	return nil
}

// groupGatewaysByPostfix groups vnet_gateway set entries by postfix.
// Input: list of {postfix, gateway} maps from a TypeSet.
// Output: map of postfix → list of gateway CIDR strings.
func groupGatewaysByPostfix(entries []interface{}) map[string][]string {
	result := make(map[string][]string)
	for _, item := range entries {
		m := item.(map[string]interface{})
		postfix := m["postfix"].(string)
		gateway := m["gateway"].(string)
		result[postfix] = append(result[postfix], gateway)
	}
	return result
}

// detailedToVNetUpdate converts a VNetDetailed response to a VNetUpdate request,
// preserving all existing fields. Used when updating a single field (e.g. gateway)
// on a V-Net managed by a server cluster template.
func detailedToVNetUpdate(v *vnet.VNetDetailed) vnet.VNetUpdate {
	sites := make([]vnet.VNetUpdateSite, len(v.Sites))
	for i, s := range v.Sites {
		sites[i] = vnet.VNetUpdateSite{ID: s.ID, Name: s.Name}
	}

	ports := make([]vnet.VNetUpdatePort, len(v.Ports))
	for i, p := range v.Ports {
		ports[i] = vnet.VNetUpdatePort{
			Access:     p.Access,
			AccessMode: p.AccessMode,
			Name:       p.Name,
			ID:         p.ID,
			Lacp:       p.Lacp,
			Maccount:   p.MacCount,
			State:      p.State.Value,
			Untagged:   p.Untagged,
			Vlan:       p.Vlan,
		}
	}

	guestTenants := make([]vnet.VNetUpdateGuestTenant, len(v.GuestTenants))
	for i, gt := range v.GuestTenants {
		guestTenants[i] = vnet.VNetUpdateGuestTenant{ID: gt.ID, Name: gt.Name}
	}

	gateways := make([]vnet.VNetUpdateGateway, len(v.Gateways))
	for i, g := range v.Gateways {
		gateways[i] = vnet.VNetUpdateGateway{
			Prefix:         g.Prefix,
			Vlan:           g.Vlan,
			DHCPEnabled:    g.DHCPEnabled,
			DHCPLeaseCount: g.DHCPLeaseCount,
			DHCP:           g.DHCP,
		}
	}

	portTags := make([]vnet.VNetPortTag, len(v.PortTags))
	copy(portTags, v.PortTags)

	tags := make([]string, len(v.Tags))
	copy(tags, v.Tags)

	return vnet.VNetUpdate{
		Name:         v.Name,
		IPFamily:     v.IPFamily,
		NativeVlan:   v.NativeVlan,
		Ports:        ports,
		Provisioning: v.Provisioning,
		Sites:        sites,
		State:        v.State,
		Vlans:        v.Vlans,
		Vlan:         v.Vlan,
		Tags:         tags,
		PortTags:     portTags,
		Gateways:     gateways,
		GuestTenants: guestTenants,
		VxlanID:      v.VxlanID,
		DhcpRelay:    v.DhcpRelay,
		Dhcpv6Relay:  v.Dhcpv6Relay,
		IPv6ND:       v.IPv6ND,
	}
}
