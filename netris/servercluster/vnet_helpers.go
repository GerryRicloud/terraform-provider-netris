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
	"strconv"

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
	if detail == nil {
		return fmt.Errorf("V-Net %d not found", vnetID)
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

// strPtr returns a pointer to s, or nil if s is empty.
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// replaceVNetDhcpRelay sets or clears DHCP relay on a V-Net, preserving
// all other fields (ports, sites, tags, gateways, etc.).
func replaceVNetDhcpRelay(clientset *api.Clientset, vnetID int, relay *vnet.VNetDhcpRelay) error {
	detail, err := clientset.VNet().GetByID(vnetID)
	if err != nil {
		return fmt.Errorf("fetching V-Net %d: %w", vnetID, err)
	}
	if detail == nil {
		return fmt.Errorf("V-Net %d not found", vnetID)
	}

	update := detailedToVNetUpdate(detail)
	update.DhcpRelay = relay

	reply, err := clientset.VNet().Update(vnetID, &update)
	if err != nil {
		return fmt.Errorf("updating V-Net DHCP relay: %w", err)
	}
	if reply.StatusCode != 200 {
		return fmt.Errorf("updating V-Net DHCP relay (HTTP %d): %s", reply.StatusCode, string(reply.Data))
	}

	log.Printf("[DEBUG] replaceVNetDhcpRelay: V-Net %d relay set", vnetID)
	return nil
}

// groupDhcpRelayByPostfix builds a map of postfix → VNetDhcpRelay from
// vnet_dhcp_relay set entries.
func groupDhcpRelayByPostfix(entries []interface{}) map[string]*vnet.VNetDhcpRelay {
	result := make(map[string]*vnet.VNetDhcpRelay)
	for _, item := range entries {
		m := item.(map[string]interface{})
		postfix := m["postfix"].(string)
		relay := &vnet.VNetDhcpRelay{
			Enabled:       m["enabled"].(bool),
			PrimaryAddr:   strPtr(m["primary"].(string)),
			SecondaryAddr: strPtr(m["secondary"].(string)),
		}
		if vpcID := m["vpc_id"].(int); vpcID > 0 {
			relay.Vpc = &vnet.IDName{ID: vpcID}
		}
		result[postfix] = relay
	}
	return result
}

// staticPort is a declared vnet_ports entry: a switch port to attach to a
// template-created V-Net (e.g. a management server NIC outside server
// cluster membership).
type staticPort struct {
	Name     string // "port@switch" format
	Untagged bool
}

// groupPortsByPostfix groups vnet_ports set entries by postfix. Entries are
// deduplicated by port name (last one wins) — the schema set hash includes
// the untagged flag, so the same port could otherwise appear twice.
func groupPortsByPostfix(entries []interface{}) map[string][]staticPort {
	result := make(map[string][]staticPort)
	index := make(map[string]map[string]int) // postfix → port name → result index
	for _, item := range entries {
		m := item.(map[string]interface{})
		postfix := m["postfix"].(string)
		port := staticPort{
			Name:     m["port"].(string),
			Untagged: m["untagged"].(bool),
		}
		if index[postfix] == nil {
			index[postfix] = make(map[string]int)
		}
		if i, ok := index[postfix][port.Name]; ok {
			result[postfix][i] = port
			continue
		}
		index[postfix][port.Name] = len(result[postfix])
		result[postfix] = append(result[postfix], port)
	}
	return result
}

// mergeVNetPorts attaches the declared static ports to a V-Net, refreshing
// existing members and dropping ports that were previously declared but
// removed from config (oldPorts). All other members — dynamically programmed
// by server cluster membership — are preserved. Multiple ports from different
// switches form an EVPN multihoming group automatically (no ESI knob exists
// in the API).
func mergeVNetPorts(clientset *api.Clientset, vnetID int, ports []staticPort, oldPorts []staticPort) error {
	detail, err := clientset.VNet().GetByID(vnetID)
	if err != nil {
		return fmt.Errorf("fetching V-Net %d: %w", vnetID, err)
	}
	if detail == nil {
		return fmt.Errorf("V-Net %d not found", vnetID)
	}

	update := detailedToVNetUpdate(detail)

	drop := make(map[string]bool, len(oldPorts))
	for _, p := range oldPorts {
		drop[p.Name] = true
	}
	for _, p := range ports {
		delete(drop, p.Name)
	}

	kept := make([]vnet.VNetUpdatePort, 0, len(detail.Ports)+len(ports))
	existingNames := make(map[string]bool, len(detail.Ports))
	for _, p := range detail.Ports {
		name := fmt.Sprintf("%s@%s", p.Port, p.SwitchName)
		if drop[name] {
			continue
		}
		existingNames[name] = true
		kept = append(kept, vnet.VNetUpdatePort{
			Access:     p.Access,
			AccessMode: p.AccessMode,
			Name:       name,
			ID:         p.ID,
			Lacp:       p.Lacp,
			Maccount:   p.MacCount,
			State:      p.State.Value,
			Untagged:   p.Untagged,
			Vlan:       p.Vlan,
		})
	}

	// Add or refresh the declared ports. Untagged members of a global-vlan
	// V-Net are submitted with the global vlan id (same convention as the
	// netris_vnet resource).
	newVlan := ""
	if detail.Vlan > 0 {
		newVlan = strconv.Itoa(detail.Vlan)
	}
	for _, p := range ports {
		if existingNames[p.Name] {
			for i := range kept {
				if kept[i].Name == p.Name {
					kept[i].AccessMode = p.Untagged
					kept[i].Untagged = p.Untagged
					kept[i].State = "active"
					break
				}
			}
			continue
		}
		vlan := ""
		if p.Untagged {
			vlan = newVlan
		}
		kept = append(kept, vnet.VNetUpdatePort{
			Name:       p.Name,
			AccessMode: p.Untagged,
			Untagged:   p.Untagged,
			Vlan:       vlan,
			State:      "active",
		})
	}

	update.Ports = kept

	reply, err := clientset.VNet().Update(vnetID, &update)
	if err != nil {
		return fmt.Errorf("updating V-Net ports: %w", err)
	}
	if reply.StatusCode != 200 {
		return fmt.Errorf("updating V-Net ports (HTTP %d): %s", reply.StatusCode, string(reply.Data))
	}

	log.Printf("[DEBUG] mergeVNetPorts: V-Net %d ports merged (%d declared, %d dropped)", vnetID, len(ports), len(drop))
	return nil
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
