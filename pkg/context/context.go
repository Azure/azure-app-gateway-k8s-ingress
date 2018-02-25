/*
Copyright 2017 The Kubernetes Authors.

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
