package utils

import (
	"errors"
	"strings"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
)

var (
	errNoNodePort = errors.New("Service defined no node ports")
)

// ResourceName parses the resource name out of an ARM resource ID
func ResourceName(resourceID *string) string {
	bits := strings.Split(*resourceID, "/")
	return bits[len(bits)-1]
}

// TryAll runs an async function for all strings in a list
func TryAll(f func(string) <-chan error, strs []string) error {
	cherrs := [](<-chan error){}
	for _, s := range strs {
		cherr := f(s)
		cherrs = append(cherrs, cherr)
	}
	for _, cherr := range cherrs {
		err := <-cherr
		if err != nil {
			return err
		}
	}
	return nil
}

// GetNodePort gets port information for a service
// TODO: do we need to consider the service port requested in the
// ingress?
func GetNodePort(service *v1.Service) (port, nodePort int32, err error) {
	for _, p := range service.Spec.Ports {
		if p.NodePort != 0 {
			glog.V(3).Infof("%s/%s located on node port %d", service.Namespace, service.Name, p.NodePort)
			return p.Port, p.NodePort, nil
		}
	}

	return 0, 0, errNoNodePort
}
