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
	"sync"
	"testing"

	daemonutil "github.com/Mirantis/k8s-daemonupgradecontroller/pkg/controller/daemon/util"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/resource"
	"k8s.io/kubernetes/pkg/api/testapi"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/apis/extensions"
	"k8s.io/kubernetes/pkg/client/cache"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset/fake"
	"k8s.io/kubernetes/pkg/client/restclient"
	"k8s.io/kubernetes/pkg/controller"
	"k8s.io/kubernetes/pkg/controller/informers"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/securitycontext"
	labelsutil "k8s.io/kubernetes/pkg/util/labels"
	"k8s.io/kubernetes/pkg/util/wait"
)

var (
	simpleDaemonSetLabel  = map[string]string{"name": "simple-daemon", "type": "production"}
	simpleDaemonSetLabel2 = map[string]string{"name": "simple-daemon", "type": "test"}
	simpleNodeLabel       = map[string]string{"color": "blue", "speed": "fast"}
	simpleNodeLabel2      = map[string]string{"color": "red", "speed": "fast"}
	alwaysReady           = func() bool { return true }
	maxUnavailable        = 2
)

func getKey(ds *extensions.DaemonSet, t *testing.T) string {
	if key, err := controller.KeyFunc(ds); err != nil {
		t.Errorf("Unexpected error getting key for ds %v: %v", ds.Name, err)
		return ""
	} else {
		return key
	}
}

func newDaemonSet(name string) *extensions.DaemonSet {
	labels := map[string]string{}
	for key, value := range simpleDaemonSetLabel {
		labels[key] = value
	}
	return &extensions.DaemonSet{
		TypeMeta: unversioned.TypeMeta{APIVersion: testapi.Extensions.GroupVersion().String()},
		ObjectMeta: api.ObjectMeta{
			Name:        name,
			Namespace:   api.NamespaceDefault,
			Annotations: map[string]string{},
		},
		Spec: extensions.DaemonSetSpec{
			Selector: &unversioned.LabelSelector{MatchLabels: simpleDaemonSetLabel},
			Template: api.PodTemplateSpec{
				ObjectMeta: api.ObjectMeta{
					Labels: labels,
				},
				Spec: api.PodSpec{
					Containers: []api.Container{
						{
							Image: "foo/bar",
							TerminationMessagePath: api.TerminationMessagePathDefault,
							ImagePullPolicy:        api.PullIfNotPresent,
							SecurityContext:        securitycontext.ValidSecurityContextWithContainerDefaults(),
						},
					},
					DNSPolicy: api.DNSDefault,
				},
			},
		},
	}
}

func newNode(name string, label map[string]string) *api.Node {
	return &api.Node{
		TypeMeta: unversioned.TypeMeta{APIVersion: testapi.Default.GroupVersion().String()},
		ObjectMeta: api.ObjectMeta{
			Name:      name,
			Labels:    label,
			Namespace: api.NamespaceDefault,
		},
		Status: api.NodeStatus{
			Conditions: []api.NodeCondition{
				{Type: api.NodeReady, Status: api.ConditionTrue},
			},
			Allocatable: api.ResourceList{
				api.ResourcePods: resource.MustParse("100"),
			},
		},
	}
}

func addNodes(nodeStore cache.Store, startIndex, numNodes int, label map[string]string) {
	for i := startIndex; i < startIndex+numNodes; i++ {
		nodeStore.Add(newNode(fmt.Sprintf("node-%d", i), label))
	}
}

func newPod(podName string, nodeName string, label map[string]string) *api.Pod {
	pod := &api.Pod{
		TypeMeta: unversioned.TypeMeta{APIVersion: testapi.Default.GroupVersion().String()},
		ObjectMeta: api.ObjectMeta{
			GenerateName: podName,
			Labels:       label,
			Namespace:    api.NamespaceDefault,
		},
		Spec: api.PodSpec{
			NodeName: nodeName,
			Containers: []api.Container{
				{
					Image: "foo/bar",
					TerminationMessagePath: api.TerminationMessagePathDefault,
					ImagePullPolicy:        api.PullIfNotPresent,
					SecurityContext:        securitycontext.ValidSecurityContextWithContainerDefaults(),
				},
			},
			DNSPolicy: api.DNSDefault,
		},
	}
	api.GenerateName(api.SimpleNameGenerator, &pod.ObjectMeta)
	return pod
}

func addPods(podStore cache.Store, nodeName string, label map[string]string, number int) {
	for i := 0; i < number; i++ {
		podStore.Add(newPod(fmt.Sprintf("%s-", nodeName), nodeName, label))
	}
}

