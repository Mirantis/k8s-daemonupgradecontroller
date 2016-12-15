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
IMAGE_REPO ?= mirantis/k8s-daemonupgradecontroller
IMAGE_TAG ?= 0.1

BUILD_DIR = _output
VENDOR_DIR = vendor


.PHONY: help
help:
	@echo "Usage: 'make <target>'"
	@echo ""
	@echo "Targets:"
	@echo "help            - Print this message and exit"
	@echo "get-deps        - Install project dependencies"
	@echo "build           - Build daemonupgrade binary"
	@echo "build-image     - Build docker image"
	@echo "test            - Run all tests"
	@echo "unit            - Run unit tests"
	@echo "e2e             - Run e2e tests"
	@echo "clean           - Delete binaries"
	@echo "clean-all       - Delete binaries and vendor files"

.PHONY: get-deps
get-deps: $(VENDOR_DIR)


.PHONY: build
build: $(BUILD_DIR)/daemonupgradecontroller


.PHONY: build-image
build-image: $(BUILD_DIR)/daemonupgradecontroller
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .
	docker save $(IMAGE_REPO):$(IMAGE_TAG) > $(BUILD_DIR)/ipcontroller.tar


.PHONY: unit
unit: $(VENDOR_DIR)
	go test -v ./pkg/...


.PHONY: e2e
e2e: $(VENDOR_DIR)
	go test -v ./test/e2e/ --master=http://127.0.0.1:8888


.PHONY: test
test: unit e2e


.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)


.PHONY: clean-all
clean-all: clean
	rm -rf $(VENDOR_DIR)
	docker rmi -f $(IMAGE_REPO):$(IMAGE_TAG)


$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)


$(BUILD_DIR)/daemonupgradecontroller: $(BUILD_DIR) $(VENDOR_DIR)
	CGO_ENABLED=0 go build -o $(BUILD_DIR)/daemonupgradecontroller cmd/daemonupgradecontroller.go


$(VENDOR_DIR):
	go get github.com/Masterminds/glide
	glide install --strip-vendor
