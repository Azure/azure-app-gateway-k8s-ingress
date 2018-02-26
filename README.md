# Azure Application Gateway Kubernetes Ingress Controller

This project supports [Kubernetes ingress](https://kubernetes.io/docs/concepts/services-networking/ingress/)
on Microsoft Azure via the [Azure Application Gateway L7 load balancer](https://docs.microsoft.com/en-us/azure/application-gateway/application-gateway-introduction).

**This project is currently in an early stage of development.  The code is not yet ready
to be deployed; we've opened it up to enable collaboration, not because it's suitable for
use with real workloads.  It is not beta, it is not even alpha... it is simply not ready!**
See "Status" below for a mere sample of the things that are missing and wrong!

# Building

The easiest way to build the project is to run the build environment in a Docker container.
To do this, run the `./scripts/devenv.ps1` script.  This will spin up a container with the
Go compiler installed and the Go environment set up correctly, and the code mounted in the
container, and leave you at a bash prompt inside the container.

**TODO:** implement the `devenv` script for Linux.  (The PS script was based closely on the
one from `acs-engine` so hopefully their Linux script will work for us too!)

From the bash prompt, you can run `./build.sh` to build the project.  The output is a Linux
executable at `./bin/azl7ic`.

To test your changes:

0. (One off)  Perform the steps in "Preparing Your Azure Cluster" below.
1. Publish the built executable as a Docker image.  You can do this using `./dockerize.sh`,
   which takes a Docker Hub username and a version, e.g. `./dockerize.sh itowlson 271031`.
   The tag is always `azl7ic` e.g. those arguments would produce `itowlson/azl7ic:271031`.
2. Run the image in your Azure Kubernetes cluster.  You can do this using `./kuberun.sh`,
   which takes the same arguments as `./dockerize.sh`.  Or run `kubectl` directly, using
   the content of `./kuberun.sh`.
3. Create an ingress with the annotation `kubernetes.io/ingress.class: azure-application-gateway`.
   There are some sample test files under the `./test` directory.
4. WAIT.  Then wait some more.  It can take 20-40 minutes for an application gateway to spin up.

Once you have finished testing, to clean up:

1. Delete the ingress.  The ingress controller should spin down the application gateway, though
   it's wise to check the portal and delete it manually if not.
2. Delete the `azl7ic` deployment.

**TODO:** have `kubectl` available in the build container (suitably configured somehow).

# Preparing Your Azure Cluster

There are a few things you need to do before you can test the ingress controller in a cluster.
These need to be done only once per cluster, but they need to be done anew for each new
cluster.

## Setting up the ConfigMap

The ingress controller currently relies on a config map for Azure authentication (to talk to
the Azure API to create and modify gateways) and configuration (e.g. resource group and vnet
in which to set up the gateway).  The config map must be called `azure-config` and must contain
three entries:

* `AZURE_AUTH_JSON`: a JSON string containing Azure SDK authentication info.  You can create
  suitable JSON by creating a service principal with subscription or resource group permissions
  and passing the `--sdk-auth` option: `az ad sp create-for-rbac --sdk-auth`
* `AZURE_RESOURCE_GROUP`: the resource group containing the cluster
* `AZURE_VNET_NAME`: the vnet containing the cluster

The script `./cm.sh` will create the config map from an auth filename and the two names, e.g.
`./cm.sh azureauth.json acstestgroup k8s-vnet-12345678`.

## Setting up Test Services

You will need services to expose.  There are some examples in the `./test/services` directory
or you can use your own.

**Your services MUST be created with `--type=NodePort`.**  The default type (ClusterIP) is not
sufficient, and you don't want LoadBalancer because that will create an Azure load balancer.
`./test/services/commands.txt` gives examples of service exposure commands.

# Current Status

This is pretty much proof of concept at the moment, and bears the scars of a _lot_ of trial and
error trying to get things working!  I have tested some core scenarios on ACS.  Things that I
know are missing/wrong at the functional level:

* No support for TLS.
* No support for Web Application Firewall.
* No fine control of app gateway settings (e.g. size).
* No handling of scale up/down (haven't been able to progress this because of an [ACS issue](https://github.com/Azure/acs-engine/issues/2063)).
* No handling of service changes/deletion.

Things that I know are missing/wrong at the operational level:

* No granularity of log levels (it's all level 1, and all logged).
* No consistency to log formats.
* Limited error/status reporting.
* No retries.
* Better way to manage authentication and configuration.
* Better control of the subnet containing the gateway.

Things that I know are missing/wrong at the code/dev level:

* No automated tests.
* Haphazard organisation of code.
* Core gateway spec algorithm needs to be made easier to reason about (simplify, improve
  readability, tests!).
* No design documentation.
* Continuous integration / testing (including PR validation).
* Probably lots of gaps in the README and contributor documentation!  *grin*

**TODO:** Create GitHub issues for all of these.

# Contributing

This project welcomes contributions and suggestions.  Most contributions require you to agree to a
Contributor License Agreement (CLA) declaring that you have the right to, and actually do, grant us
the rights to use your contribution. For details, visit https://cla.microsoft.com.

When you submit a pull request, a CLA-bot will automatically determine whether you need to provide
a CLA and decorate the PR appropriately (e.g., label, comment). Simply follow the instructions
provided by the bot. You will only need to do this once across all repos using our CLA.

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/).
For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or
contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.
