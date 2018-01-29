$pwd = (Get-Location).Path

docker build --pull -t azure-app-gateway-k8s-ingress -f devenv.Dockerfile .
docker run --security-opt seccomp:unconfined -it `
	-v ${pwd}:/gopath/src/github.com/Azure/azure-app-gateway-k8s-ingress `
	-w /gopath/src/github.com/Azure/azure-app-gateway-k8s-ingress `
		azure-app-gateway-k8s-ingress /bin/bash
