// Copyright 2016 Mirantis
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"time"

	testutils "github.com/Mirantis/k8s-daemonupgradecontroller/test/e2e/utils"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/apis/extensions"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/labels"
	"k8s.io/kubernetes/pkg/util/wait"
)

const (
	// this should not be a multiple of 5, because node status updates
	// every 5 seconds. See https://github.com/kubernetes/kubernetes/pull/14915.
	dsRetryPeriod  = 2 * time.Second
	dsRetryTimeout = 5 * time.Minute

	daemonsetLabelPrefix = "daemonset-"
	daemonsetNameLabel   = daemonsetLabelPrefix + "name"
	daemonsetColorLabel  = daemonsetLabelPrefix + "color"
)

var _ = Describe("Basic", func() {
	var c *clientset.Clientset
	var ns *api.Namespace

	BeforeEach(func() {
		var err error
		c, err = testutils.KubeClient()
		Expect(err).NotTo(HaveOccurred())
		namespaceObj := &api.Namespace{
			ObjectMeta: api.ObjectMeta{
				GenerateName: "e2e-tests-daemmonupgradecontroller-",
				Namespace:    "",
			},
			Status: api.NamespaceStatus{},
		}
		ns, err = c.Namespaces().Create(namespaceObj)
		Expect(err).NotTo(HaveOccurred())
	})

	image := "gcr.io/google_containers/serve_hostname:v1.4"
	redisImage := "gcr.io/google_containers/redis:e2e"
	dsName := "daemon-set"

	AfterEach(func() {
		if daemonsets, err := c.Extensions().DaemonSets(ns.Name).List(api.ListOptions{}); err == nil {
			//testutils.Logf("daemonset: %s", runtime.EncodeOrDie(api.Codecs.LegacyCodec(registered.EnabledVersions()...), daemonsets))
			for _, ds := range daemonsets.Items {
				testutils.Logf("Deleteing daemonset: %s", ds.Name)
				c.Extensions().DaemonSets(ds.Namespace).Delete(ds.Name, &api.DeleteOptions{})
			}
		} else {
			testutils.Logf("unable to dump daemonsets: %v", err)
		}
		if pods, err := c.Core().Pods(ns.Name).List(api.ListOptions{}); err == nil {
			for _, pod := range pods.Items {
				testutils.Logf("Deleteing pod: %s", pod.Name)
				c.Core().Pods(pod.Namespace).Delete(pod.Name, &api.DeleteOptions{})
			}
		} else {
			testutils.Logf("unable to dump pods: %v", err)
		}
		c.Namespaces().Delete(ns.Name, &api.DeleteOptions{})
	})

	It("should update pod when spec was updated and update strategy is rolling update", func() {
		label := map[string]string{daemonsetNameLabel: dsName}

		testutils.Logf("Creating simple daemon set %s", dsName)
		_, err := c.DaemonSets(ns.Name).Create(&extensions.DaemonSet{
			ObjectMeta: api.ObjectMeta{
				Name: dsName,
				Annotations: map[string]string{
					"daemonset.kubernetes.io/strategyType":   "RollingUpdate",
					"daemonset.kubernetes.io/maxUnavailable": "1",
				},
			},
			Spec: extensions.DaemonSetSpec{
				Template: api.PodTemplateSpec{
					ObjectMeta: api.ObjectMeta{
						Labels: label,
					},
					Spec: api.PodSpec{
						Containers: []api.Container{
							{
								Name:  dsName,
								Image: image,
								Ports: []api.ContainerPort{{ContainerPort: 9376}},
							},
						},
					},
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		By("Check that daemon pods launch on every node of the cluster.")
		Expect(err).NotTo(HaveOccurred())
		err = wait.Poll(dsRetryPeriod, dsRetryTimeout, checkRunningOnAllNodes(c, ns, label))
		Expect(err).NotTo(HaveOccurred(), "error waiting for daemon pod to start")

		By("Update daemon pods image.")
		ds, err := c.DaemonSets(ns.Name).Get(dsName)
		ds.Spec.Template.Spec.Containers[0].Image = redisImage
		_, err = c.DaemonSets(ns.Name).Update(ds)
		Expect(err).NotTo(HaveOccurred())

		By("Check that demon pods have set updated image.")
		err = wait.Poll(dsRetryPeriod, dsRetryTimeout, checkDaemonPodsImage(c, ns, label, redisImage))
		Expect(err).NotTo(HaveOccurred())

	})

	It("should rollback updated daemonset when strategy is rolling update", func() {
		label := map[string]string{daemonsetNameLabel: dsName}

		testutils.Logf("Creating simple daemon set %s", dsName)
		_, err := c.DaemonSets(ns.Name).Create(&extensions.DaemonSet{
			ObjectMeta: api.ObjectMeta{
				Name: dsName,
				Annotations: map[string]string{
					"daemonset.kubernetes.io/strategyType":   "RollingUpdate",
					"daemonset.kubernetes.io/maxUnavailable": "1",
				},
			},
			Spec: extensions.DaemonSetSpec{
				Template: api.PodTemplateSpec{
					ObjectMeta: api.ObjectMeta{
						Labels: label,
					},
					Spec: api.PodSpec{
						Containers: []api.Container{
							{
								Name:  dsName,
								Image: image,
								Ports: []api.ContainerPort{{ContainerPort: 9376}},
							},
						},
					},
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		By("Check that daemon pods launch on every node of the cluster.")
		Expect(err).NotTo(HaveOccurred())
		err = wait.Poll(dsRetryPeriod, dsRetryTimeout, checkRunningOnAllNodes(c, ns, label))
		Expect(err).NotTo(HaveOccurred(), "error waiting for daemon pod to start")

		By("Update daemon pods image.")
		ds, err := c.DaemonSets(ns.Name).Get(dsName)
		ds.Spec.Template.Spec.Containers[0].Image = redisImage
		_, err = c.DaemonSets(ns.Name).Update(ds)
		Expect(err).NotTo(HaveOccurred())

		By("Check that demon pods have set updated image.")
		err = wait.Poll(dsRetryPeriod, dsRetryTimeout, checkDaemonPodsImage(c, ns, label, redisImage))
		Expect(err).NotTo(HaveOccurred())

		By("rollback daemonset.")
		ds, err = c.DaemonSets(ns.Name).Get(dsName)
		ds.ObjectMeta.Annotations["daemonset.kubernetes.io/rollbackTo"] = "1"
		_, err = c.DaemonSets(ns.Name).Update(ds)
		Expect(err).NotTo(HaveOccurred())

		By("Check that demon pods have set original image.")
		err = wait.Poll(dsRetryPeriod, dsRetryTimeout, checkDaemonPodsImage(c, ns, label, image))
		Expect(err).NotTo(HaveOccurred())
	})
})

func checkDaemonPodsImage(c *clientset.Clientset, ns *api.Namespace, selector map[string]string, image string) func() (bool, error) {
	return func() (bool, error) {
		selector := labels.Set(selector).AsSelector()
		options := api.ListOptions{LabelSelector: selector}
		podList, err := c.Pods(ns.Name).List(options)
		if err != nil {
			return false, err
		}
		pods := podList.Items

		for _, pod := range pods {
			podImage := pod.Spec.Containers[0].Image
			if podImage != image || !api.IsPodReady(&pod) {
				testutils.Logf("Wrong image for pod: %s. Expected: %s, got: %s. Pod Ready: %t", pod.Name, image, podImage, api.IsPodReady(&pod))
				return false, nil
			}
		}
		return true, nil
	}
}

func checkDaemonPodOnNodes(c *clientset.Clientset, ns *api.Namespace, selector map[string]string, nodeNames []string) func() (bool, error) {
	return func() (bool, error) {
		selector := labels.Set(selector).AsSelector()
		options := api.ListOptions{LabelSelector: selector}
		podList, err := c.Core().Pods(ns.Name).List(options)
		if err != nil {
			return false, nil
		}
		pods := podList.Items

		nodesToPodCount := make(map[string]int)
		for _, pod := range pods {
			nodesToPodCount[pod.Spec.NodeName] += 1
		}
		testutils.Logf("nodesToPodCount: %#v", nodesToPodCount)

		// Ensure that exactly 1 pod is running on all nodes in nodeNames.
		for _, nodeName := range nodeNames {
			if nodesToPodCount[nodeName] != 1 {
				return false, nil
			}
		}

		// Ensure that sizes of the lists are the same. We've verified that every element of nodeNames is in
		// nodesToPodCount, so verifying the lengths are equal ensures that there aren't pods running on any
		// other nodes.
		return len(nodesToPodCount) == len(nodeNames), nil
	}
}

func checkRunningOnAllNodes(c *clientset.Clientset, ns *api.Namespace, selector map[string]string) func() (bool, error) {
	return func() (bool, error) {
		nodeList, err := c.Core().Nodes().List(api.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		nodeNames := make([]string, 0)
		for _, node := range nodeList.Items {
			nodeNames = append(nodeNames, node.Name)
		}
		return checkDaemonPodOnNodes(c, ns, selector, nodeNames)()
	}
}
