package controller

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-app-gateway-k8s-ingress/pkg/context"
	"github.com/Azure/azure-app-gateway-k8s-ingress/pkg/utils"
	"github.com/Azure/azure-sdk-for-go/arm/network"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
)

const (
	// ingressClassKey picks a specific "class" for the Ingress. The controller
	// only processes Ingresses with this annotation either unset, or set
	// to either gceIngessClass or the empty string.
	ingressClassKey        = "kubernetes.io/ingress.class"
	appGatewayIngressClass = "azure-application-gateway"
)

type azureContext struct {
	subscriptionID string
	resourceGroup  string
	vnetName       string
	location       string
}

func isAppGatewayIngress(ingress v1beta1.Ingress) bool {
	// defaulting to app gateway is modelled on GCE ingress controller;
	// assuming it makes sense because you wouldn't install multiple
	// ingress controllers?
	if l, ok := ingress.Annotations[ingressClassKey]; ok {
		return l == "" || l == appGatewayIngressClass
	}
	return true
}

func getGatewaySpec(ingress v1beta1.Ingress, subscriptionID string, azureConfig context.AzureConfig, location string, serviceResolver func(string) (*v1.Service, error), backendIPCIDs []string) (network.ApplicationGateway, network.PublicIPAddress, network.Subnet, error) {
	ac := azureContext{
		subscriptionID: subscriptionID,
		resourceGroup:  azureConfig.ResourceGroup,
		vnetName:       azureConfig.VnetName,
		location:       location,
	}
	return ac.ingressToGateway(ingress, serviceResolver, backendIPCIDs)
}

func getGatewayName(ingress v1beta1.Ingress) (string, string) {
	gatewayName := "k8s-aaging-" + ingress.Name
	publicIPName := gatewayName + "-public-ip"
	return gatewayName, publicIPName
}

type listenerPort struct {
	port     int32
	hostName string
}

func listenerSuffix(listener listenerPort) string {
	if listener.hostName != "" {
		return fmt.Sprintf("%d-%s", listener.port, strings.Replace(listener.hostName, ".", "-", -1)) // TODO: this probably isn't good enough long term, but will do for POC
	}
	return fmt.Sprintf("%d", listener.port)
}

