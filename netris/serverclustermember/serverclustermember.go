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

package serverclustermember

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	api "github.com/netrisai/netriswebapi/v2"
	"github.com/netrisai/netriswebapi/v2/types/servercluster"
)

// Membership is a full-list read-modify-write: GET the cluster's current
// members, add/remove one server, PUT the full list back. Without
// serialization, two concurrent Create calls on the same cluster both GET
// the same snapshot and the second PUT silently drops the first member.
var clusterMu sync.Map // clusterID(int) -> *sync.Mutex

func clusterMutex(clusterID int) *sync.Mutex {
	v, _ := clusterMu.LoadOrStore(clusterID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Attaches a single server to a ServerCluster without owning the cluster.
// The cluster typically lives in a different Terraform state (e.g. created
// by the region root), while members are managed per host-batch. There is
// no per-server membership endpoint: membership is a full-list
// read-modify-write via GET + PUT /api/v2/server-cluster/{id} carrying the
// cluster's current tags plus the full servers list, so members of the
// same cluster can be managed from any number of configurations (modulo
// concurrent writers on the same cluster).
//
// Membership is a landing-zone assertion, not a continuously enforced one:
// bm-extension legitimately swaps servers between clusters at runtime (VM
// bind/unbind), so out-of-band membership removal is NOT treated as drift.
// The resource is dropped from state only when the cluster itself is gone.

func Resource() *schema.Resource {
	return &schema.Resource{
		Description: "Manages membership of a single server in an existing ServerCluster",
		Schema: map[string]*schema.Schema{
			"cluster_id": {
				Type:        schema.TypeInt,
				Required:    true,
				ForceNew:    true,
				Description: "ID of the ServerCluster.",
			},
			"server_id": {
				Type:        schema.TypeInt,
				Required:    true,
				ForceNew:    true,
				Description: "ID of the server to attach to the cluster.",
			},
		},
		Create: resourceCreate,
		Read:   resourceRead,
		Delete: resourceDelete,
		Exists: resourceExists,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
	}
}

func parseID(id string) (clusterID, serverID int, err error) {
	parts := strings.SplitN(id, "/", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected ID in the format cluster_id/server_id, got: %s", id)
	}
	clusterID, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid cluster_id in %q: %s", id, err)
	}
	serverID, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid server_id in %q: %s", id, err)
	}
	return clusterID, serverID, nil
}

func getCluster(clientset *api.Clientset, clusterID int) (*servercluster.ServerCluster, error) {
	cluster, err := clientset.ServerCluster().GetByID(clusterID)
	if err != nil {
		log.Println("[DEBUG] serverclustermember cluster read err:", err)
		return nil, err
	}
	if cluster == nil || cluster.ID <= 0 {
		return nil, fmt.Errorf("server cluster %d not found", clusterID)
	}
	return cluster, nil
}

func clusterExists(clientset *api.Clientset, clusterID int) bool {
	_, err := getCluster(clientset, clusterID)
	return err == nil
}

func memberIDs(cluster *servercluster.ServerCluster) map[int]bool {
	ids := map[int]bool{}
	for _, s := range cluster.Servers {
		ids[s.ID] = true
	}
	return ids
}

func updateMembers(clientset *api.Clientset, cluster *servercluster.ServerCluster, ids map[int]bool) error {
	servers := []servercluster.Servers{}
	for id := range ids {
		servers = append(servers, servercluster.Servers{ID: id})
	}
	update := &servercluster.ServerClusterU{Tags: cluster.Tags, Servers: servers}
	js, _ := json.Marshal(update)
	log.Println("[DEBUG] serverclustermember update", string(js))
	reply, err := clientset.ServerCluster().Update(cluster.ID, update)
	if err != nil {
		return err
	}
	if reply.StatusCode != 200 {
		return fmt.Errorf("%s", string(reply.Data))
	}
	return nil
}

func resourceCreate(d *schema.ResourceData, m interface{}) error {
	clientset := m.(*api.Clientset)
	clusterID := d.Get("cluster_id").(int)
	serverID := d.Get("server_id").(int)

	mu := clusterMutex(clusterID)
	mu.Lock()
	defer mu.Unlock()

	cluster, err := getCluster(clientset, clusterID)
	if err != nil {
		return err
	}

	members := memberIDs(cluster)
	if !members[serverID] {
		members[serverID] = true
		if err := updateMembers(clientset, cluster, members); err != nil {
			return err
		}
	}

	d.SetId(fmt.Sprintf("%d/%d", clusterID, serverID))
	return nil
}

func resourceRead(d *schema.ResourceData, m interface{}) error {
	clientset := m.(*api.Clientset)

	clusterID, serverID, err := parseID(d.Id())
	if err != nil {
		return err
	}

	// Not a membership check: out-of-band removal (runtime cluster swap) is
	// expected and must not be corrected. Only a vanished cluster invalidates
	// the resource.
	if !clusterExists(clientset, clusterID) {
		d.SetId("")
		return nil
	}

	d.Set("cluster_id", clusterID)
	d.Set("server_id", serverID)
	return nil
}

func resourceDelete(d *schema.ResourceData, m interface{}) error {
	clientset := m.(*api.Clientset)

	clusterID, serverID, err := parseID(d.Id())
	if err != nil {
		return err
	}

	mu := clusterMutex(clusterID)
	mu.Lock()
	defer mu.Unlock()

	cluster, err := getCluster(clientset, clusterID)
	if err != nil {
		// Cluster gone — nothing to detach from.
		d.SetId("")
		return nil
	}

	members := memberIDs(cluster)
	if members[serverID] {
		delete(members, serverID)
		if err := updateMembers(clientset, cluster, members); err != nil {
			return err
		}
	}

	d.SetId("")
	return nil
}

func resourceExists(d *schema.ResourceData, m interface{}) (bool, error) {
	clientset := m.(*api.Clientset)

	clusterID, _, err := parseID(d.Id())
	if err != nil {
		return false, err
	}

	return clusterExists(clientset, clusterID), nil
}
