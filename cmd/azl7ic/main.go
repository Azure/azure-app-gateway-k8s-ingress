package main

import (
	"errors"
	go_flag "flag"
	"io/ioutil"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"github.com/Azure/azure-sdk-for-go/arm/network"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/Azure/azure-app-gateway-k8s-ingress/pkg/controller"
	"github.com/Azure/azure-app-gateway-k8s-ingress/pkg/context"

	"github.com/golang/glog"
	flag "github.com/spf13/pflag"
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

var (
	flags = flag.NewFlagSet(
		`azl7ic`,
		flag.ExitOnError)

	inCluster = flags.Bool("running-in-cluster", true,
		"If running in a Kubernetes cluster, use the pod secrets for creating a Kubernetes client. Optional.")

	apiServerHost = flags.String("apiserver-host", "",
		"The address of the Kubernetes apiserver. Optional if running in cluster; if omitted, local discovery is attempted.")

	kubeConfigFile = flags.String("kubeconfig", "",
		"Path to kubeconfig file with authorization and master location information.")

	resyncPeriod = flags.Duration("sync-period", 2*time.Minute,
		"Interval at which to re-list and confirm cloud resources.")

	// defaultBackendService = flags.String("default-backend-service", "kube-system/default-http-backend",
	// 	"Backend service when path is not routed, in the form namespace/name. Should serve a 404 page.")
)

var (
	errInvalidNamespacedName = errors.New("Invalid namespaced name")
)

func main() {
	flags.Parse(os.Args)

	setLoggingOptions()

	kubeClient := kubeClient()
	azureAuth := azAuthFromConfigMap(kubeClient, "azure-config")
	azureConfig := azureConfigFromConfigMap(kubeClient, "azure-config")

	cc := context.NewControllerContext(kubeClient, "default", *resyncPeriod, false)
	lbc := controller.NewLoadBalancerController(kubeClient, azureAuth, azureConfig)

	// Note that at startup we will get an Add for every existing ingress,
	// even ones which do already have a corresponding LB
	//
	// I think the plan will be one gateway per ingress - the ingress can
	// map multiple hosts but it looks like they are all on the same IP,
	// which corresponds nicely to a gateway allowing only one public FE IP cfg.

	// These actually need to go via a queue I think, so we can handle retries
	ingressHandler := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			i := obj.(*v1beta1.Ingress)
			glog.V(1).Infof("New ingress %s", i.Name)
			lbc.OnNewIngress(i)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			// Hmm, this seems to get called on every watch sync (every 2 min).  Need
			// to work out how to detect no-ops
			old := oldObj.(*v1beta1.Ingress)
			new := newObj.(*v1beta1.Ingress)
			if reflect.DeepEqual(old, new) {
				return
			}
			glog.V(1).Infof("Updating ingress %s->%s", old.Name, new.Name)
			lbc.OnEditIngress(old, new)
		},
		DeleteFunc: func(obj interface{}) {
			i := obj.(*v1beta1.Ingress)
			glog.V(1).Infof("Deleting ingress %s", i.Name)
			lbc.OnDeleteIngress(i)
		},
	}

	nodeHandler := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			n := obj.(*v1.Node)
			glog.V(1).Infof("Handling new node %s", n.Name)
			lbc.OnNewNode(n)
		},
		// don't need to handle updates
		DeleteFunc: func(obj interface{}) {
			n := obj.(*v1.Node)
			glog.V(1).Infof("Handling deletion of node %s", n.Name)
			lbc.OnDeleteNode(n)
		},
	}

	cc.NodeInformer.AddEventHandler(nodeHandler)
	cc.IngressInformer.AddEventHandler(ingressHandler)

	go handleSigterm(cc)

	cc.Start()

	for true {
		time.Sleep(1 * time.Minute)
	}
}

func handleSigterm(cc *context.ControllerContext) {
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, syscall.SIGTERM)
	<-signalChannel
	glog.V(1).Info("Received SIGTERM - shutting down")

	cc.Stop()

	os.Exit(0)
}

func setLoggingOptions() {
	go_flag.Lookup("logtostderr").Value.Set("true")
	go_flag.Set("v", "3") // TODO: for now
}

func kubeClient() kubernetes.Interface {
	config := getKubeClientConfig()

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("failed to create client: %v", err)
	}

	return kubeClient
}

func getKubeClientConfig() *rest.Config {
	if *inCluster {
		config, err := rest.InClusterConfig()
		if err != nil {
			glog.Fatalf("error creating client configuration: %v", err)
		}
		return config
	}

	if *apiServerHost == "" {
		glog.Fatalf("when not running in a cluster you must specify --apiserver-host")
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: *kubeConfigFile},
		&clientcmd.ConfigOverrides{
			ClusterInfo: clientcmdapi.Cluster{
				Server: *apiServerHost,
			},
		}).ClientConfig()
	if err != nil {
		glog.Fatalf("error creating client configuration: %v", err)
	}

	return config
}

func azAuthFromConfigMap(kubeclient kubernetes.Interface, mapName string) auth.ClientSetup {
	// TODO: We should be able to do this declaratively - seems like there is a way to
	// map a configmap into filespace.
	cm, err := kubeclient.CoreV1().ConfigMaps("default").Get(mapName, metav1.GetOptions{})
	if err != nil {
		glog.Fatalf("Error retrieving azure-config configmap %s: %v", mapName, err)
	}

	authJSON := cm.Data["AZURE_AUTH_JSON"]

	authFile := "azureauth-ksrjtnfowfekj.json" // temporary keyboard spew, TODO: uniqueify
	err = ioutil.WriteFile(authFile, []byte(authJSON), 0)
	if err != nil {
		glog.Fatalf("Error writing azure config to temp file: %v", err)
	}

	os.Setenv("AZURE_AUTH_LOCATION", authFile)
	authn, err := auth.GetClientSetup(network.DefaultBaseURI)
	if err != nil {
		glog.Fatalf("Error creating Azure client from config: %v", err)
	}

	return authn
}

func azureConfigFromConfigMap(kubeclient kubernetes.Interface, mapName string) context.AzureConfig {
	cm, err := kubeclient.CoreV1().ConfigMaps("default").Get(mapName, metav1.GetOptions{})
	if err != nil {
		glog.Fatalf("Error retrieving azure-config configmap %s: %v", mapName, err)
	}

	resourceGroup, ok := cm.Data["AZURE_RESOURCE_GROUP"]
	if !ok {
		glog.Fatalf("AZURE_RESOURCE_GROUP not found in configmap %s", mapName)
	}

	vnetName, ok := cm.Data["AZURE_VNET_NAME"]
	if !ok {
		glog.Fatalf("AZURE_VNET_NAME not found in configmap %s", mapName)
	}

	return context.AzureConfig{
		ResourceGroup: resourceGroup,
		VnetName:      vnetName,
	}
}
