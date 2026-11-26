/*
Copyright 2023. Netris, Inc.

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
	"log"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/helper/validation"
	"github.com/netrisai/netriswebapi/http"
	api "github.com/netrisai/netriswebapi/v2"
	"github.com/netrisai/netriswebapi/v2/types/servercluster"
	"github.com/netrisai/netriswebapi/v2/types/vnet"
)

func Resource() *schema.Resource {
	return &schema.Resource{
		Description: "Creates and manages ServerCluster",
		Schema: map[string]*schema.Schema{
			"name": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "User assigned name of ServerCluster.",
			},
			"adminid": {
				Required:    true,
				Type:        schema.TypeInt,
				ForceNew:    true,
				Description: "ID of Admin tenant. Users of this tenant will be permitted to edit this unit.",
			},
			"siteid": {
				Type:        schema.TypeInt,
				Required:    true,
				ForceNew:    true,
				Description: "The site ID where this ServerCluster belongs.",
			},
			"dynamicvpcgroupid": {
				Type:          schema.TypeInt,
				Optional:      true,
				Computed:      true,
				ForceNew:      true,
				ValidateFunc:  validation.IntAtLeast(1),
				ConflictsWith: []string{"vpcid"},
				Description:   "Dynamic VPC Group for untagged L3VPN template objects. Conflicts with vpcid and configured vpcmapping.",
			},
			"vpcid": {
				ForceNew:    true,
				Computed:    true,
				Optional:    true,
				Type:        schema.TypeInt,
				Description: "ID of VPC. If not specified, a new VPC will be created.",
			},
			"vpcmapping": {
				ForceNew:    true,
				Computed:    true,
				Optional:    true,
				Type:        schema.TypeSet,
				Description: "VPC mappings for server cluster template objects. Each VPC-backed template object postfix must be mapped exactly once when this block is used. Set id to an existing VPC ID, or set id to 0 to create a new VPC. Use group to place multiple template objects into the same new VPC.",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"id": {
							ForceNew:    true,
							Computed:    true,
							Optional:    true,
							Type:        schema.TypeInt,
							Description: "VPC ID. Use an existing VPC ID, or 0 to create a new VPC.",
						},
						"dynamicvpcgroupid": {
							Type:         schema.TypeInt,
							Optional:     true,
							ForceNew:     true,
							ValidateFunc: validation.IntAtLeast(1),
							Description:  "Dynamic VPC Group for this untagged L3VPN template object. Conflicts with an existing VPC ID, group or name.",
						},
						"group": {
							ForceNew:    true,
							Optional:    true,
							Type:        schema.TypeString,
							Description: "Grouping key for new VPC mappings. Template objects with the same group are created in the same new VPC.",
						},
						"name": {
							ForceNew:    true,
							Optional:    true,
							Type:        schema.TypeString,
							Description: "VPC name. For new VPC mappings, this can override the generated VPC name.",
						},
						"postfix": {
							ForceNew:    true,
							Required:    true,
							Type:        schema.TypeString,
							Description: "Server cluster template object postfix.",
						},
					},
				},
			},
			"templateid": {
				ForceNew:    true,
				Required:    true,
				Type:        schema.TypeInt,
				Description: "ID of Server Cluster Template.",
			},
			"tags": {
				Optional: true,
				Type:     schema.TypeSet,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"servers": {
				Optional: true,
				Computed: true,
				Type:     schema.TypeSet,
				Elem: &schema.Schema{
					Type: schema.TypeInt,
				},
			},
			"vlan": {
				Optional:    true,
				Computed:    true,
				ForceNew:    true,
				Type:        schema.TypeSet,
				Description: "Specifies the VLAN ID to use for a Server Cluster Template VNet whose VLAN mode is set to \"specify\". One block per VNet postfix.",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"postfix": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "Postfix of the VNet, as defined in the Server Cluster Template.",
						},
						"vlan_id": {
							Type:        schema.TypeInt,
							Required:    true,
							Description: "VLAN ID to assign to this VNet.",
						},
					},
				},
			},
			"vnet_gateway": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"postfix": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "V-Net name suffix (e.g. 'frontend' for V-Net named <cluster>-frontend).",
						},
						"gateway": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "Gateway CIDR (e.g. '10.100.1.1/24' or '2001:db8::1/64'). Multiple entries per postfix supported for dual-stack.",
						},
					},
				},
				Description: "Set of V-Net gateway entries. Each entry specifies a gateway CIDR for a V-Net identified by its postfix. Multiple entries with the same postfix enable dual-stack.",
			},
			"vnet_extra_gateway": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"postfix": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "V-Net name suffix (e.g. 'frontend').",
						},
						"gateway": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "Gateway CIDR (e.g. '10.100.8.1/22').",
						},
					},
				},
				Description: "More gateways on a V-Net, beside its vnet_gateway: a second subnet on the same segment. A server cluster creates the subnet of one gateway per V-Net and address family only, so these are added by a V-Net update after the cluster exists, and each one's subnet must already exist in the tenant's IPAM (a netris_subnet, purpose common, under the tenant's allocation).",
			},
			"vnet_dhcp_relay": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"postfix": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "V-Net name suffix (e.g. 'frontend').",
						},
						"enabled": {
							Type:        schema.TypeBool,
							Required:    true,
							Description: "Enable DHCP relay on the V-Net.",
						},
						"vpc_id": {
							Type:        schema.TypeInt,
							Optional:    true,
							Description: "VPC ID where the DHCP servers reside.",
						},
						"primary": {
							Type:        schema.TypeString,
							Optional:    true,
							Description: "Primary DHCP relay target address.",
						},
						"secondary": {
							Type:        schema.TypeString,
							Optional:    true,
							Description: "Secondary DHCP relay target address.",
						},
					},
				},
				Description: "Set of V-Net DHCP relay entries. Configures DHCP relay on auto-created V-Nets to forward DHCP requests to a remote DHCP server (e.g. MAAS rackd). Applied as a post-creation V-Net update.",
			},
			"vnet_ports": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"postfix": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "V-Net name suffix (e.g. 'frontend').",
						},
						"port": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "Switch port name in 'port@switch' format (e.g. 'swp10@leaf-01').",
						},
						"untagged": {
							Type:        schema.TypeBool,
							Optional:    true,
							Default:     true,
							Description: "Add the port as an untagged (access mode) member. Default true.",
						},
					},
				},
				Description: "Set of static switch port members for auto-created V-Nets. Binds ports of hosts outside server cluster membership (e.g. management servers like MAAS rackd) to a template V-Net. Merged into the V-Net port list without disturbing dynamically programmed members. Multiple ports from different switches form an EVPN multihoming group automatically.",
			},
			"vnet_ids": {
				Type:        schema.TypeMap,
				Computed:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: "Map of V-Net postfix to V-Net ID. Populated automatically from V-Nets created by the cluster template.",
			},
			"vnets": {
				Type:        schema.TypeList,
				Computed:    true,
				Description: "V-Net details auto-created by this cluster (read-only). Each entry has postfix, vlan_id, vxlan_id, gateway_ip, and prefix.",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"postfix": {
							Type:        schema.TypeString,
							Computed:    true,
							Description: "V-Net name postfix (e.g. 'frontend').",
						},
						"vlan_id": {
							Type:        schema.TypeInt,
							Computed:    true,
							Description: "VLAN ID assigned by Netris.",
						},
						"vxlan_id": {
							Type:        schema.TypeInt,
							Computed:    true,
							Description: "VXLAN ID assigned by Netris.",
						},
						"gateway_ip": {
							Type:        schema.TypeString,
							Computed:    true,
							Description: "Gateway CIDR (e.g. '10.0.0.1/24'). Empty if no gateway configured.",
						},
						"prefix": {
							Type:        schema.TypeString,
							Computed:    true,
							Description: "Subnet prefix (same as gateway_ip, includes CIDR). Empty if no gateway configured.",
						},
					},
				},
			},
		},
		CustomizeDiff: func(d *schema.ResourceDiff, m interface{}) error {
			// A read can derive the top-level group from per-object mappings.
			// Validate this conflict on creation, when computed values are absent.
			if d.Id() == "" && d.NewValueKnown("dynamicvpcgroupid") && d.Get("dynamicvpcgroupid").(int) > 0 && d.Get("vpcmapping").(*schema.Set).Len() > 0 {
				return fmt.Errorf("dynamicvpcgroupid conflicts with configured vpcmapping")
			}
			return validateVPCMappings(d.Get("vpcmapping").(*schema.Set).List())
		},
		Create: resourceCreate,
		Read:   resourceRead,
		Update: resourceUpdate,
		Delete: resourceDelete,
		Exists: resourceExists,
		Importer: &schema.ResourceImporter{
			State: resourceImport,
		},
	}
}

func DiffSuppress(k, old, new string, d *schema.ResourceData) bool {
	return true
}

func expandVLANs(d *schema.ResourceData) []servercluster.VNetVLAN {
	vlanList := d.Get("vlan").(*schema.Set).List()
	vlans := make([]servercluster.VNetVLAN, 0, len(vlanList))
	for _, v := range vlanList {
		vlanMap := v.(map[string]interface{})
		vlans = append(vlans, servercluster.VNetVLAN{
			Postfix: vlanMap["postfix"].(string),
			VLANID:  vlanMap["vlan_id"].(int),
		})
	}
	return vlans
}

func validateVPCMappings(mappings []interface{}) error {
	for _, item := range mappings {
		mapping := item.(map[string]interface{})
		if mapping["dynamicvpcgroupid"].(int) > 0 && (mapping["id"].(int) > 0 || mapping["group"].(string) != "" || mapping["name"].(string) != "") {
			return fmt.Errorf("vpcmapping %q: dynamicvpcgroupid conflicts with id, group and name", mapping["postfix"])
		}
	}
	return nil
}

func expandVPCMappings(mappingsList []interface{}) []servercluster.VPCMapping {
	mappings := []servercluster.VPCMapping{}
	for _, item := range mappingsList {
		mappingMap := item.(map[string]interface{})
		mapping := servercluster.VPCMapping{
			ID:      mappingMap["id"].(int),
			Postfix: mappingMap["postfix"].(string),
		}

		if group, ok := mappingMap["group"].(string); ok {
			mapping.Group = group
		}
		if name, ok := mappingMap["name"].(string); ok {
			mapping.Name = name
		}

		if id := mappingMap["dynamicvpcgroupid"].(int); id > 0 {
			mapping.DynamicVPCGroup = &servercluster.DynamicVPCGroupRef{ID: id}
		}
		mappings = append(mappings, mapping)
	}

	sort.Slice(mappings, func(i, j int) bool {
		return mappings[i].Postfix < mappings[j].Postfix
	})

	return mappings
}

func flattenVPCMappings(mappings []servercluster.VPCMapping) []map[string]interface{} {
	flattenedMappings := []map[string]interface{}{}
	for _, mapping := range mappings {
		flattenedMapping := map[string]interface{}{
			"id":      mapping.ID,
			"postfix": mapping.Postfix,
		}

		if mapping.Group != "" {
			flattenedMapping["group"] = mapping.Group
		}
		if mapping.Name != "" {
			flattenedMapping["name"] = mapping.Name
		}

		if mapping.DynamicVPCGroup != nil {
			flattenedMapping["dynamicvpcgroupid"] = mapping.DynamicVPCGroup.ID
		}
		flattenedMappings = append(flattenedMappings, flattenedMapping)
	}

	sort.Slice(flattenedMappings, func(i, j int) bool {
		return flattenedMappings[i]["postfix"].(string) < flattenedMappings[j]["postfix"].(string)
	})

	return flattenedMappings
}

func resourceCreate(d *schema.ResourceData, m interface{}) error {
	log.Println("[DEBUG] serverclusterCreate")
	clientset := m.(*api.Clientset)

	tagsList := d.Get("tags").(*schema.Set).List()
	tags := []string{}
	for _, tag := range tagsList {
		tags = append(tags, tag.(string))
	}

	serversList := d.Get("servers").(*schema.Set).List()
	servers := []int{}
	for _, server := range serversList {
		servers = append(servers, server.(int))
	}
	if err := validateVPCMappings(d.Get("vpcmapping").(*schema.Set).List()); err != nil {
		return err
	}
	if d.Get("dynamicvpcgroupid").(int) > 0 && (d.Get("vpcid").(int) > 0 || d.Get("vpcmapping").(*schema.Set).Len() > 0) {
		return fmt.Errorf("dynamicvpcgroupid conflicts with vpcid and configured vpcmapping")
	}
	vpcMappings := expandVPCMappings(d.Get("vpcmapping").(*schema.Set).List())

	sort.Ints(servers)
	sortedServers := []servercluster.Servers{}

	for _, sortedServer := range servers {
		sortedServers = append(sortedServers, servercluster.Servers{ID: sortedServer})
	}

	vpcID := d.Get("vpcid").(int)
	var vpc *servercluster.IDName
	if vpcID > 0 {
		vpc = &servercluster.IDName{ID: vpcID}
	}

	var ipv4Gateways []servercluster.VNetGatewayAssign
	var ipv6Gateways []servercluster.VNetGatewayAssign
	if gwSet, ok := d.GetOk("vnet_gateway"); ok {
		for _, item := range gwSet.(*schema.Set).List() {
			m := item.(map[string]interface{})
			gw := servercluster.VNetGatewayAssign{
				Postfix: m["postfix"].(string),
				Address: m["gateway"].(string),
			}
			if strings.Contains(gw.Address, ":") {
				ipv6Gateways = append(ipv6Gateways, gw)
			} else {
				ipv4Gateways = append(ipv4Gateways, gw)
			}
		}
	}

	serverclusterAdd := &servercluster.ServerClusterW{
		Name:                           d.Get("name").(string),
		Admin:                          servercluster.IDName{ID: d.Get("adminid").(int)},
		Site:                           servercluster.IDName{ID: d.Get("siteid").(int)},
		VPC:                            vpc,
		VPCList:                        vpcMappings,
		SrvClusterTemplate:             servercluster.IDName{ID: d.Get("templateid").(int)},
		SrvClusterTemplateVLANs:        expandVLANs(d),
		SrvClusterTemplateIPv4Gateways: ipv4Gateways,
		SrvClusterTemplateIPv6Gateways: ipv6Gateways,
		Tags:                           tags,
		Servers:                        sortedServers,
	}

	if id := d.Get("dynamicvpcgroupid").(int); id > 0 {
		serverclusterAdd.DynamicVPCGroup = &servercluster.DynamicVPCGroupRef{ID: id}
	}

	js, _ := json.Marshal(serverclusterAdd)
	log.Println("[DEBUG] serverclusterAdd", string(js))

	reply, err := clientset.ServerCluster().Add(serverclusterAdd)
	if err != nil {
		log.Println("[DEBUG]", err)
		return err
	}

	js, _ = json.Marshal(reply)
	log.Println("[DEBUG]", string(js))

	log.Println("[DEBUG]", string(reply.Data))

	idStruct := struct {
		ID int `json:"id"`
	}{}

	data, err := reply.Parse()
	if err != nil {
		log.Println("[DEBUG]", err)
		return err
	}

	err = http.Decode(data.Data, &idStruct)
	if err != nil {
		log.Println("[DEBUG]", err)
		return err
	}

	log.Println("[DEBUG] ID:", idStruct.ID)

	if reply.StatusCode != 200 {
		return fmt.Errorf("%s", reply.Data)
	}

	d.SetId(strconv.Itoa(idStruct.ID))

	// DHCP relay cannot be set inline at cluster creation (the server cluster
	// API doesn't accept it). Read back the cluster to get V-Net IDs, then
	// apply relay config as a post-creation V-Net update.
	if relaySet, ok := d.GetOk("vnet_dhcp_relay"); ok {
		relayByPostfix := groupDhcpRelayByPostfix(relaySet.(*schema.Set).List())
		clusterName := d.Get("name").(string)

		apiCluster, err := clientset.ServerCluster().GetByID(idStruct.ID)
		if err != nil {
			return fmt.Errorf("reading back cluster %d for DHCP relay: %w", idStruct.ID, err)
		}
		if apiCluster == nil {
			return fmt.Errorf("cluster %d not found after creation", idStruct.ID)
		}

		for _, v := range apiCluster.Resources.VNets {
			postfix := v.Name
			if strings.HasPrefix(v.Name, clusterName+"-") {
				postfix = strings.TrimPrefix(v.Name, clusterName+"-")
			}
			if relay, ok := relayByPostfix[postfix]; ok {
				if err := replaceVNetDhcpRelay(clientset, v.ID, relay); err != nil {
					return fmt.Errorf("setting DHCP relay for V-Net %q: %w", v.Name, err)
				}
			}
		}
	}

	// Extra gateways: the cluster creates the subnet of one gateway per V-Net
	// and family only, so the rest go on by a V-Net update, the created ones
	// first (their subnets exist already, as the schema requires).
	if extraSet, ok := d.GetOk("vnet_extra_gateway"); ok {
		merged := mergeGateways(d.Get("vnet_gateway").(*schema.Set).List(), extraSet.(*schema.Set).List())
		extras := groupGatewaysByPostfix(extraSet.(*schema.Set).List())
		clusterName := d.Get("name").(string)

		apiCluster, err := clientset.ServerCluster().GetByID(idStruct.ID)
		if err != nil {
			return fmt.Errorf("reading back cluster %d for extra gateways: %w", idStruct.ID, err)
		}
		if apiCluster == nil {
			return fmt.Errorf("cluster %d not found after creation", idStruct.ID)
		}

		found := make(map[string]bool)
		for _, v := range apiCluster.Resources.VNets {
			postfix := v.Name
			if strings.HasPrefix(v.Name, clusterName+"-") {
				postfix = strings.TrimPrefix(v.Name, clusterName+"-")
			}
			if _, ok := extras[postfix]; ok {
				found[postfix] = true
				if err := replaceVNetGateways(clientset, v.ID, merged[postfix]); err != nil {
					return fmt.Errorf("adding extra gateways to V-Net %q: %w", v.Name, err)
				}
			}
		}
		for postfix := range extras {
			if !found[postfix] {
				return fmt.Errorf("vnet_extra_gateway: cluster %q has no V-Net %q", clusterName, clusterName+"-"+postfix)
			}
		}
	}

	// Static port members (e.g. management servers) cannot be declared at
	// cluster creation either. Merge them into the template V-Nets after
	// creation, preserving dynamically programmed members.
	if portsSet, ok := d.GetOk("vnet_ports"); ok {
		portsByPostfix := groupPortsByPostfix(portsSet.(*schema.Set).List())
		clusterName := d.Get("name").(string)

		apiCluster, err := clientset.ServerCluster().GetByID(idStruct.ID)
		if err != nil {
			return fmt.Errorf("reading back cluster %d for static ports: %w", idStruct.ID, err)
		}
		if apiCluster == nil {
			return fmt.Errorf("cluster %d not found after creation", idStruct.ID)
		}

		for _, v := range apiCluster.Resources.VNets {
			postfix := v.Name
			if strings.HasPrefix(v.Name, clusterName+"-") {
				postfix = strings.TrimPrefix(v.Name, clusterName+"-")
			}
			if ports, ok := portsByPostfix[postfix]; ok {
				if err := mergeVNetPorts(clientset, v.ID, ports, nil); err != nil {
					return fmt.Errorf("adding static ports to V-Net %q: %w", v.Name, err)
				}
			}
		}
	}

	// Read back so Computed attributes (vnets, vnet_ids) are populated for
	// same-apply consumers of the module outputs; Create itself never sets them.
	return resourceRead(d, m)
}

func resourceRead(d *schema.ResourceData, m interface{}) error {
	log.Println("[DEBUG] serverclusterRead")
	clientset := m.(*api.Clientset)

	id, _ := strconv.Atoi(d.Id())
	apiServerCluster, err := clientset.ServerCluster().GetByID(id)

	if err != nil {
		return err
	}
	if apiServerCluster == nil {
		d.SetId("")
		return nil
	}

	d.SetId(strconv.Itoa(apiServerCluster.ID))
	err = d.Set("name", apiServerCluster.Name)
	if err != nil {
		return err
	}
	err = d.Set("adminid", apiServerCluster.Admin.ID)
	if err != nil {
		return err
	}
	err = d.Set("siteid", apiServerCluster.Site.ID)
	if err != nil {
		return err
	}
	groupID := 0
	if apiServerCluster.DynamicVPCGroup != nil {
		groupID = apiServerCluster.DynamicVPCGroup.ID
	}
	if err := d.Set("dynamicvpcgroupid", groupID); err != nil {
		return err
	}
	err = d.Set("vpcid", apiServerCluster.VPC.ID)
	if err != nil {
		return err
	}
	if d.Get("vpcmapping").(*schema.Set).Len() == 0 && len(apiServerCluster.VPCList) > 0 {
		err = d.Set("vpcmapping", flattenVPCMappings(apiServerCluster.VPCList))
		if err != nil {
			return err
		}
	}
	err = d.Set("templateid", apiServerCluster.SrvClusterTemplate.ID)
	if err != nil {
		return err
	}
	err = d.Set("tags", apiServerCluster.Tags)
	if err != nil {
		return err
	}

	serversList := apiServerCluster.Servers
	servers := []int{}
	for _, server := range serversList {
		servers = append(servers, server.ID)
	}
	sort.Ints(servers)

	err = d.Set("servers", servers)
	if err != nil {
		return err
	}

	if d.Get("vlan").(*schema.Set).Len() == 0 && len(apiServerCluster.SrvClusterTemplate.VLANs) > 0 {
		vlans := []interface{}{}
		for _, vlan := range apiServerCluster.SrvClusterTemplate.VLANs {
			vlans = append(vlans, map[string]interface{}{
				"postfix": vlan.Postfix,
				"vlan_id": vlan.VLANID,
			})
		}
		err = d.Set("vlan", vlans)
		if err != nil {
			return err
		}
	}

	// Read V-Net gateways and DHCP relay for drift detection.
	clusterName := apiServerCluster.Name
	configuredGWs := d.Get("vnet_gateway").(*schema.Set).List()
	configuredPostfixes := make(map[string]bool)
	for _, item := range configuredGWs {
		configuredPostfixes[item.(map[string]interface{})["postfix"].(string)] = true
	}
	// Gateways declared as extra are read back into vnet_extra_gateway, the
	// rest of a declared V-Net's into vnet_gateway.
	configuredExtra := make(map[string]bool)
	for _, item := range d.Get("vnet_extra_gateway").(*schema.Set).List() {
		e := item.(map[string]interface{})
		configuredPostfixes[e["postfix"].(string)] = true
		configuredExtra[e["postfix"].(string)+"|"+e["gateway"].(string)] = true
	}
	configuredRelays := d.Get("vnet_dhcp_relay").(*schema.Set).List()
	configuredRelayPostfixes := make(map[string]bool)
	for _, item := range configuredRelays {
		configuredRelayPostfixes[item.(map[string]interface{})["postfix"].(string)] = true
	}

	// Static port members keyed by postfix → "port@switch" name, for drift
	// detection limited to the declared set (dynamic members are ignored).
	configuredPortsByPostfix := make(map[string]map[string]map[string]interface{})
	for _, item := range d.Get("vnet_ports").(*schema.Set).List() {
		entry := item.(map[string]interface{})
		postfix := entry["postfix"].(string)
		if configuredPortsByPostfix[postfix] == nil {
			configuredPortsByPostfix[postfix] = make(map[string]map[string]interface{})
		}
		configuredPortsByPostfix[postfix][entry["port"].(string)] = entry
	}

	vnetIDs := make(map[string]string)
	gwEntries := []interface{}{}
	extraEntries := []interface{}{}
	vnetDetails := []interface{}{}
	relayEntries := []interface{}{}
	portEntries := []interface{}{}

	for _, v := range apiServerCluster.Resources.VNets {
		postfix := v.Name
		if strings.HasPrefix(v.Name, clusterName+"-") {
			postfix = strings.TrimPrefix(v.Name, clusterName+"-")
		}
		vnetIDs[postfix] = strconv.Itoa(v.ID)

		// Always fetch full V-Net detail — needed for vlan_id (vnets block)
		// and for dual-stack gateway reading (vnet_gateway drift).
		var gateways []string
		var vlanID, vxlanID int
		detail, err := clientset.VNet().GetByID(v.ID)
		if err != nil {
			log.Printf("[WARN] fetching V-Net %d detail: %v", v.ID, err)
		} else if detail != nil {
			vlanID = detail.Vlan
			vxlanID = detail.VxlanID
			for _, gw := range detail.Gateways {
				gateways = append(gateways, gw.Prefix)
			}

			// Read DHCP relay for configured postfixes
			if configuredRelayPostfixes[postfix] && detail.DhcpRelay != nil {
				entry := map[string]interface{}{
					"postfix": postfix,
					"enabled": detail.DhcpRelay.Enabled,
				}
				if detail.DhcpRelay.Vpc != nil {
					entry["vpc_id"] = detail.DhcpRelay.Vpc.ID
				}
				if detail.DhcpRelay.PrimaryAddr != nil {
					entry["primary"] = *detail.DhcpRelay.PrimaryAddr
				}
				if detail.DhcpRelay.SecondaryAddr != nil {
					entry["secondary"] = *detail.DhcpRelay.SecondaryAddr
				}
				relayEntries = append(relayEntries, entry)
			}

			// Static port member drift: emit declared ports that are present
			// in the V-Net with their live state. Missing ports are omitted,
			// producing a plan diff that re-adds them.
			if declared, ok := configuredPortsByPostfix[postfix]; ok {
				for _, p := range detail.Ports {
					name := fmt.Sprintf("%s@%s", p.Port, p.SwitchName)
					if _, ok := declared[name]; ok {
						portEntries = append(portEntries, map[string]interface{}{
							"postfix":  postfix,
							"port":     name,
							"untagged": p.Untagged,
						})
					}
				}
			}
		}
		// Populate vnets computed block
		gwIP := ""
		if len(gateways) > 0 {
			gwIP = gateways[0]
		}
		vnetDetails = append(vnetDetails, map[string]interface{}{
			"postfix":    postfix,
			"vlan_id":    vlanID,
			"vxlan_id":   vxlanID,
			"gateway_ip": gwIP,
			"prefix":     gwIP,
		})

		// Populate vnet_gateway drift entries for configured postfixes
		if configuredPostfixes[postfix] {
			for _, gw := range gateways {
				entry := map[string]interface{}{
					"postfix": postfix,
					"gateway": gw,
				}
				if configuredExtra[postfix+"|"+gw] {
					extraEntries = append(extraEntries, entry)
				} else {
					gwEntries = append(gwEntries, entry)
				}
			}
		}
	}

	err = d.Set("vnet_ids", vnetIDs)
	if err != nil {
		return err
	}
	err = d.Set("vnet_gateway", gwEntries)
	if err != nil {
		return err
	}
	err = d.Set("vnet_extra_gateway", extraEntries)
	if err != nil {
		return err
	}
	err = d.Set("vnets", vnetDetails)
	if err != nil {
		return err
	}
	err = d.Set("vnet_dhcp_relay", relayEntries)
	if err != nil {
		return err
	}
	err = d.Set("vnet_ports", portEntries)
	if err != nil {
		return err
	}

	return nil
}

func resourceUpdate(d *schema.ResourceData, m interface{}) error {
	log.Println("[DEBUG] serverclusterUpdate")
	clientset := m.(*api.Clientset)

	serverclusterID, _ := strconv.Atoi(d.Id())

	tagsList := d.Get("tags").(*schema.Set).List()
	tags := []string{}
	for _, tag := range tagsList {
		tags = append(tags, tag.(string))
	}

	servers := []int{}
	if d.HasChange("servers") {
		serversList := d.Get("servers").(*schema.Set).List()
		for _, server := range serversList {
			servers = append(servers, server.(int))
		}
	} else {
		// `servers` is bm-extension-owned when the config does not declare
		// it; PUT the controller's live members instead of the refresh-time
		// snapshot so a concurrent membership change is not reverted.
		current, err := clientset.ServerCluster().GetByID(serverclusterID)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("server cluster %s not found", d.Id())
		}
		for _, server := range current.Servers {
			servers = append(servers, server.ID)
		}
	}

	sort.Ints(servers)
	sortedServers := []servercluster.Servers{}

	for _, sortedServer := range servers {
		sortedServers = append(sortedServers, servercluster.Servers{ID: sortedServer})
	}

	serverclusterUpdate := &servercluster.ServerClusterU{
		Tags:    tags,
		Servers: sortedServers,
	}

	js, _ := json.Marshal(serverclusterUpdate)
	log.Println("[DEBUG] serverclusterUpdate", string(js))

	reply, err := clientset.ServerCluster().Update(serverclusterID, serverclusterUpdate)
	if err != nil {
		log.Println("[DEBUG]", err)
		return err
	}

	js, _ = json.Marshal(reply)
	log.Println("[DEBUG]", string(js))

	log.Println("[DEBUG]", string(reply.Data))

	idStruct := struct {
		ID int `json:"id"`
	}{}

	data, err := reply.Parse()
	if err != nil {
		log.Println("[DEBUG]", err)
		return err
	}

	err = http.Decode(data.Data, &idStruct)
	if err != nil {
		log.Println("[DEBUG]", err)
		return err
	}

	log.Println("[DEBUG] ID:", idStruct.ID)

	if reply.StatusCode != 200 {
		return fmt.Errorf("%s", reply.Data)
	}

	if d.HasChange("vnet_gateway") || d.HasChange("vnet_extra_gateway") {
		oldGW, newGW := d.GetChange("vnet_gateway")
		oldExtra, newExtra := d.GetChange("vnet_extra_gateway")
		oldByPostfix := mergeGateways(oldGW.(*schema.Set).List(), oldExtra.(*schema.Set).List())
		newByPostfix := mergeGateways(newGW.(*schema.Set).List(), newExtra.(*schema.Set).List())
		clusterName := d.Get("name").(string)
		vnetIDsMap := d.Get("vnet_ids").(map[string]interface{})

		resolveVNetID := func(postfix string) (int, error) {
			if idStr, ok := vnetIDsMap[postfix]; ok {
				id, _ := strconv.Atoi(idStr.(string))
				if id > 0 {
					return id, nil
				}
			}
			return findVNetByName(clientset, clusterName+"-"+postfix)
		}

		for postfix, gateways := range newByPostfix {
			vnetID, err := resolveVNetID(postfix)
			if err != nil {
				return fmt.Errorf("finding V-Net %q: %w", clusterName+"-"+postfix, err)
			}
			if err := replaceVNetGateways(clientset, vnetID, gateways); err != nil {
				return fmt.Errorf("updating gateways for V-Net %q: %w", clusterName+"-"+postfix, err)
			}
		}

		// Clear gateways for postfixes removed from config.
		for postfix := range oldByPostfix {
			if _, ok := newByPostfix[postfix]; ok {
				continue
			}
			vnetID, err := resolveVNetID(postfix)
			if err != nil {
				log.Printf("[WARN] could not find V-Net %q to clear gateways: %v", clusterName+"-"+postfix, err)
				continue
			}
			if err := replaceVNetGateways(clientset, vnetID, nil); err != nil {
				return fmt.Errorf("clearing gateways for V-Net %q: %w", clusterName+"-"+postfix, err)
			}
		}
	}

	if d.HasChange("vnet_dhcp_relay") {
		old, new := d.GetChange("vnet_dhcp_relay")
		oldByPostfix := groupDhcpRelayByPostfix(old.(*schema.Set).List())
		newByPostfix := groupDhcpRelayByPostfix(new.(*schema.Set).List())
		clusterName := d.Get("name").(string)
		vnetIDsMap := d.Get("vnet_ids").(map[string]interface{})

		resolveVNetID := func(postfix string) (int, error) {
			if idStr, ok := vnetIDsMap[postfix]; ok {
				id, _ := strconv.Atoi(idStr.(string))
				if id > 0 {
					return id, nil
				}
			}
			return findVNetByName(clientset, clusterName+"-"+postfix)
		}

		for postfix, relay := range newByPostfix {
			vnetID, err := resolveVNetID(postfix)
			if err != nil {
				return fmt.Errorf("finding V-Net %q: %w", clusterName+"-"+postfix, err)
			}
			if err := replaceVNetDhcpRelay(clientset, vnetID, relay); err != nil {
				return fmt.Errorf("updating DHCP relay for V-Net %q: %w", clusterName+"-"+postfix, err)
			}
		}

		// Clear DHCP relay for postfixes removed from config.
		for postfix := range oldByPostfix {
			if _, ok := newByPostfix[postfix]; ok {
				continue
			}
			vnetID, err := resolveVNetID(postfix)
			if err != nil {
				log.Printf("[WARN] could not find V-Net %q to clear DHCP relay: %v", clusterName+"-"+postfix, err)
				continue
			}
			if err := replaceVNetDhcpRelay(clientset, vnetID, &vnet.VNetDhcpRelay{Enabled: false}); err != nil {
				return fmt.Errorf("clearing DHCP relay for V-Net %q: %w", clusterName+"-"+postfix, err)
			}
		}
	}

	if d.HasChange("vnet_ports") {
		old, new := d.GetChange("vnet_ports")
		oldByPostfix := groupPortsByPostfix(old.(*schema.Set).List())
		newByPostfix := groupPortsByPostfix(new.(*schema.Set).List())
		clusterName := d.Get("name").(string)
		vnetIDsMap := d.Get("vnet_ids").(map[string]interface{})

		resolveVNetID := func(postfix string) (int, error) {
			if idStr, ok := vnetIDsMap[postfix]; ok {
				id, _ := strconv.Atoi(idStr.(string))
				if id > 0 {
					return id, nil
				}
			}
			return findVNetByName(clientset, clusterName+"-"+postfix)
		}

		// Union of postfixes touched on either side. Removed entries are
		// passed as the drop list so their ports leave the V-Net.
		postfixes := make(map[string]bool)
		for postfix := range oldByPostfix {
			postfixes[postfix] = true
		}
		for postfix := range newByPostfix {
			postfixes[postfix] = true
		}
		for postfix := range postfixes {
			vnetID, err := resolveVNetID(postfix)
			if err != nil {
				return fmt.Errorf("finding V-Net %q: %w", clusterName+"-"+postfix, err)
			}
			if err := mergeVNetPorts(clientset, vnetID, newByPostfix[postfix], oldByPostfix[postfix]); err != nil {
				return fmt.Errorf("updating static ports on V-Net %q: %w", clusterName+"-"+postfix, err)
			}
		}
	}

	// Read back to refresh Computed attributes after in-place updates.
	return resourceRead(d, m)
}

func resourceExists(d *schema.ResourceData, m interface{}) (bool, error) {
	clientset := m.(*api.Clientset)

	id, _ := strconv.Atoi(d.Id())
	item, err := clientset.ServerCluster().GetByID(id)
	if err != nil {
		log.Println("[DEBUG] serverclusterExist response err:", err)
	}

	if item == nil {
		return false, nil
	}
	if item.ID > 0 {
		return true, nil
	}

	return false, nil
}

func resourceImport(d *schema.ResourceData, m interface{}) ([]*schema.ResourceData, error) {
	clientset := m.(*api.Clientset)

	items, _ := clientset.ServerCluster().Get()
	name := d.Id()
	for _, item := range items {
		if item.Name == name {
			d.SetId(strconv.Itoa(item.ID))
			return []*schema.ResourceData{d}, nil
		}
	}

	return []*schema.ResourceData{d}, nil
}

func resourceDelete(d *schema.ResourceData, m interface{}) error {
	clientset := m.(*api.Clientset)

	id, _ := strconv.Atoi(d.Id())
	reply, err := clientset.ServerCluster().Delete(id)
	if err != nil {
		return err
	}

	if reply.StatusCode != 200 {
		return fmt.Errorf("%s", reply.Data)
	}

	d.SetId("")
	return nil
}
