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

package vpcpeering

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/netrisai/netriswebapi/http"
	api "github.com/netrisai/netriswebapi/v2"
	"github.com/netrisai/netriswebapi/v2/types/vpcpeerings"
)

func Resource() *schema.Resource {
	return &schema.Resource{
		Description: "Creates and manages a VPC Peering between two VPCs",
		Schema: map[string]*schema.Schema{
			"name": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Name of the VPC peering.",
			},
			"vpca": {
				Type:     schema.TypeList,
				Required: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"id": {
							Type:        schema.TypeInt,
							Required:    true,
							ForceNew:    true,
							Description: "VPC ID of side A.",
						},
						"prefix_list_inbound_ipv4": {
							Type:        schema.TypeString,
							Optional:    true,
							Description: "Inbound IPv4 prefix list filter (e.g. 'permit 10.0.0.0/24 le 32').",
						},
						"prefix_list_inbound_ipv6": {
							Type:        schema.TypeString,
							Optional:    true,
							Description: "Inbound IPv6 prefix list filter.",
						},
					},
				},
			},
			"vpcb": {
				Type:     schema.TypeList,
				Required: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"id": {
							Type:        schema.TypeInt,
							Required:    true,
							ForceNew:    true,
							Description: "VPC ID of side B.",
						},
						"prefix_list_inbound_ipv4": {
							Type:        schema.TypeString,
							Optional:    true,
							Description: "Inbound IPv4 prefix list filter (e.g. 'permit 10.0.0.0/24 le 32').",
						},
						"prefix_list_inbound_ipv6": {
							Type:        schema.TypeString,
							Optional:    true,
							Description: "Inbound IPv6 prefix list filter.",
						},
					},
				},
			},
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

func vpcInfoFromState(d *schema.ResourceData, key string) vpcpeerings.VPCInfo {
	list := d.Get(key).([]interface{})
	if len(list) == 0 {
		return vpcpeerings.VPCInfo{}
	}
	m := list[0].(map[string]interface{})
	return vpcpeerings.VPCInfo{
		ID:                    m["id"].(int),
		PrefixListInboundIPv4: m["prefix_list_inbound_ipv4"].(string),
		PrefixListInboundIPv6: m["prefix_list_inbound_ipv6"].(string),
	}
}

func vpcInfoToState(d *schema.ResourceData, key string, info vpcpeerings.VPCInfo) error {
	return d.Set(key, []map[string]interface{}{
		{
			"id":                        info.ID,
			"prefix_list_inbound_ipv4":   info.PrefixListInboundIPv4,
			"prefix_list_inbound_ipv6":   info.PrefixListInboundIPv6,
		},
	})
}

func resourceCreate(d *schema.ResourceData, m interface{}) error {
	log.Println("[DEBUG] vpcPeeringCreate")
	clientset := m.(*api.Clientset)

	peering := &vpcpeerings.VPCPeeringW{
		Name: d.Get("name").(string),
		VpcA: vpcInfoFromState(d, "vpca"),
		VpcB: vpcInfoFromState(d, "vpcb"),
	}

	js, _ := json.Marshal(peering)
	log.Println("[DEBUG] vpcPeeringAdd", string(js))

	reply, err := clientset.VPCPeerings().Add(peering)
	if err != nil {
		return err
	}

	// The vpc-peering endpoint returns 201 Created on success (other
	// endpoints return 200).
	if reply.StatusCode != 200 && reply.StatusCode != 201 {
		return fmt.Errorf("%s", string(reply.Data))
	}

	data, err := reply.Parse()
	if err != nil {
		return err
	}

	idStruct := struct {
		ID int `json:"id"`
	}{}
	if err := http.Decode(data.Data, &idStruct); err != nil {
		return err
	}

	d.SetId(strconv.Itoa(idStruct.ID))
	return nil
}

func resourceRead(d *schema.ResourceData, m interface{}) error {
	log.Println("[DEBUG] vpcPeeringRead")
	clientset := m.(*api.Clientset)

	id, _ := strconv.Atoi(d.Id())
	peering, err := clientset.VPCPeerings().GetByID(id)
	if err != nil {
		return err
	}
	if peering == nil {
		d.SetId("")
		return nil
	}

	d.SetId(strconv.Itoa(peering.ID))
	if err := d.Set("name", peering.Name); err != nil {
		return err
	}
	if err := vpcInfoToState(d, "vpca", peering.VpcA); err != nil {
		return err
	}
	if err := vpcInfoToState(d, "vpcb", peering.VpcB); err != nil {
		return err
	}

	return nil
}

func resourceUpdate(d *schema.ResourceData, m interface{}) error {
	log.Println("[DEBUG] vpcPeeringUpdate")
	clientset := m.(*api.Clientset)

	id, _ := strconv.Atoi(d.Id())
	peering := &vpcpeerings.VPCPeeringW{
		Name: d.Get("name").(string),
		VpcA: vpcInfoFromState(d, "vpca"),
		VpcB: vpcInfoFromState(d, "vpcb"),
	}

	reply, err := clientset.VPCPeerings().Update(id, peering)
	if err != nil {
		return err
	}

	if reply.StatusCode != 200 {
		return fmt.Errorf("%s", string(reply.Data))
	}

	return nil
}

func resourceDelete(d *schema.ResourceData, m interface{}) error {
	clientset := m.(*api.Clientset)

	id, _ := strconv.Atoi(d.Id())
	reply, err := clientset.VPCPeerings().Delete(id)
	if err != nil {
		return err
	}

	if reply.StatusCode != 200 {
		return fmt.Errorf("%s", string(reply.Data))
	}

	d.SetId("")
	return nil
}

func resourceExists(d *schema.ResourceData, m interface{}) (bool, error) {
	clientset := m.(*api.Clientset)

	id, _ := strconv.Atoi(d.Id())
	peering, err := clientset.VPCPeerings().GetByID(id)
	if err != nil {
		log.Println("[DEBUG] vpcPeeringExists err:", err)
	}
	if peering == nil {
		return false, nil
	}
	return peering.ID > 0, nil
}

func resourceImport(d *schema.ResourceData, m interface{}) ([]*schema.ResourceData, error) {
	clientset := m.(*api.Clientset)

	items, _ := clientset.VPCPeerings().Get()
	name := d.Id()
	for _, item := range items {
		if item.Name == name {
			d.SetId(strconv.Itoa(item.ID))
			return []*schema.ResourceData{d}, nil
		}
	}

	return []*schema.ResourceData{d}, nil
}
