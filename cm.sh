#! /bin/bash
if [ $# -ne 3 ]; then
  echo "needs azure auth json file path, resource group name and vnet name"
  exit 1
fi

authfile=$1
rgname=$2
vnetname=$3

kubectl create configmap azure-config --from-file=AZURE_AUTH_JSON=${authfile} --from-literal=AZURE_RESOURCE_GROUP=${rgname} --from-literal=AZURE_VNET_NAME=${vnetname}
