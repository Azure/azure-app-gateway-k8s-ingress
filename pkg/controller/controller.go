package controller

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"github.com/Azure/azure-app-gateway-k8s-ingress/pkg/context"
	"github.com/Azure/azure-sdk-for-go/arm/resources/resources"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/golang/glog"

	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// LoadBalancerController watches the Kubernetes API, and adds/removes
// resource to/from an Azure Application Gateway load balancer
type LoadBalancerController struct {
	client      kubernetes.Interface
	azureAuth   auth.ClientSetup
	azureConfig context.AzureConfig

	ingressSynced cache.InformerSynced
	serviceSynced cache.InformerSynced
	podSynced     cache.InformerSynced
	nodeSynced    cache.InformerSynced

	stopChannel chan struct{}
	stopLock    sync.Mutex // used to ensure only one call to Stop() is in flight at a time
}

// NewLoadBalancerController creates a new LoadBalancerController
func NewLoadBalancerController(kubeclient kubernetes.Interface, azureAuth auth.ClientSetup, azureConfig context.AzureConfig) *LoadBalancerController {
	return &LoadBalancerController{
		client:      kubeclient,
		azureAuth:   azureAuth,
		azureConfig: azureConfig,
	}
}

func (lbc *LoadBalancerController) resourceGroup() string {
	return lbc.azureConfig.ResourceGroup
}

func (lbc *LoadBalancerController) agentIPConfigIDs() ([]string, error) {
	nodes, err := lbc.client.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("Error getting Kubernetes nodes: %v", err)
	}

	ipcids := []string{}
	for _, n := range nodes.Items {
		if l, ok := n.Labels["kubernetes.io/role"]; ok && l == "agent" {
			ipcid, err := lbc.primaryIPConfigID(n.Name)
			if err != nil {
				return nil, fmt.Errorf("failed to get ip for %s: %v", n.Name, err)
			}
			ipcids = append(ipcids, ipcid)
		}
	}

	return ipcids, nil
}

func (lbc *LoadBalancerController) defaultLocation() (string, error) {
	client := resources.NewGroupsClientWithBaseURI(lbc.azureAuth.BaseURI, lbc.azureAuth.SubscriptionID)
	client.Authorizer = lbc.azureAuth

	g, err := client.Get(lbc.resourceGroup())
	if err != nil {
		return "", err
	}

	return *g.Location, nil
}

func (lbc *LoadBalancerController) reportIngressErrorEvent(i v1beta1.Ingress, code string, message string) error {
	errorEvent := v1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "azl7icing",
		},
		InvolvedObject: v1.ObjectReference{
			Kind:      "Ingress",
			Namespace: i.Namespace,
			Name:      i.Name,
			UID:       i.UID,
		},
		Reason:  code,
		Message: message,
		Source: v1.EventSource{
			Component: "azl7ic",
		},
		Type:           "Warning",
		Count:          1,
		FirstTimestamp: metav1.Now(),
		LastTimestamp:  metav1.Now(),
	}
	_, err := lbc.client.CoreV1().Events("default").Create(&errorEvent)
	return err
}

func (lbc *LoadBalancerController) setIngressGateway(i *v1beta1.Ingress) {
	currIng, err := lbc.client.Extensions().Ingresses(i.Namespace).Get(i.Name, metav1.GetOptions{})
	if err != nil {
		glog.V(1).Infof("Error getting ingress object: %v", err)
		return
	}

	serviceResolver := func(serviceName string) (*v1.Service, error) {
		return lbc.client.CoreV1().Services("default").Get(serviceName, metav1.GetOptions{})
	}

	ipcids, err := lbc.agentIPConfigIDs()
	if err != nil {
		glog.V(1).Infof("Error getting agent IP config IDs: %v", err)
		return
	}

	location, err := lbc.defaultLocation()
	if err != nil {
		glog.V(1).Infof("Error selecting location: %v", err)
		return
	}

	// TODO: consider using a resources.Deployment
	desiredGatewayState, desiredPublicIPState, desiredSubnet, err := getGatewayResourceSpecs(*currIng, lbc.azureAuth.SubscriptionID, lbc.azureConfig, location, serviceResolver, ipcids)
	if err != nil {
		glog.V(1).Infof("Error deriving desired gateway resource states: %v", err)
		err = lbc.reportIngressErrorEvent(*currIng, "ErrorDerivingSpec", fmt.Sprintf("Error deriving desired gateway spec: %v", err))
		if err != nil {
			glog.V(1).Infof("Error recording ErrorDerivingSpec event: %v", err)
		}
		return
	}

	buf, err := json.Marshal(desiredGatewayState)
	glog.V(1).Infof("Desired LB state: %s", string(buf))

	// (note: currently subnets get deleted during acs-engine scale up but a fix is in progress)
	_, err = lbc.putSubnet(lbc.azureConfig.VnetName, desiredSubnet)
	if err != nil {
		glog.V(1).Infof("Error committing app gateway subnet %v", err)
		return
	}

	publicIP, err := lbc.putPublicIP(desiredPublicIPState)
	if err != nil {
		glog.V(1).Infof("Error committing PIP resource %v", err)
		return
	}

	gateway, err := lbc.putGateway(desiredGatewayState)
	if err != nil {
		glog.V(1).Infof("Error committing gateway resource: %v", err)
		return
	}

	err = lbc.nicUpdate(*(*gateway.BackendAddressPools)[0].ID, ipcids)
	if err != nil {
		glog.V(1).Infof("Error setting backend pool on NICs: %v", err)
		return
	}

	ip, err := lbc.getPublicIP(*publicIP)
	if err != nil {
		glog.V(1).Infof("Error getting ingress public IP: %v", err)
		return
	}

	currIng.Status = v1beta1.IngressStatus{
		LoadBalancer: v1.LoadBalancerStatus{
			Ingress: []v1.LoadBalancerIngress{
				v1.LoadBalancerIngress{
					IP: *ip.IPAddress,
				},
			},
		},
	}

	_, err = lbc.client.Extensions().Ingresses(currIng.Namespace).UpdateStatus(currIng)
	if err != nil {
		glog.Infof("Saving ingress %s: error updating IP: %v", currIng, err)
		return
	}

	glog.V(1).Infof("Success: created PIP and LB %s", currIng.Name)
}

