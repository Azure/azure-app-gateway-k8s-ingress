#! /bin/bash
if [ $# -ne 2 ]; then
  echo "needs user and version suffix"
  exit 1
fi

user=$1
vsuffix=$2

set -e
CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o ./output/azl7ic ./cmd/azureag
docker build -t ${user}/azl7ic:${vsuffix} -f Dockerfile .
docker push ${user}/azl7ic:${vsuffix}
