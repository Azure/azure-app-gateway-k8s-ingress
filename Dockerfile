FROM ubuntu:16.04
RUN apt-get update
RUN apt-get install -y ca-certificates
ADD output/azl7ic /
CMD ["/azl7ic"]
