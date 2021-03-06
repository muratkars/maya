/*
Copyright 2019 The OpenEBS Authors

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

package cspc

import (
	"github.com/golang/glog"
	nodeselect "github.com/openebs/maya/pkg/algorithm/nodeselect/v1alpha2"
	apis "github.com/openebs/maya/pkg/apis/openebs.io/v1alpha1"
	apisbd "github.com/openebs/maya/pkg/blockdevice/v1alpha2"
	apiscsp "github.com/openebs/maya/pkg/cstor/newpool/v1alpha3"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (pc *PoolConfig) handleOperations() {
	// TODO: Disable pool-mgmt reconciliation
	// before carrying out ant day 2 ops.
	pc.expandPool()
	pc.replaceBlockDevice()
	// Once all operations are executed enable pool-mgmt
	// reconciliation
}

// replaceBlockDevice replaces block devices in cStor pools as specified in CSPC.
func (pc *PoolConfig) replaceBlockDevice() {
	glog.V(2).Info("block device replacement is not supported yet")
}

// expandPool expands the required cStor pools as specified in CSPC
func (pc *PoolConfig) expandPool() error {
	for _, pool := range pc.AlgorithmConfig.CSPC.Spec.Pools {
		pool := pool
		var cspObj *apis.NewTestCStorPool
		nodeName, err := nodeselect.GetNodeFromLabelSelector(pool.NodeSelector)
		if err != nil {
			return errors.Wrapf(err,
				"could not get node name for node selector {%v} "+
					"from cspc %s", pool.NodeSelector, pc.AlgorithmConfig.CSPC.Name)
		}

		cspObj, err = pc.getCSPWithNodeName(nodeName)
		if err != nil {
			return errors.Wrapf(err, "failed to csp with node name %s", nodeName)
		}

		// Pool expansion for raid group types other than striped
		if len(pool.RaidGroups) > len(cspObj.Spec.RaidGroups) {
			cspObj = pc.addGroupToPool(&pool, cspObj)
		}

		// Pool expansion for striped raid group
		pc.expandExistingStripedGroup(&pool, cspObj)

		_, err = apiscsp.NewKubeClient().WithNamespace(pc.AlgorithmConfig.Namespace).Update(cspObj)
		if err != nil {
			glog.Errorf("could not update csp %s: %s", cspObj.Name, err.Error())
		}
	}

	return nil
}

// addGroupToPool adds a raid group to the csp
func (pc *PoolConfig) addGroupToPool(cspcPoolSpec *apis.PoolSpec, csp *apis.NewTestCStorPool) *apis.NewTestCStorPool {
	for _, cspcRaidGroup := range cspcPoolSpec.RaidGroups {
		validGroup := true
		cspcRaidGroup := cspcRaidGroup
		if !isRaidGroupPresentOnCSP(&cspcRaidGroup, csp) {
			if cspcRaidGroup.Type == "" {
				cspcRaidGroup.Type = cspcPoolSpec.PoolConfig.DefaultRaidGroupType
			}
			for _, bd := range cspcRaidGroup.BlockDevices {
				err := pc.isBDUsable(bd.BlockDeviceName)
				if err != nil {
					glog.Errorf("could not use bd %s for expanding pool "+
						"%s:%s", bd.BlockDeviceName, csp.Name, err.Error())
					validGroup = false
					break
				}
			}
			if validGroup {
				csp.Spec.RaidGroups = append(csp.Spec.RaidGroups, cspcRaidGroup)
			}
		}
	}
	return csp
}

// expandExistingStripedGroup adds newly added block devices to the existing striped
// groups present on CSP
func (pc *PoolConfig) expandExistingStripedGroup(cspcPoolSpec *apis.PoolSpec, csp *apis.NewTestCStorPool) {
	for _, cspcGroup := range cspcPoolSpec.RaidGroups {
		cspcGroup := cspcGroup
		if getRaidGroupType(cspcGroup, cspcPoolSpec) != string(apis.PoolStriped) || !isRaidGroupPresentOnCSP(&cspcGroup, csp) {
			continue
		}
		pc.addBlockDeviceToGroup(&cspcGroup, csp)
	}
}

// getRaidGroupType returns the raid type for the provided group
func getRaidGroupType(group apis.RaidGroup, poolSpec *apis.PoolSpec) string {
	if group.Type != "" {
		return group.Type
	}
	return poolSpec.PoolConfig.DefaultRaidGroupType
}

// addBlockDeviceToGroup adds block devices to the provided raid group on CSP
func (pc *PoolConfig) addBlockDeviceToGroup(group *apis.RaidGroup, csp *apis.NewTestCStorPool) *apis.NewTestCStorPool {
	for i, groupOnCSP := range csp.Spec.RaidGroups {
		groupOnCSP := groupOnCSP
		if isRaidGroupPresentOnCSP(group, csp) {
			if len(group.BlockDevices) > len(groupOnCSP.BlockDevices) {
				newBDs := getAddedBlockDevicesInGroup(group, &groupOnCSP)
				if len(newBDs) == 0 {
					glog.V(2).Infof("No new block devices "+
						"added for group {%+v} on csp %s", groupOnCSP, csp.Name)
				}
				for _, bdName := range newBDs {
					err := pc.isBDUsable(bdName)
					if err != nil {
						glog.Errorf("could not use bd %s for "+
							"expanding pool %s:%s", bdName, csp.Name, err.Error())
						break
					}
					csp.Spec.RaidGroups[i].BlockDevices =
						append(csp.Spec.RaidGroups[i].BlockDevices,
							apis.CStorPoolClusterBlockDevice{BlockDeviceName: bdName})
				}
			}
		}
	}
	return csp
}

// isRaidGroupPresentOnCSP returns true if the provided
// raid group is already present on CSP
// TODO: Validation webhook should ensure that in striped group type
// the blockdevices are only added and existing block device are not
// removed.
func isRaidGroupPresentOnCSP(group *apis.RaidGroup, csp *apis.NewTestCStorPool) bool {
	blockDeviceMap := make(map[string]bool)
	for _, bd := range group.BlockDevices {
		blockDeviceMap[bd.BlockDeviceName] = true
	}
	for _, cspRaidGroup := range csp.Spec.RaidGroups {
		for _, cspBDs := range cspRaidGroup.BlockDevices {
			if blockDeviceMap[cspBDs.BlockDeviceName] {
				return true
			}
		}
	}
	return false
}

// getAddedBlockDevicesInGroup returns the added block device list
func getAddedBlockDevicesInGroup(groupOnCSPC, groupOnCSP *apis.RaidGroup) []string {
	var addedBlockDevices []string

	// bdPresentOnCSP is a map whose key is block devices
	// name present on the CSP and corresponding value for
	// the key is true.
	bdPresentOnCSP := make(map[string]bool)
	for _, bdCSP := range groupOnCSP.BlockDevices {
		bdPresentOnCSP[bdCSP.BlockDeviceName] = true
	}

	for _, bdCSPC := range groupOnCSPC.BlockDevices {
		if !bdPresentOnCSP[bdCSPC.BlockDeviceName] {
			addedBlockDevices = append(addedBlockDevices, bdCSPC.BlockDeviceName)
		}
	}
	return addedBlockDevices
}

// getCSPWithNodeName returns a csp object with provided node name
// TODO: Move to CSP package
func (pc *PoolConfig) getCSPWithNodeName(nodeName string) (*apis.NewTestCStorPool, error) {
	cspList, _ := apiscsp.
		NewKubeClient().
		WithNamespace(pc.AlgorithmConfig.Namespace).
		List(metav1.ListOptions{LabelSelector: string(apis.CStorPoolClusterCPK) + "=" + pc.AlgorithmConfig.CSPC.Name})

	cspListBuilder := apiscsp.ListBuilderFromAPIList(cspList).WithFilter(apiscsp.HasNodeName(nodeName)).List()
	if len(cspListBuilder.ObjectList.Items) == 1 {
		return &cspListBuilder.ObjectList.Items[0], nil
	}
	return nil, errors.New("No CSP(s) found")
}

// isBDUsable returns no error if BD can be used.
// If BD has no BDC -- it is created
// TODO: Move to algorithm package
func (pc *PoolConfig) isBDUsable(bdName string) error {
	bdObj, err := apisbd.
		NewKubeClient().
		WithNamespace(pc.AlgorithmConfig.Namespace).
		Get(bdName, metav1.GetOptions{})
	if err != nil {
		return errors.Wrapf(err, "could not get bd object %s", bdName)
	}
	err = pc.AlgorithmConfig.ClaimBD(bdObj)
	if err != nil {
		return errors.Wrapf(err, "failed to claim bd %s", bdName)

	}
	isBDUsable, err := pc.AlgorithmConfig.IsClaimedBDUsable(bdObj)
	if err != nil {
		return errors.Wrapf(err, "bd %s cannot be used as could not get claim status", bdName)
	}

	if !isBDUsable {
		return errors.Errorf("BD %s cannot be used as it is already claimed but not by cspc", bdName)
	}
	return nil
}
