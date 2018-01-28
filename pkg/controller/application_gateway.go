package controller

import (
	"github.com/Azure/azure-sdk-for-go/arm/network"
)

func (lbc *LoadBalancerController) putGateway(gw network.ApplicationGateway) (*network.ApplicationGateway, error) {
	client := network.NewApplicationGatewaysClientWithBaseURI(lbc.azureAuth.BaseURI, lbc.azureAuth.SubscriptionID)
	client.Authorizer = lbc.azureAuth

	cancel := make(chan struct{})
	gwch, errch := client.CreateOrUpdate(lbc.resourceGroup(), *gw.Name, gw, cancel)
	err := <-errch
	if err != nil {
		return nil, err
	}
	gw = <-gwch
	return &gw, nil
}

func (lbc *LoadBalancerController) deleteGateway(name string) error {
	client := network.NewApplicationGatewaysClientWithBaseURI(lbc.azureAuth.BaseURI, lbc.azureAuth.SubscriptionID)
	client.Authorizer = lbc.azureAuth

	cancel := make(chan struct{})
	_, errch := client.Delete(lbc.resourceGroup(), name, cancel)
	err := <-errch
	if err != nil {
		return err
	}
	return nil
}
