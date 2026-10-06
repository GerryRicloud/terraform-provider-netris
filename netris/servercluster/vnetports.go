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
	"log"
	"strconv"

	api "github.com/netrisai/netriswebapi/v2"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

// netris_vnet_ports attaches a declared set of switch ports to an existing
// V-Net (typically one created by a Server Cluster template). It is the
// explicit "port binding" half of bare-metal onboarding: while a server is
// not yet commissioned, its frontend switch ports are bound directly to the
// parking V-Net here; after commissioning, membership in a Server Cluster
// (whose server↔port mapping comes from inventory links) programs the ports.
//
// The merge is additive and scoped to the declared ports: every other member
// of the V-Net — ports dynamically programmed via server cluster membership,
// or ports declared by another configuration — is preserved untouched.
// Destroy removes exactly the declared ports. Multiple configurations may
// each manage their own netris_vnet_ports on the same V-Net (mirroring how
// per-host-batch membership is managed), as long as they do not declare the
// same port; concurrent applies against one V-Net race (last PUT wins),
// same as any read-modify-write.
//
// Read is an existence check: it verifies the V-Net still exists but does
// not reconcile port membership — the binding is short-lived commissioning
// scaffolding, and out-of-band port changes are converged by the next
// update or a taint/replace.
func ResourceVNetPorts() *schema.Resource {
	return &schema.Resource{
		Description: "Attaches static switch ports to an existing V-Net without owning it. Use for hosts outside server cluster membership (e.g. parking bare-metal servers during MAAS commissioning): the declared ports are merged into the V-Net's member list, preserving all other members; destroy removes exactly the declared ports.",
		Schema: map[string]*schema.Schema{
			"vnet_id": {
				Type:        schema.TypeInt,
				Required:    true,
				ForceNew:    true,
				Description: "ID of the V-Net to bind ports to.",
			},
			"ports": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
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
				Description: "Set of switch ports to bind to the V-Net. Multiple ports from different switches form an EVPN multihoming group automatically.",
			},
		},
		Create: resourceVNetPortsCreate,
		Read:   resourceVNetPortsRead,
		Update: resourceVNetPortsUpdate,
		Delete: resourceVNetPortsDelete,
		Exists: resourceVNetPortsExists,
		Importer: &schema.ResourceImporter{
			State: resourceVNetPortsImport,
		},
	}
}

// groupPorts converts `ports` set entries to staticPort list. Entries are
// deduplicated by port name (last one wins) — the schema set hash includes
// the untagged flag, so the same port could otherwise appear twice.
func groupPorts(entries []interface{}) []staticPort {
	var result []staticPort
	index := make(map[string]int)
	for _, item := range entries {
		m := item.(map[string]interface{})
		port := staticPort{
			Name:     m["port"].(string),
			Untagged: m["untagged"].(bool),
		}
		if i, ok := index[port.Name]; ok {
			result[i] = port
			continue
		}
		index[port.Name] = len(result)
		result = append(result, port)
	}
	return result
}

func resourceVNetPortsCreate(d *schema.ResourceData, m interface{}) error {
	clientset := m.(*api.Clientset)

	vnetID := d.Get("vnet_id").(int)
	ports := groupPorts(d.Get("ports").(*schema.Set).List())

	if err := mergeVNetPorts(clientset, vnetID, ports, nil); err != nil {
		return err
	}

	d.SetId(strconv.Itoa(vnetID))
	return nil
}

func resourceVNetPortsRead(d *schema.ResourceData, m interface{}) error {
	clientset := m.(*api.Clientset)

	vnetID, err := strconv.Atoi(d.Id())
	if err != nil {
		return err
	}

	exists, err := vnetExists(clientset, vnetID)
	if err != nil {
		return err
	}
	if !exists {
		log.Printf("[DEBUG] netris_vnet_ports: V-Net %d gone, dropping from state", vnetID)
		d.SetId("")
	}
	return nil
}

func resourceVNetPortsUpdate(d *schema.ResourceData, m interface{}) error {
	clientset := m.(*api.Clientset)

	vnetID, err := strconv.Atoi(d.Id())
	if err != nil {
		return err
	}

	old, new := d.GetChange("ports")
	if err := mergeVNetPorts(clientset, vnetID, groupPorts(new.(*schema.Set).List()), groupPorts(old.(*schema.Set).List())); err != nil {
		return err
	}
	return nil
}

func resourceVNetPortsDelete(d *schema.ResourceData, m interface{}) error {
	clientset := m.(*api.Clientset)

	vnetID, err := strconv.Atoi(d.Id())
	if err != nil {
		return err
	}

	// A vanished V-Net already implies the binding is gone.
	exists, err := vnetExists(clientset, vnetID)
	if err != nil {
		return err
	}
	if !exists {
		d.SetId("")
		return nil
	}

	ports := groupPorts(d.Get("ports").(*schema.Set).List())
	if err := mergeVNetPorts(clientset, vnetID, nil, ports); err != nil {
		return err
	}

	d.SetId("")
	return nil
}

func resourceVNetPortsExists(d *schema.ResourceData, m interface{}) (bool, error) {
	clientset := m.(*api.Clientset)

	vnetID, err := strconv.Atoi(d.Id())
	if err != nil {
		return false, err
	}

	return vnetExists(clientset, vnetID)
}

func resourceVNetPortsImport(d *schema.ResourceData, m interface{}) ([]*schema.ResourceData, error) {
	vnetID, err := strconv.Atoi(d.Id())
	if err != nil {
		return nil, err
	}
	if err := d.Set("vnet_id", vnetID); err != nil {
		return nil, err
	}
	return []*schema.ResourceData{d}, nil
}
