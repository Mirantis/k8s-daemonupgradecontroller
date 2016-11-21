# Copyright 2016 Mirantis
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

.PHONY: all docker

TAG ?= mirantis/k8s-daemonupgradecontroller
builddir=_output

all: build docker

build:
	if [ ! -d vendor ]; then $(MAKE) install-vendor; fi
	go build -o $(builddir)/daemonupgradecontroller cmd/daemonupgradecontroller.go

verify-glide-installation:
	@which glide || go get github.com/Masterminds/glide

install-vendor: verify-glide-installation
	glide install --strip-vendor

update-vendor: verify-glide-installation
	glide update --strip-vendor

docker:
	docker build -t $(TAG) .

clean:
	rm -fr $(builddir)/*
	docker rmi $(TAG) -f || /bin/true
