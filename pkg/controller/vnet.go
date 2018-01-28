package controller

import (
	"github.com/Azure/azure-sdk-for-go/arm/network"
	"github.com/golang/glog"
)

func (lbc *LoadBalancerController) putSubnet(vnetName string, subnet network.Subnet) (*network.Subnet, error) {
	client := network.NewSubnetsClientWithBaseURI(lbc.azureAuth.BaseURI, lbc.azureAuth.SubscriptionID)
	client.Authorizer = lbc.azureAuth

	cancel := make(chan struct{})
	rsrcch, errch := client.CreateOrUpdate(lbc.resourceGroup(), vnetName, *subnet.Name, subnet, cancel)
	err := <-errch
	if err != nil {
		return nil, err
	}

	rsrc := <-rsrcch

	glog.V(1).Infof("created or updated %s", safe(rsrc.Name))

	return &rsrc, nil
}
