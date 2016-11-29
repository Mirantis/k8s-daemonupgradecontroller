[![Build Status](https://travis-ci.org/Mirantis/k8s-daemonupgradecontroller.svg?branch=master)](https://travis-ci.org/Mirantis/k8s-daemonupgradecontroller)
Daemon Set upgrade controller
=============================

Controller which manages Kubernetes DaemonSet upgrades. It stores history using PodTemplates,
allows rollback to previous version and pause the upgrade process.

Getting Started
===============

Installing
----------

Daemon upgrade controller can be started as a docker container inside of the Kubernetes cluster.

Create new file called `controller.yml`

```yaml
apiVersion: extensions/v1beta1
kind: Deployment
metadata:
  name: daemonupgrader
spec:
  replicas: 1
  template:
    metadata:
      labels:
        app: daemonupgrader
    spec:
      containers:
      - name: daemonupgrader
        image: mirantis/k8s-daemonupgradecontroller:latest
        imagePullPolicy: IfNotPresent
```

and start the container: `kubectl --namespace kube-system -f controller.yml`

Usage
-----

All configuration is done by setting options in daemon annotations.

By default upgrade controller will not touch the running daemons.
To enable upgrades, strategy type needs to be set:

`daemonset.kubernetes.io/strategyType: "RollingUpdate"`

from now on whenever `Spec.Template` is changed upgrade controller will delete old pods
and new pods with new template will be created. By default controller deletes one pod at the time and
waits for new to be in Ready state. It can by configured by setting how many pods can be unavailable 
during the upgrade:

`daemonset.kubernetes.io/maxUnavailable: "1"`

[![asciicast](https://asciinema.org/a/94027.png)](https://asciinema.org/a/94027)

All configuration options
-------------------------

#### daemonset.kubernetes.io/paused

When set to "true" or "1" it pauses upgrades.

#### daemonset.kubernetes.io/rollbackTo

Sets the revision number to which rollback. When rollback is done it's set to empty string.

#### daemonset.kubernetes.io/strategyType

For now two strategies are supported:

* RollingUpdate - deletes old pods and waiting for new to be created. By default it deletes one pod at the time.
* Noop - does nothing.

#### daemonset.kubernetes.io/maxUnavailable

Sets how many pods can be deleted in one step during the upgrade. Default: 1.

Building from source
====================

#### Rquirements

* make
* golang
* docker
* git
* mercurial
* configured go environemnt

Put this project in the $GOPATH

#### Building

```shell
make build
```

##### docker image

```shell
make build-image
```

#### Running unit tests

```
make unit
```

#### Running e2e tests

Start container with daemon upgrade controller. Make sure that it can connect to Kubernetes cluster. If [kubernetes dind cluster](https://github.com/sttts/kubernetes-dind-cluster) is used
then container can be started using [docker-compos.yaml](https://github.com/Mirantis/k8s-daemonupgradecontroller/blob/master/docker-compose.yaml) file.

```shell
make e2e
```

Authors
=======

Łukasz Oleś, github: lukaszo, IRC: salmon_, k8s slack: lukaszo

License
=======

Apache 2.0 License
