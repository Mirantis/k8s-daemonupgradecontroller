FROM ubuntu:16.04
MAINTAINER Łukasz Oleś <lukaszoles@gmail.com>
LABEL Name="k8s-daemonupgradecontroller" Version="0.1"

RUN apt-get update -qq && \
    DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -qqy \
        curl \
        ca-certificates \
        && \
    apt-get clean

COPY _output/daemonupgradecontroller /usr/local/bin/

RUN curl -o- https://raw.githubusercontent.com/karlkfi/resolveip/v1.0.2/install.sh | bash
