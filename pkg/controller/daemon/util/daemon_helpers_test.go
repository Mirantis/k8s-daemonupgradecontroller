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

	"k8s.io/kubernetes/pkg/apis/extensions"
	"k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset/fake"
	"k8s.io/kubernetes/pkg/util/intstr"
)

func TestGettingMaxUnavailable(t *testing.T) {
	ds := newDaemonSet("test", "testImage")
	val := GetMaxUnavailable(ds)
	if val != intstr.FromInt(1) {
		t.Errorf("Unexpected value of MaxUnavailable. Expected: 1, got %s", val)
	}

	ds.ObjectMeta.Annotations = map[string]string{}
	val = GetMaxUnavailable(ds)
	if val != intstr.FromInt(1) {
		t.Errorf("Unexpected value of MaxUnavailable. Expected: 1, got %s", val)
	}

	ds.ObjectMeta.Annotations[DaemonMaxUnavailableAnnotation] = "one"
	val = GetMaxUnavailable(ds)
	if val != intstr.FromInt(1) {
		t.Errorf("Unexpected value of MaxUnavailable. Expected: 1, got %s", val)
	}

	ds.ObjectMeta.Annotations[DaemonMaxUnavailableAnnotation] = "3"
	val = GetMaxUnavailable(ds)
	if val != intstr.FromInt(3) {
		t.Errorf("Unexpected value of MaxUnavailable. Expected: 3, got %s", val)
	}
}

func TestIsPaused(t *testing.T) {
	ds := newDaemonSet("test", "testImage")
	paused := IsPaused(ds)
	if paused != false {
		t.Errorf("Unexpected value of paused. Expected: false, got %t", paused)
	}

	ds.ObjectMeta.Annotations = map[string]string{}
	paused = IsPaused(ds)
	if paused != false {
		t.Errorf("Unexpected value of paused. Expected: false, got %t", paused)
	}

	ds.ObjectMeta.Annotations[DaemonPausedAnnotation] = "one"
	paused = IsPaused(ds)
	if paused != false {
		t.Errorf("Unexpected value of paused. Expected: false, got %t", paused)
	}

	ds.ObjectMeta.Annotations[DaemonPausedAnnotation] = "1"
	paused = IsPaused(ds)
	if paused != true {
		t.Errorf("Unexpected value of paused. Expected: true , got %t", paused)
	}

	ds.ObjectMeta.Annotations[DaemonPausedAnnotation] = "true"
	paused = IsPaused(ds)
	if paused != true {
		t.Errorf("Unexpected value of paused. Expected: true , got %t", paused)
	}
}

func TestGetRollbackTo(t *testing.T) {
	ds := newDaemonSet("test", "testImage")
	rollback := GetRollbackTo(ds)
	if rollback != nil {
		t.Errorf("Unexpected value of rollback. Expected: nil, got %v", rollback)
	}

	ds.ObjectMeta.Annotations = map[string]string{}
	rollback = GetRollbackTo(ds)
	if rollback != nil {
		t.Errorf("Unexpected value of rollback. Expected: nil, got %v", rollback)
	}

	ds.ObjectMeta.Annotations[DaemonRollbackToAnnotation] = "one"
	rollback = GetRollbackTo(ds)
	if rollback != nil {
		t.Errorf("Unexpected value of rollback. Expected: nil, got %v", rollback)
	}

	ds.ObjectMeta.Annotations[DaemonRollbackToAnnotation] = "5"
	rollback = GetRollbackTo(ds)
	if rollback == nil {
		t.Errorf("Unexpected value of rollback. Expected: struct, got nil")
	}
	if rollback.Revision != 5 {
		t.Errorf("Unexpected value of rollback.Revision. Expected: 5, got: %d", rollback.Revision)
	}
}

func TestGetStrategyType(t *testing.T) {
	ds := newDaemonSet("test", "testImage")
	strategy := GetStrategyType(ds)
	if strategy != "Noop" {
		t.Errorf("Unexpected strategy type. Expected: Noop, got %s", strategy)
	}

	ds.ObjectMeta.Annotations = map[string]string{}
	strategy = GetStrategyType(ds)
	if strategy != "Noop" {
		t.Errorf("Unexpected strategy type. Expected: Noop, got %s", strategy)
	}

	ds.ObjectMeta.Annotations[DaemonStrategyTypeAnnotation] = "one"
	strategy = GetStrategyType(ds)
	if strategy != "one" {
		t.Errorf("Unexpected strategy type. Expected: one, got %s", strategy)
	}
}

func TestUpdateDsSpecHashLabel(t *testing.T) {
	fakeClient := &fake.Clientset{}
	ds := newDaemonSet("test", "testImage")
	template := newPodTemplate("image", "1")

	template.ObjectMeta.Labels[extensions.DefaultDaemonSetUniqueLabelKey] = "hash"
	err := UpdateDsSpecHashLabel(fakeClient, ds, template)
	_, ok := ds.Spec.Template.ObjectMeta.Labels[extensions.DefaultDaemonSetUniqueLabelKey]
	if !ok {
		t.Errorf("Hash not copied.")
	}

	delete(template.ObjectMeta.Labels, extensions.DefaultDaemonSetUniqueLabelKey)
	err = UpdateDsSpecHashLabel(fakeClient, ds, template)
	if err == nil {
		t.Errorf("Excpected error, got nil")
	}

}
