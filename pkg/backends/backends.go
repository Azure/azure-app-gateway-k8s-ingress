package backends

import (
	"github.com/Azure/azure-app-gateway-k8s-ingress/pkg/utils"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// ServicePort holds information about how a service is accessed
type ServicePort struct {
	Port        int64
	Protocol    utils.AppProtocol
	ServiceName types.NamespacedName
	ServicePort intstr.IntOrString
}
