package controller

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/arm/compute"
	"github.com/Azure/azure-sdk-for-go/arm/network"
	"github.com/azure-app-gateway-k8s-ingress/pkg/utils"
	"github.com/golang/glog"
)

func nicName(ipconfigid string) string {
	return strings.Split(ipconfigid, "/")[8]
}

func (lbc *LoadBalancerController) nicUpdate(bepid string, ipcids []string) error {
	client := network.NewInterfacesClientWithBaseURI(lbc.azureAuth.BaseURI, lbc.azureAuth.SubscriptionID)
	client.Authorizer = lbc.azureAuth

	f := func(s string) <-chan error {
		return lbc.nicUpdate1(client, bepid, s)
	}
	return utils.TryAll(f, ipcids)
}

func (lbc *LoadBalancerController) nicUpdate1(client network.InterfacesClient, bepid string, ipcid string) <-chan error {
	cancel := make(chan struct{})

	nicName := nicName(ipcid)
	itf, err := client.Get(lbc.resourceGroup(), nicName, "")
	if err != nil {
		c := make(chan error)
		go func() {
			c <- fmt.Errorf("Get NIC %s error %v", nicName, err)
			close(c)
		}()
		return c
	}
	bep := network.ApplicationGatewayBackendAddressPool{
		ID: &bepid,
	}
	pools := (*itf.IPConfigurations)[0].ApplicationGatewayBackendAddressPools
	if pools == nil {
		pools = &[]network.ApplicationGatewayBackendAddressPool{bep}
	} else if containsBackendPoolID(*pools, bepid) {
		// nothing to do
	} else {
		poolstmp := append(*pools, bep)
		pools = &poolstmp
	}
	// TODO: consider what we need to do when we need to *remove* a node from a BEP
	// really we want all nodes to be members of all BEPs at all times...
	newPrimaryIPCfg := (*itf.IPConfigurations)[0]
	newPrimaryIPCfg.ApplicationGatewayBackendAddressPools = pools
	newIPCfgs := append([]network.InterfaceIPConfiguration{newPrimaryIPCfg}, (*itf.IPConfigurations)[1:]...)
	itf.IPConfigurations = &newIPCfgs
	_, cherr := client.CreateOrUpdate(lbc.resourceGroup(), nicName, itf, cancel)
	glog.V(1).Infof("Kicked off updating NIC %s to associate it to AGW BEP", ipcid)
	return cherr
}

func containsBackendPoolID(pools []network.ApplicationGatewayBackendAddressPool, poolID string) bool {
	for _, p := range pools {
		if strings.EqualFold(*p.ID, poolID) {
			return true
		}
	}
	return false
}

func getPrimaryInterfaceID(machine compute.VirtualMachine) (string, error) {
	if len(*machine.NetworkProfile.NetworkInterfaces) == 1 {
		return *(*machine.NetworkProfile.NetworkInterfaces)[0].ID, nil
	}

	for _, ref := range *machine.NetworkProfile.NetworkInterfaces {
		if *ref.Primary {
			glog.V(1).Infof("Found a primary interface ID: %s", *ref.ID)
			return *ref.ID, nil
		}
	}

	return "", fmt.Errorf("failed to find a primary nic for the vm. vmname=%q", *machine.Name)
}

func getPrimaryIPConfig(nic network.Interface) (*network.InterfaceIPConfiguration, error) {
	if len(*nic.IPConfigurations) == 1 {
		return &((*nic.IPConfigurations)[0]), nil
	}

	for _, ref := range *nic.IPConfigurations {
		if *ref.Primary {
			glog.V(1).Infof("Found a primary IP config ID: %s", *ref.ID)
			return &ref, nil
		}
	}

	return nil, fmt.Errorf("failed to determine the determine primary ipconfig. nicname=%q", *nic.Name)
}

func (lbc *LoadBalancerController) primaryIPConfigID(nodeName string) (string, error) {
	client := compute.NewVirtualMachinesClientWithBaseURI(lbc.azureAuth.BaseURI, lbc.azureAuth.SubscriptionID)
	client.Authorizer = lbc.azureAuth

	var machine compute.VirtualMachine
	vmName := nodeName //mapNodeNameToVMName(nodeName)
	machine, err := client.Get(lbc.resourceGroup(), vmName, "")
	if err != nil {
		glog.V(2).Infof("get VM error %s %v", vmName, err)
		return "", err
	}

	primaryNicID, err := getPrimaryInterfaceID(machine)
	if err != nil {
		return "", err
	}
	nicName := utils.ResourceName(&primaryNicID)

	nclient := network.NewInterfacesClientWithBaseURI(lbc.azureAuth.BaseURI, lbc.azureAuth.SubscriptionID)
	nclient.Authorizer = lbc.azureAuth

	nic, err := nclient.Get(lbc.resourceGroup(), nicName, "")
	if err != nil {
		return "", err
	}

	var primaryIPConfig *network.InterfaceIPConfiguration
	primaryIPConfig, err = getPrimaryIPConfig(nic)
	if err != nil {
		return "", err
	}

	return *primaryIPConfig.ID, nil
}