func (ac azureContext) ingressToGateway(ingress v1beta1.Ingress, serviceResolver func(string) (*v1.Service, error), backendIPCIDs []string) (network.ApplicationGateway, network.PublicIPAddress, network.Subnet, error) {

	// SIMPLEST CASE: SINGLE SERVICE INGRESS
	/*
		apiVersion: extensions/v1beta1
		kind: Ingress
		metadata:
			name: test-ingress
		spec:
			backend:
				serviceName: testsvc
				servicePort: 80
	*/
	// NEEDS TO RESULT IN
	// - an AAG L7 LB
	// - configured with a suitable front end address
	// - configured with a suitable backend pool
	// - with a routing rule from the front end to the back end
	// NEEDS TO MAP TO WHAT
	/*
		gatewayIPCfgs: { subnet: cluster_subnet_ID }
		fips: { publicIPID: alloced, subnet: cluster_subnet_id }
		frontendPorts: { port: spec.backend.servicePort }
		probes: { TBD }
		backendAddressPools: { create one and then assign nodes to it }
		backendHTTP: { port: spec.backend.servicePort?, anything else? }
		httpListeners: { feipcfg: ref, feport: ref, hostName: ???what???, sslCert: can skip for port 80 }
		reqroutingrules: { any needed or will defaults in URLpathmaps be enough for simple case? }
	*/
	// I don't quite get the gatewayIPConfigs vs frontendIPConfigs

	// THE NEXT CASE: RULES

	// EXAMPLE 1: SIMPLE FANOUT
	/*
		apiVersion: extensions/v1beta1
		kind: Ingress
		metadata:
			name: test-fanout
			annotations:
				ingress.kubernetes.io/rewrite-target: /
		spec:
			rules:
			-	host: foo.bar.example.com
				http:
					paths:
					-	path: /quux
						backend:
							serviceName: s1
							servicePort: 80
					-	path: /baz
						backend:
							serviceName: s2
							servicePort: 80
	*/
	// TODO: what annotations are supported and how do we need to translate them?
	// NEEDS TO MAP TO
	/*
		gatewayIPCfgs: { subnet: cluster_subnet_ID }
		fips: { publicIPID: alloced, subnet: cluster_subnet_id }
		frontendPorts: { port: 80? }
		probes: { TBD }
		backendAddressPools: { how do we get one of these?  Need the nodes collection I guess }
		backendHTTP: { port: spec.backend.servicePort } x N
		httpListeners: { feipcfg: ref, feport: ref, hostName: host, sslCert: can skip for port 80 }
		URLpathmaps: { defbep: ref, defbhttps: ref, defredircfg: ref, pathrules: path } x N
		reqroutingrules: { urlpathmap: ref, ...? }
	*/

	// EXAMPLE 1a: SIMPLE FANOUT WITH 404
	// Spec.Backend is used if none of the Hosts in the ingress match
	// the Host in the request header, and/or none of the paths match
	// the URL of the request
	/*
		apiVersion: extensions/v1beta1
		kind: Ingress
		metadata:
			name: test-fanout
			annotations:
				ingress.kubernetes.io/rewrite-target: /
		spec:
			rules:
			-	host: foo.bar.example.com
				http:
					paths:
					-	path: /quux
						backend:
							serviceName: s1
							servicePort: 80
					-	path: /baz
						backend:
							serviceName: s2
							servicePort: 80
			backend:
				serviceName: testsvc
				servicePort: 80
	*/

	// EXAMPLE 2: NAME BASED VIRTUAL HOSTING
	/*
		apiVersion: extensions/v1beta1
		kind: Ingress
		metadata:
			name: test-vhost
		spec:
			rules:
			-	host: foo.bar.example.com
				http:
					paths:
						backend:
							serviceName: s1
							servicePort: 80
			-	host: baz.quux.example.com
				http:
					paths:
						backend:
							serviceName: s2
							servicePort: 80
	*/

	/*
		The minimum we need to specify:
		- location
		- properties
		  - sku (capacity, name, tier)
		  - fip config (name, ip, subnet)
		  - frontend port (name, port)
		  - gateway ip cfg (name, subnet)
		  - backend addr pool (name, addresses)
		  - backend http settings (name, protocol, port)
		  - http listener (name, fipcfgref, fepref, protocol)
		  - req routing rule (name, ruletype, bepref, httpsettingsref, httplistenerref)
	*/

	// things we do not tackle right now
	if len(ingress.Spec.TLS) > 0 {
		glog.V(1).Infof("TLS not yet implemented for Azure Application Gateway ingress")
		return network.ApplicationGateway{}, network.PublicIPAddress{}, network.Subnet{}, fmt.Errorf("TLS not yet implemented")
	}

	backend := ingress.Spec.Backend
	rules := ingress.Spec.Rules

	gatewayName, publicIPName := getGatewayName(ingress)

	gatewayIPConfigurationName := "k8sgatewayipcfg"

	backendPoolName := "k8sbackendpool"
	backendPoolID := ac.addressPoolID(gatewayName, backendPoolName)

	frontendIPConfigurationName := "k8spublicipcfg"
	frontendIPConfigurationID := ac.fipID(gatewayName, frontendIPConfigurationName)

	urlPathMapName := "k8surlpathmap"
	urlPathMapID := ac.urlPathMapID(gatewayName, urlPathMapName)

	gatewayVnetName := ac.vnetName
	gatewaySubnetName := "agw-subnet"
	gatewaySubnetID := ac.subnetID(gatewayVnetName, gatewaySubnetName)
	gatewaySubnet := network.Subnet{
		Name: &gatewaySubnetName,
		SubnetPropertiesFormat: &network.SubnetPropertiesFormat{
			AddressPrefix: to.StringPtr("10.1.0.0/24"), // TODO: how to override this?  Annotation?
			// hoping we don't need anything else
		},
	}

	publicIPID := ac.publicIPID(publicIPName)

	publicIP := network.PublicIPAddress{
		Name:     &publicIPName,
		Location: &ac.location,
		PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: network.Dynamic, // has to be dynamic or AG won't link to it
			PublicIPAddressVersion:   network.IPv4,
		},
	}

	defHTTPSettingsName := "k8s-defaultbackend-settings"
	defHTTPSettingsID := ac.httpSettingsID(gatewayName, defHTTPSettingsName)

	frontendPorts := []network.ApplicationGatewayFrontendPort{}
	backendHTTPSettingsCollection := []network.ApplicationGatewayBackendHTTPSettings{}
	httpListeners := []network.ApplicationGatewayHTTPListener{}
	requestRoutingRules := []network.ApplicationGatewayRequestRoutingRule{}
	servicePorts := make(map[listenerPort]listenerPort)

	urlPathMap := network.ApplicationGatewayURLPathMap{
		Name: &urlPathMapName,
		ApplicationGatewayURLPathMapPropertiesFormat: &network.ApplicationGatewayURLPathMapPropertiesFormat{
			DefaultBackendAddressPool:  resourceRef(backendPoolID),
			DefaultBackendHTTPSettings: resourceRef(defHTTPSettingsID),
		},
	}
	pathRules := []network.ApplicationGatewayPathRule{}

	if backend != nil {
		servicePort, nodePort, protocol, err := serviceInfo(*backend, serviceResolver)

		if err != nil {
			return network.ApplicationGateway{}, network.PublicIPAddress{}, network.Subnet{}, fmt.Errorf("Failed to resolve service %s: %v", backend.ServiceName, err)
		}

		listener := listenerPort{port: servicePort, hostName: ""}
		servicePorts[listener] = listener

		backendHTTPSettingsCollection = append(backendHTTPSettingsCollection, network.ApplicationGatewayBackendHTTPSettings{
			Name: &defHTTPSettingsName,
			ApplicationGatewayBackendHTTPSettingsPropertiesFormat: &network.ApplicationGatewayBackendHTTPSettingsPropertiesFormat{
				Protocol: protocol,
				Port:     &nodePort,
			},
		})
	} else {
		backendHTTPSettingsCollection = append(backendHTTPSettingsCollection, network.ApplicationGatewayBackendHTTPSettings{
			Name: &defHTTPSettingsName,
			ApplicationGatewayBackendHTTPSettingsPropertiesFormat: &network.ApplicationGatewayBackendHTTPSettingsPropertiesFormat{
				// TODO: ugh
				Protocol: network.HTTP,
				Port:     to.Int32Ptr(80),
			},
		})
	}

	index := 0
	backendHTTPSettingsForEntryPoint := make(map[listenerPort]string)
	for _, rule := range rules {
		host := rule.Host
		http := rule.HTTP
		for _, pathSpec := range http.Paths {
			urlPath := pathSpec.Path
			backend := pathSpec.Backend

			servicePort, nodePort, protocol, err := serviceInfo(backend, serviceResolver)

			if err != nil {
				return network.ApplicationGateway{}, network.PublicIPAddress{}, network.Subnet{}, fmt.Errorf("Failed to resolve service %s: %v", backend.ServiceName, err)
			}

			listener := listenerPort{port: servicePort, hostName: host}
			servicePorts[listener] = listener

			if err != nil {
				return network.ApplicationGateway{}, network.PublicIPAddress{}, network.Subnet{}, fmt.Errorf("Failed to resolve service %s: %v", backend.ServiceName, err)
			}

			httpSettingsName := fmt.Sprintf("k8s-backend%d-settings", index)
			httpSettingsID := ac.httpSettingsID(gatewayName, httpSettingsName)

			backendHTTPSettingsCollection = append(backendHTTPSettingsCollection, network.ApplicationGatewayBackendHTTPSettings{
				Name: &httpSettingsName,
				ApplicationGatewayBackendHTTPSettingsPropertiesFormat: &network.ApplicationGatewayBackendHTTPSettingsPropertiesFormat{
					Protocol: protocol,
					Port:     &nodePort,
				},
			})

			// TODO: can you have a blank path and specific paths within the same host?
			if urlPath != "" {
				pathRuleName := fmt.Sprintf("k8s-backend%d-pathrule", index)

				pathRules = append(pathRules, network.ApplicationGatewayPathRule{
					Name: &pathRuleName,
					ApplicationGatewayPathRulePropertiesFormat: &network.ApplicationGatewayPathRulePropertiesFormat{
						Paths:               &[]string{urlPath},
						BackendAddressPool:  resourceRef(backendPoolID),
						BackendHTTPSettings: resourceRef(httpSettingsID),
					},
				})
			} else {
				backendHTTPSettingsForEntryPoint[listener] = httpSettingsID
			}

			index = index + 1
		}
	}

	urlPathMap.PathRules = &pathRules

	for servicePort := range servicePorts {
		frontendPortName := fmt.Sprintf("k8s-fep-%d", servicePort.port)
		frontendPortID := ac.fepID(gatewayName, frontendPortName)

		httpListenerName := fmt.Sprintf("k8s-listener-%s", listenerSuffix(servicePort))
		httpListenerID := ac.httpListenerID(gatewayName, httpListenerName)

		hostName := to.StringPtr(servicePort.hostName)  // don't use &servicePort.hostName because range variable reuses address
		if servicePort.hostName == "" {
			hostName = nil
		}

		requestRoutingRuleName := fmt.Sprintf("k8s-routingrule-%s", listenerSuffix(servicePort))

		protocol := protocol(servicePort.port)

		frontendPorts = appendIfNeeded(frontendPorts, network.ApplicationGatewayFrontendPort{
			Name: &frontendPortName,
			ApplicationGatewayFrontendPortPropertiesFormat: &network.ApplicationGatewayFrontendPortPropertiesFormat{
				Port: to.Int32Ptr(servicePort.port), // presumably
			},
		})

		httpListeners = append(httpListeners, network.ApplicationGatewayHTTPListener{
			Name: &httpListenerName,
			ApplicationGatewayHTTPListenerPropertiesFormat: &network.ApplicationGatewayHTTPListenerPropertiesFormat{
				FrontendIPConfiguration: resourceRef(frontendIPConfigurationID),
				FrontendPort:            resourceRef(frontendPortID),
				Protocol:                protocol,
				HostName:                hostName,
			},
		})

		routingURLPathMapRef := resourceRef(urlPathMapID)
		routingRuleType := network.PathBasedRouting
		if len(pathRules) == 0 {
			routingURLPathMapRef = nil
			routingRuleType = network.Basic
		}

		routingRuleBackendHTTPSettingsID := defHTTPSettingsID
		if s, ok := backendHTTPSettingsForEntryPoint[servicePort]; ok {
			routingRuleBackendHTTPSettingsID = s
		}

		requestRoutingRules = append(requestRoutingRules, network.ApplicationGatewayRequestRoutingRule{
			Name: &requestRoutingRuleName,
			ApplicationGatewayRequestRoutingRulePropertiesFormat: &network.ApplicationGatewayRequestRoutingRulePropertiesFormat{
				RuleType:            routingRuleType,
				BackendAddressPool:  resourceRef(backendPoolID),
				BackendHTTPSettings: resourceRef(routingRuleBackendHTTPSettingsID),
				HTTPListener:        resourceRef(httpListenerID),
				URLPathMap:          routingURLPathMapRef,
				//RedirectConfiguration: &id,
			},
		})
	}

	gatewayURLPathMaps := &[]network.ApplicationGatewayURLPathMap{urlPathMap}
	if len(pathRules) == 0 {
		gatewayURLPathMaps = nil
	}

	gw := network.ApplicationGateway{
		Name:     &gatewayName,
		Location: &ac.location,
		ApplicationGatewayPropertiesFormat: &network.ApplicationGatewayPropertiesFormat{
			Sku: &network.ApplicationGatewaySku{
				Capacity: to.Int32Ptr(1),
				Name:     network.StandardMedium,
				Tier:     network.Standard,
			},
			FrontendIPConfigurations: &[]network.ApplicationGatewayFrontendIPConfiguration{
				network.ApplicationGatewayFrontendIPConfiguration{
					Name: &frontendIPConfigurationName,
					ApplicationGatewayFrontendIPConfigurationPropertiesFormat: &network.ApplicationGatewayFrontendIPConfigurationPropertiesFormat{
						PublicIPAddress: resourceRef(publicIPID),
					},
				},
			},
			GatewayIPConfigurations: &[]network.ApplicationGatewayIPConfiguration{
				network.ApplicationGatewayIPConfiguration{
					Name: &gatewayIPConfigurationName,
					ApplicationGatewayIPConfigurationPropertiesFormat: &network.ApplicationGatewayIPConfigurationPropertiesFormat{
						Subnet: resourceRef(gatewaySubnetID),
					},
				},
			},
			FrontendPorts: &frontendPorts,
			BackendAddressPools: &[]network.ApplicationGatewayBackendAddressPool{
				network.ApplicationGatewayBackendAddressPool{
					Name: &backendPoolName,
					ApplicationGatewayBackendAddressPoolPropertiesFormat: &network.ApplicationGatewayBackendAddressPoolPropertiesFormat{},
				},
			},
			BackendHTTPSettingsCollection: &backendHTTPSettingsCollection,
			HTTPListeners:                 &httpListeners,
			URLPathMaps:                   gatewayURLPathMaps,
			RequestRoutingRules:           &requestRoutingRules,
		},
	}

	return gw, publicIP, gatewaySubnet, nil
}

