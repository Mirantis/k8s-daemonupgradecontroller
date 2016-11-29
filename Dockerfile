FROM alpine
MAINTAINER Łukasz Oleś <lukaszoles@gmail.com>
LABEL Name="k8s-daemonupgradecontroller"

COPY _output/daemonupgradecontroller /usr/bin/

RUN chmod +x /usr/bin/daemonupgradecontroller

ENTRYPOINT ["/usr/bin/daemonupgradecontroller"]
