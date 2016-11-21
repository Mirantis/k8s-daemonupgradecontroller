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
	"fmt"
	"strconv"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/apis/extensions"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/util/intstr"
)

const (
	// DaemonPausedAnnotation says if daemon upgrades are paused
	DaemonPausedAnnotation = "daemonset.kubernetes.io/paused"
	// DaemonRollbackTo
	DaemonRollbackToAnnotation = "daemonset.kubernetes.io/rollbackTo"
	// DaemonStrategyTypeAnnotation
	DaemonStrategyTypeAnnotation = "daemonset.kubernetes.io/strategyType"
	// daemonMaxUnavailableAnnotation
	DaemonMaxUnavailableAnnotation = "daemonset.kubernetes.io/maxUnavailable"
)

func IsPaused(ds *extensions.DaemonSet) bool {
	if ds.ObjectMeta.Annotations != nil {
		v, ok := ds.ObjectMeta.Annotations[DaemonPausedAnnotation]
		if !ok {
			return false
		}
		if v == "1" || v == "true" {
			return true
		}
	}
	return false
}

func GetRollbackTo(ds *extensions.DaemonSet) *extensions.RollbackConfig {
	if ds.ObjectMeta.Annotations != nil {
		revision, ok := ds.ObjectMeta.Annotations[DaemonRollbackToAnnotation]
		if ok {
			i, err := strconv.ParseInt(revision, 10, 64)
			if err == nil {
				rollbackTo := extensions.RollbackConfig{
					Revision: i}
				return &rollbackTo
			}
		}
	}
	return nil
}

func GetStrategyType(ds *extensions.DaemonSet) string {
	if ds.ObjectMeta.Annotations != nil {
		strategyType, ok := ds.ObjectMeta.Annotations[DaemonStrategyTypeAnnotation]
		if ok {
			return strategyType
		}
	}
	return "Noop"
}

func GetMaxUnavailable(ds *extensions.DaemonSet) intstr.IntOrString {
	if ds.ObjectMeta.Annotations != nil {
		maxUnavailable, ok := ds.ObjectMeta.Annotations[DaemonMaxUnavailableAnnotation]
		if ok {
			i, err := strconv.ParseInt(maxUnavailable, 10, 64)
			if err == nil {
				return intstr.FromInt(int(i))
			}
		}
	}
	return intstr.FromInt(1)
}

func UpdateDsSpecHashLabel(c clientset.Interface, ds *extensions.DaemonSet, template *api.PodTemplate) error {
	podTemplateSpecHash, hashExists := template.ObjectMeta.Labels[extensions.DefaultDaemonSetUniqueLabelKey]
	if !hashExists {
		return fmt.Errorf("No label %s in DS %s spec", extensions.DefaultDaemonSetUniqueLabelKey, ds.Name)
	}
	ds.Spec.Template.ObjectMeta.Labels[extensions.DefaultDaemonSetUniqueLabelKey] = podTemplateSpecHash
	_, err := c.Extensions().DaemonSets(ds.Namespace).Update(ds)
	return err
}

func UpdatePodsTemplateHashLabel(c clientset.Interface, ds *extensions.DaemonSet, daemonPods []*api.Pod, template *api.PodTemplate) error {
	for _, daemonPod := range daemonPods {
		_, hashExists := daemonPod.ObjectMeta.Labels[extensions.DefaultDaemonSetUniqueLabelKey]
		if !hashExists {
			daemonPod.ObjectMeta.Labels[extensions.DefaultDaemonSetUniqueLabelKey] = template.ObjectMeta.Labels[extensions.DefaultDaemonSetUniqueLabelKey]
			_, err := c.Core().Pods(ds.Namespace).Update(daemonPod)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