func serviceInfo(backend v1beta1.IngressBackend, serviceResolver func(string) (*v1.Service, error)) (int32, int32, network.ApplicationGatewayProtocol, error) {
	service, err := serviceResolver(backend.ServiceName)
	if err != nil {
		glog.V(1).Infof("Failed to resolve service %s: %v", backend.ServiceName, err)
		return 0, 0, network.HTTP, err
	}
	_, nodePort, err := utils.GetNodePort(service)
	if err != nil {
		glog.V(1).Infof("Failed to get node port for service %s: %v", service.Name, err)
		return 0, 0, network.HTTP, err
	}
	servicePort := int32(backend.ServicePort.IntValue())
	glog.V(1).Infof("Found service %s and it is on %s and port %d", service.Name, service.Spec.ClusterIP, nodePort)

	protocol := protocol(servicePort)

	return servicePort, nodePort, protocol, nil
}

func protocol(servicePort int32) network.ApplicationGatewayProtocol {
	// TODO: how?
	if servicePort == 443 {
		return network.HTTPS
	}
	return network.HTTP
}

func nicIPConfigs(ipcids []string) []network.InterfaceIPConfiguration {
	ipconfigs := []network.InterfaceIPConfiguration{}
	for _, ipcid := range ipcids {
		ipconfigid := ipcid
		ipconfig := network.InterfaceIPConfiguration{
			ID: &ipconfigid,
		}
		glog.V(1).Infof("Created InterfaceIPConfiguration with ID %s", *ipconfig.ID)
		ipconfigs = append(ipconfigs, ipconfig)
	}
	return ipconfigs
}

