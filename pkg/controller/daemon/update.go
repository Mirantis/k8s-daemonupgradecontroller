/*
Copyright 2015 The Kubernetes Authors.

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

	daemonutil "github.com/Mirantis/k8s-daemonupgradecontroller/pkg/controller/daemon/util"
	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/apis/extensions"
	"k8s.io/kubernetes/pkg/util/intstr"
	labelsutil "k8s.io/kubernetes/pkg/util/labels"
	podutil "k8s.io/kubernetes/pkg/util/pod"
)

func (dsc *DaemonUpgradeController) needsRollingUpdate(ds *extensions.DaemonSet, daemonPods []*api.Pod, numUnavailable, numNodes int) (*api.Pod, bool) {
	maxUnavailablePods := daemonutil.GetMaxUnavailable(ds)
	// maxUnavailable, err := intstr.GetValueFromIntOrPercent(&ds.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable, numNodes, true)
	maxUnavailable, err := intstr.GetValueFromIntOrPercent(&maxUnavailablePods, numNodes, true)
	if err != nil {
		glog.Errorf("Invalid value for MaxUnavailable: %v", err)
		return nil, false
	}
	if numUnavailable >= maxUnavailable {
		glog.V(4).Infof("Number of unavailable DaemonSet pods: %d, is equal to or exceeds allowed maximum: %d", numUnavailable, maxUnavailable)
		return nil, false
	}

	needsUpdate := true
	var podToRetain *api.Pod

	// make api.PodTemplateSpec copy without extensions.DefaultDaemonSetUniqueLabelKey label
	obj, _ := api.Scheme.DeepCopy(ds.Spec.Template)
	template := obj.(api.PodTemplateSpec)
	template.ObjectMeta.Labels = labelsutil.CloneAndRemoveLabel(
		ds.Spec.Template.ObjectMeta.Labels,
		extensions.DefaultDaemonSetUniqueLabelKey,
	)

	newPodTemplateSpecHash := podutil.GetPodTemplateSpecHash(template)
	newPodTemplateSpecHashStr := strconv.FormatUint(uint64(newPodTemplateSpecHash), 10)
	fmt.Printf("POD T HASH %+v\n", newPodTemplateSpecHashStr)
	for _, daemonPod := range daemonPods {
		curPodTemplateSpecHash, hashExists := daemonPod.ObjectMeta.Labels[extensions.DefaultDaemonSetUniqueLabelKey]
		fmt.Printf("    POD HASH %+v\n", curPodTemplateSpecHash)
		if hashExists && curPodTemplateSpecHash == newPodTemplateSpecHashStr {
			podToRetain = daemonPod
			needsUpdate = false
			break
		}
		// special case
		// label is not set yet
		if !hashExists {
			fmt.Printf("ups\n")
			needsUpdate = false
			break
		}
	}
	return podToRetain, needsUpdate
}

func (dsc *DaemonUpgradeController) needsUpdate(ds *extensions.DaemonSet, daemonPods []*api.Pod, numUnavailable, numNodes int) (*api.Pod, bool) {
	if daemonPods == nil {
		return nil, false
	}

	updateStrategyType := daemonutil.GetStrategyType(ds)
	// switch ds.Spec.UpdateStrategy.Type {
	switch updateStrategyType {
	// case extensions.RollingUpdateDaemonSetStrategyType:
	case "RollingUpdate":
		return dsc.needsRollingUpdate(ds, daemonPods, numUnavailable, numNodes)
	// case extensions.NoopDaemonSetStrategyType:
	case "Noop":
		return nil, false
	}
	return nil, false
}
