FROM ubuntu:16.04
MAINTAINER Łukasz Oleś <lukaszoles@gmail.com>
LABEL Name="k8s-daemonupgradecontroller"

RUN apt-get update -qq && \
    DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -qqy \
        curl \
        ca-certificates \
        && \
    apt-get clean

COPY _output/daemonupgradecontroller /usr/bin/

RUN chmod +x /usr/bin/daemonupgradecontroller

ENTRYPOINT ["/usr/bin/daemonupgradecontroller"]
