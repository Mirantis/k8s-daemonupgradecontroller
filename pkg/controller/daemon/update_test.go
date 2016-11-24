/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package daemonupgradecontroller

import (
	"testing"

	"k8s.io/kubernetes/pkg/api"
)

// DaemonSets not take any actions when being deleted
func TestNeedsUpdate(t *testing.T) {
	manager, _ := newTestController()
	ds := newDaemonSet("test")
	_, needsUpdate := manager.needsUpdate(ds, []*api.Pod{}, 0, 0)
	if needsUpdate != false {
		t.Errorf("Unexcpected return value. Excpected false, got true")
	}
}
