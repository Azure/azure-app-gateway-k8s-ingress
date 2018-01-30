#! /bin/bash
if [ $# -ne 2 ]; then
  echo "needs user and version suffix"
  exit 1
fi

user=$1
vsuffix=$2

docker run --mount type=bind,source=/home,destination=/home ${user}/azl7ic:${vsuffix}