func (ac azureContext) resourceID(provider string, resourceKind string, resourcePath string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/%s/%s/%s", ac.subscriptionID, ac.resourceGroup, provider, resourceKind, resourcePath)
}

func (ac azureContext) gatewayResourceID(gatewayName string, subResourceKind string, resourceName string) string {
	resourcePath := fmt.Sprintf("%s/%s/%s", gatewayName, subResourceKind, resourceName)
	return ac.resourceID("Microsoft.Network", "applicationGateways", resourcePath)
}

func (ac azureContext) addressPoolID(gatewayName string, poolName string) string {
	return ac.gatewayResourceID(gatewayName, "backendAddressPools", poolName)
}

func (ac azureContext) fipID(gatewayName string, fipName string) string {
	return ac.gatewayResourceID(gatewayName, "frontEndIPConfigurations", fipName)
}

func (ac azureContext) fepID(gatewayName string, portName string) string {
	return ac.gatewayResourceID(gatewayName, "frontEndPorts", portName)
}

func (ac azureContext) httpSettingsID(gatewayName string, settingsName string) string {
	return ac.gatewayResourceID(gatewayName, "backendHttpSettingsCollection", settingsName)
}

func (ac azureContext) urlPathMapID(gatewayName string, urlPathMapName string) string {
	return ac.gatewayResourceID(gatewayName, "urlPathMaps", urlPathMapName)
}

func (ac azureContext) httpListenerID(gatewayName string, listenerName string) string {
	return ac.gatewayResourceID(gatewayName, "httpListeners", listenerName)
}

func (ac azureContext) subnetID(vnetName string, subnetName string) string {
	resourcePath := fmt.Sprintf("%s/subnets/%s", vnetName, subnetName)
	return ac.resourceID("Microsoft.Network", "virtualNetworks", resourcePath)
}

func (ac azureContext) publicIPID(publicIPName string) string {
	return ac.resourceID("Microsoft.Network", "publicIPAddresses", publicIPName)
}

func resourceRef(id string) *network.SubResource {
	return &network.SubResource{ID: to.StringPtr(id)}
}

func appendIfNeeded(existing []network.ApplicationGatewayFrontendPort, new network.ApplicationGatewayFrontendPort) []network.ApplicationGatewayFrontendPort {
	for _, p := range existing {
		if strings.EqualFold(*p.Name, *new.Name) {
			return existing
		}
	}
	return append(existing, new)
}