// OnNewIngress runs when a new ingress is detected
// and creates a corresponding load balancer
func (lbc *LoadBalancerController) OnNewIngress(i *v1beta1.Ingress) {
	if (!isAppGatewayIngress(i)) {
		glog.V(1).Infof("Ignoring new ingress %s - not an Azure Application Gateway ingress", i.Name)
	}
	glog.V(1).Infof("Processing new ingress %s", i.Name)
	lbc.setIngressGateway(i)
}

// OnEditIngress runs when an ingress changes and
// updates the corresponding load balancer
func (lbc *LoadBalancerController) OnEditIngress(old, new *v1beta1.Ingress) {
	// I think this pretty much normalises the AGW to the new state.  So the same
	// spec-then-PUT should work I think

	isOldAppGateway := isAppGatewayIngress(old)
	isNewAppGateway := isAppGatewayIngress(new)
	if (isOldAppGateway && !isNewAppGateway) {
		glog.V(1).Infof("Ingress %s changed from Azure Application Gateway to other - deleting gateway", old.Name)
		lbc.deleteIngress(old)
		return
	} else if (!isOldAppGateway && !isNewAppGateway) {
		glog.V(1).Infof("Ignoring updated ingress %s - not an Azure Application Gateway ingress", old.Name)
		return
	}

	// If they differ only by status then there is no work to do?
	// (We still need to check ObjectMeta because of annotations.)
	unchanged := reflect.DeepEqual(old.ObjectMeta, new.ObjectMeta) &&
		reflect.DeepEqual(old.Spec, new.Spec)

	if unchanged {
		glog.V(1).Infof("Salient details of %s have not changed - taking no action", old.Name)
		return
	}

	// If we get here, it's either:
	// * a gateway ingress being modified (most likely) - we need to update the existing gateway; or
	// * a non-gateway ingress (e.g. nginx) being changed to use a gateway - we trust that the
	//   ingress controller handling the 'from' class will delete anything it needs to, and just
	//   create a new gateway
	// It turns out these are the same code path at our end, so no need to distinguish these cases!

	glog.V(1).Infof("Updating ingress %s", old.Name)
	// TODO: if the name changes we need to tear down the old gateway
	// TODO: is this allowed in k8s and if so how do we transition in Azure?
	lbc.setIngressGateway(new)
}

// OnDeleteIngress runs when an ingress is deleted
// and removes the load balancer
func (lbc *LoadBalancerController) OnDeleteIngress(i *v1beta1.Ingress) {
	glog.V(1).Infof("Processing ingress deletion %s", i.Name)
	lbc.deleteIngress(i)
	glog.V(1).Infof("Processed ingress deletion %s", i.Name)
}

func (lbc *LoadBalancerController) deleteIngress(i *v1beta1.Ingress) {
	lbName, pipName := getGatewayResourceNames(*i)
	err := lbc.deleteGateway(lbName)
	if err != nil {
		glog.V(1).Infof("Failed to delete LB %s for %s", lbName, i.Name)
		// TODO: put it back on the queue
	}
	err = lbc.deletePublicIP(pipName)
	if err != nil {
		glog.V(1).Infof("Failed to delete PIP %s for %s", pipName, i.Name)
		// TODO: put it back on the queue
	}
}

// OnNewNode - TODO: implementation and documentation
func (lbc *LoadBalancerController) OnNewNode(n *v1.Node) {
	glog.V(1).Infof("Processing new node %s", n.Name)
	// TODO: add all AGW BEPs to node IPC
	// TODO: okay it seems like we get new node events during agent pool
	// scaling as this appears to delete the ingress controller pod* and create a
	// new one - this is probably okay because we will sanity check if
	// the AGW BEP is already present before adding it
	//
	// *But other pods e.g. nginx don't seem to get this so I think
	// this could be a coincidence that azl7ic has tended to land
	// on the node that gets scaled away?
	//
	// And then there are the times we don't seem to get new node notifications
	// **at all**...
}

// OnDeleteNode - TODO: implementation and documentation
func (lbc *LoadBalancerController) OnDeleteNode(n *v1.Node) {
	glog.V(1).Infof("Processing deleted node %s", n.Name)
	// TODO: remove node from all AGW BEPs -- needed?  (Since the
	// BEP membership seems to be a node attribute not an AGW attribute.)
}