type fakePodControl struct {
	sync.Mutex
	*controller.FakePodControl
	podStore *cache.StoreToPodLister
	PodIDMap map[string]*api.Pod
}

func (f *fakePodControl) CreatePodsOnNode(nodeName, namespace string, template *api.PodTemplateSpec, object runtime.Object) error {
	f.Lock()
	defer f.Unlock()
	if err := f.FakePodControl.CreatePodsOnNode(nodeName, namespace, template, object); err != nil {
		return fmt.Errorf("failed to create pod on node %q", nodeName)
	}

	pod := &api.Pod{
		ObjectMeta: api.ObjectMeta{
			Namespace:    namespace,
			GenerateName: fmt.Sprintf("%s-", nodeName),
		},
	}

	pod.ObjectMeta.Labels = labelsutil.CloneAndRemoveLabel(
		template.ObjectMeta.Labels,
		extensions.DefaultDaemonSetUniqueLabelKey,
	)

	if err := api.Scheme.Convert(&template.Spec, &pod.Spec, nil); err != nil {
		return fmt.Errorf("unable to convert pod template: %v", err)
	}
	if len(nodeName) != 0 {
		pod.Spec.NodeName = nodeName
	}
	api.GenerateName(api.SimpleNameGenerator, &pod.ObjectMeta)

	f.podStore.Indexer.Add(pod)
	f.PodIDMap[pod.Name] = pod
	return nil
}

func (f *fakePodControl) DeletePod(namespace string, podID string, object runtime.Object) error {
	f.Lock()
	defer f.Unlock()
	if err := f.FakePodControl.DeletePod(namespace, podID, object); err != nil {
		return fmt.Errorf("failed to delete pod %q", podID)
	}
	pod, ok := f.PodIDMap[podID]
	if !ok {
		return fmt.Errorf("pod %q does not exist", podID)
	}
	f.podStore.Indexer.Delete(pod)
	delete(f.PodIDMap, podID)
	return nil
}

type fakePodTemplateControl struct {
	*daemonutil.PodTemplateController
}

func (f *fakePodTemplateControl) CreatePodTemplate(template *api.PodTemplate, namespace string) (*api.PodTemplate, error) {
	return template, nil
}

func newTestController() (*DaemonUpgradeController, *fakePodControl) {
	clientset := clientset.NewForConfigOrDie(&restclient.Config{Host: "", ContentConfig: restclient.ContentConfig{GroupVersion: testapi.Default.GroupVersion()}})
	fake := &fake.Clientset{}
	informerFactory := informers.NewSharedInformerFactory(clientset, controller.NoResyncPeriodFunc())

	manager := NewDaemonUpgradeController(informerFactory.DaemonSets(), informerFactory.Pods(), informerFactory.Nodes(), fake, 0)
	informerFactory.Start(wait.NeverStop)

	manager.podStoreSynced = alwaysReady
	manager.nodeStoreSynced = alwaysReady
	podControl := &fakePodControl{
		FakePodControl: &controller.FakePodControl{},
		podStore:       manager.podStore,
		PodIDMap:       make(map[string]*api.Pod),
	}
	podTemplateControl := &fakePodTemplateControl{
		PodTemplateController: &daemonutil.PodTemplateController{
			KubeClient: fake,
		},
	}
	manager.podControl = podControl
	manager.podTemplateController = podTemplateControl
	return manager, podControl
}

func validateSyncDaemonSets(t *testing.T, fakePodControl *fakePodControl, expectedCreates, expectedDeletes int) {
	if len(fakePodControl.Templates) != expectedCreates {
		t.Errorf("Unexpected number of creates.  Expected %d, saw %d\n", expectedCreates, len(fakePodControl.Templates))
	}
	if len(fakePodControl.DeletePodName) != expectedDeletes {
		t.Errorf("Unexpected number of deletes.  Expected %d, saw %d\n", expectedDeletes, len(fakePodControl.DeletePodName))
	}
}

func syncAndValidateDaemonSets(t *testing.T, manager *DaemonUpgradeController, ds *extensions.DaemonSet, podControl *fakePodControl, expectedCreates, expectedDeletes int) {
	key, err := controller.KeyFunc(ds)
	if err != nil {
		t.Errorf("Could not get key for daemon.")
	}
	manager.syncHandler(key)
	validateSyncDaemonSets(t, podControl, expectedCreates, expectedDeletes)
}

