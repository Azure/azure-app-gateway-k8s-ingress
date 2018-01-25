#! /bin/bash
if [ $# -ne 2 ]; then
  echo "needs user and version suffix"
  exit 1
fi

user=$1
vsuffix=$2

kubectl run azl7ic --image=docker.io/${user}/azl7ic:${vsuffix} --command -- /azl7ic --running-in-cluster=true
