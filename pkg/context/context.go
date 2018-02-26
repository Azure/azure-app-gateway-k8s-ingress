package context

import (
	"time"

	informerv1 "k8s.io/client-go/informers/core/v1"
	informerv1beta1 "k8s.io/client-go/informers/extensions/v1beta1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// AzureConfig contains the Azure settings for creating application gateways
type AzureConfig struct {
	ResourceGroup string
	VnetName      string
}

// ControllerContext contains the informers and other settings for a controller
type ControllerContext struct {
	IngressInformer  cache.SharedIndexInformer
	ServiceInformer  cache.SharedIndexInformer
	PodInformer      cache.SharedIndexInformer
	NodeInformer     cache.SharedIndexInformer
	EndpointInformer cache.SharedIndexInformer
	StopChannel      chan struct{}
}

// NewControllerContext creates a context based on a Kubernetes client instance
func NewControllerContext(kubeClient kubernetes.Interface, namespace string, resyncPeriod time.Duration, enableEndpointsInformer bool) *ControllerContext {
	indexer := cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}
	context := &ControllerContext{
		IngressInformer: informerv1beta1.NewIngressInformer(kubeClient, namespace, resyncPeriod, indexer),
		ServiceInformer: informerv1.NewServiceInformer(kubeClient, namespace, resyncPeriod, indexer),
		PodInformer:     informerv1.NewPodInformer(kubeClient, namespace, resyncPeriod, indexer),
		NodeInformer:    informerv1.NewNodeInformer(kubeClient, resyncPeriod, indexer),
		StopChannel:     make(chan struct{}),
	}
	if enableEndpointsInformer {
		context.EndpointInformer = informerv1.NewEndpointsInformer(kubeClient, namespace, resyncPeriod, indexer)
	}
	return context
}

// Start runs all informers in the context
func (ctx *ControllerContext) Start() {
	go ctx.IngressInformer.Run(ctx.StopChannel)
	go ctx.ServiceInformer.Run(ctx.StopChannel)
	go ctx.PodInformer.Run(ctx.StopChannel)
	go ctx.NodeInformer.Run(ctx.StopChannel)
	if ctx.EndpointInformer != nil {
		go ctx.EndpointInformer.Run(ctx.StopChannel)
	}
}

// Stop stops all informers in the context
func (ctx *ControllerContext) Stop() {
	ctx.StopChannel <- struct{}{}
}