// clearExpectations copies the FakePodControl to PodStore and clears the create and delete expectations.
func clearExpectations(t *testing.T, manager *DaemonUpgradeController, ds *extensions.DaemonSet, fakePodControl *fakePodControl) {
	//	nCreates := len(fakePodControl.Templates)
	//	nDeletes := len(fakePodControl.DeletePodName)
	fakePodControl.Clear()

	key, err := controller.KeyFunc(ds)
	if err != nil {
		t.Errorf("Could not get key for daemon.")
		return
	}
	manager.expectations.DeleteExpectations(key)
}

func allocatableResources(memory, cpu string) api.ResourceList {
	return api.ResourceList{
		api.ResourceMemory: resource.MustParse(memory),
		api.ResourceCPU:    resource.MustParse(cpu),
		api.ResourcePods:   resource.MustParse("100"),
	}
}

// DaemonSet should launch pods and update them when the pod template changes.
func TestDaemonSetUpdatesPods(t *testing.T) {
	manager, podControl := newTestController()
	addNodes(manager.nodeStore.Store, 0, 5, nil)
	ds := newDaemonSet("foo")
	manager.dsStore.Add(ds)
	podControl.CreatePodsOnNode("node-0", ds.Namespace, &ds.Spec.Template, ds)
	podControl.CreatePodsOnNode("node-1", ds.Namespace, &ds.Spec.Template, ds)
	podControl.CreatePodsOnNode("node-2", ds.Namespace, &ds.Spec.Template, ds)
	podControl.CreatePodsOnNode("node-3", ds.Namespace, &ds.Spec.Template, ds)
	podControl.CreatePodsOnNode("node-4", ds.Namespace, &ds.Spec.Template, ds)
	syncAndValidateDaemonSets(t, manager, ds, podControl, 5, 0)

	fmt.Printf("TERAZ %s, labels: %v\n", ds.Spec.Template.ObjectMeta.Labels[extensions.DefaultDaemonSetUniqueLabelKey], ds.Spec.Selector)
	for _, daemonPod := range podControl.PodIDMap {
		fmt.Printf("  Start hash: %s, labels: %v \n", daemonPod.ObjectMeta.Labels[extensions.DefaultDaemonSetUniqueLabelKey], daemonPod.ObjectMeta.Labels)
	}

	ds.Spec.Template.Spec.Containers[0].Image = "foo2/bar2"
	ds.ObjectMeta.Annotations[daemonutil.DaemonStrategyTypeAnnotation] = "RollingUpdate"
	ds.ObjectMeta.Annotations[daemonutil.DaemonMaxUnavailableAnnotation] = strconv.Itoa(maxUnavailable)

	manager.dsStore.Update(ds)

	clearExpectations(t, manager, ds, podControl)
	syncAndValidateDaemonSets(t, manager, ds, podControl, 0, maxUnavailable)

	clearExpectations(t, manager, ds, podControl)
	syncAndValidateDaemonSets(t, manager, ds, podControl, 0, maxUnavailable)

	clearExpectations(t, manager, ds, podControl)
	syncAndValidateDaemonSets(t, manager, ds, podControl, 0, 1)

	clearExpectations(t, manager, ds, podControl)
	syncAndValidateDaemonSets(t, manager, ds, podControl, 0, 0)
}

// DaemonSet should NOT launch pods and update them when the DaemonSet is paused.
func TestDaemonSetPaused(t *testing.T) {
	manager, podControl := newTestController()
	addNodes(manager.nodeStore.Store, 0, 5, nil)
	ds := newDaemonSet("foo")
	manager.dsStore.Add(ds)
	syncAndValidateDaemonSets(t, manager, ds, podControl, 5, 0)

	ds.Spec.Template.Spec.Containers[0].Image = "foo2/bar2"
	manager.dsStore.Update(ds)
	ds.ObjectMeta.Annotations["daemonset.kubernetes.io/paused"] = "1"

	clearExpectations(t, manager, ds, podControl)
	syncAndValidateDaemonSets(t, manager, ds, podControl, 0, 0)
}

// DaemonSet should launch pods and NOT update them when strategyType is NoopDaemonSetStrategyType.
func TestDaemonNoopStrategy(t *testing.T) {
	manager, podControl := newTestController()
	addNodes(manager.nodeStore.Store, 0, 5, nil)
	ds := newDaemonSet("foo")
	manager.dsStore.Add(ds)
	syncAndValidateDaemonSets(t, manager, ds, podControl, 5, 0)

	ds.Spec.Template.Spec.Containers[0].Image = "foo2/bar2"
	manager.dsStore.Update(ds)

	clearExpectations(t, manager, ds, podControl)
	syncAndValidateDaemonSets(t, manager, ds, podControl, 0, 0)
}
