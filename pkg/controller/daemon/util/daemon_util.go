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

	"github.com/golang/glog"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/apis/extensions"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	labelsutil "k8s.io/kubernetes/pkg/util/labels"
	podutil "k8s.io/kubernetes/pkg/util/pod"
)

const (
	// RevisionAnnotation is the revision annotation of a daemon set pod template which records its rollout sequence
	RevisionAnnotation = "daemonset.kubernetes.io/revision"
	PodTemplateLabel   = "damonset.kubernetes.io/daemon-name"

	// RollbackRevisionNotFound is not found rollback event reason
	RollbackRevisionNotFound = "DaemonRollbackRevisionNotFound"
	// RollbackTemplateUnchanged is the template unchanged rollback event reason
	RollbackTemplateUnchanged = "DaemonRollbackTemplateUnchanged"
	// RollbackDone is the done rollback event reason
	RollbackDone = "DaemonSetRollback"
)

var annotationsToSkip = map[string]bool{
	RevisionAnnotation:             true,
	DaemonPausedAnnotation:         true,
	DaemonRollbackToAnnotation:     true,
	DaemonStrategyTypeAnnotation:   true,
	DaemonMaxUnavailableAnnotation: true,
}

func skipCopyAnnotation(key string) bool {
	return annotationsToSkip[key]
}

type podTemplateListFunc func(string, api.ListOptions) (*api.PodTemplateList, error)

func GetOrCreatePodTemplate(ptc PodTemplateControllerInterface, ds *extensions.DaemonSet, c clientset.Interface) (*api.PodTemplate, error) {
	var podTemplate *api.PodTemplate

	podTemplates, err := ListPodTemplates(ds, c)
	if err != nil {
		return nil, err
	}
	if len(podTemplates.Items) == 0 {
		podTemplate, err = CreatePodTemplateFromDS(ptc, ds, "1")
		if err != nil {
			return nil, err
		}
	} else {
		podTmpl, err := getNewPodTemplate(ds, podTemplates)
		if err != nil {
			return nil, err
		}
		if podTmpl != nil {
			return podTmpl, nil
		}
		revision := MaxRevision(podTemplates)
		newRevision := strconv.FormatInt(revision+1, 10)
		podTemplate, err = CreatePodTemplateFromDS(ptc, ds, newRevision)
		if err != nil {
			return nil, err
		}
	}
	return podTemplate, nil
}

func CreatePodTemplateFromDS(ptc PodTemplateControllerInterface, ds *extensions.DaemonSet, revision string) (*api.PodTemplate, error) {
	obj, _ := api.Scheme.DeepCopy(ds.Spec.Template)
	template := obj.(api.PodTemplateSpec)
	template.ObjectMeta.Labels = labelsutil.CloneAndRemoveLabel(
		ds.Spec.Template.ObjectMeta.Labels,
		extensions.DefaultDaemonSetUniqueLabelKey,
	)
	namespace := ds.ObjectMeta.Namespace
	podTemplateSpecHash := podutil.GetPodTemplateSpecHash(template)
	// TODO: copy annotations from DaemonSet to podtemplate

	template.ObjectMeta.Labels = labelsutil.CloneAndAddLabel(
		ds.Spec.Template.ObjectMeta.Labels,
		extensions.DefaultDaemonSetUniqueLabelKey,
		podTemplateSpecHash,
	)

	newPodTemplate := api.PodTemplate{
		ObjectMeta: api.ObjectMeta{
			Name:        ds.Name + "-" + fmt.Sprintf("%d", podTemplateSpecHash),
			Namespace:   namespace,
			Annotations: map[string]string{},
		},
		Template: template,
	}
	newPodTemplate.ObjectMeta.Labels = labelsutil.CloneAndAddLabel(
		ds.Spec.Template.ObjectMeta.Labels,
		extensions.DefaultDaemonSetUniqueLabelKey,
		podTemplateSpecHash,
	)
	newPodTemplate.ObjectMeta.Annotations[RevisionAnnotation] = revision
	createdPodTemplate, err := ptc.CreatePodTemplate(&newPodTemplate, namespace)
	if err != nil {
		return nil, fmt.Errorf("error creating pod template for DaemonSet %v: %v", ds.Name, err)
	}
	return createdPodTemplate, nil
}

// MaxRevision finds the highest revision in the pod templates
func MaxRevision(templates *api.PodTemplateList) int64 {
	max := int64(0)
	for _, template := range templates.Items {
		if v, err := Revision(&template); err != nil {
			// Skip the PodTemplate  when it failed to parse their revision information
			glog.V(4).Infof("Error: %v. Couldn't parse revision for PodTemplate %#v, daemonset controller will skip it when reconciling revisions.", err, template)
		} else if v > max {
			max = v
		}
	}
	return max
}

// Revision returns the revision number of the input podTemplate
func Revision(template *api.PodTemplate) (int64, error) {
	v, ok := template.ObjectMeta.Annotations[RevisionAnnotation]
	if !ok {
		return 0, fmt.Errorf("Missing revision annotation in PodTemplate: ", template.Name)
	}
	return strconv.ParseInt(v, 10, 64)
}

// LastRevision finds the second max revision number in all PodTemplates (the last revision)
func LastRevision(podTemplates *api.PodTemplateList) int64 {
	max, secMax := int64(0), int64(0)
	for _, template := range podTemplates.Items {
		if v, err := Revision(&template); err != nil {
			// Skip the pod templates when it failed to parse their revision information
			glog.V(4).Infof("Error: %v. Couldn't parse revision for pod template %#v, daemon controller will skip it when reconciling revisions.", err, template)
		} else if v >= max {
			secMax = max
			max = v
		} else if v > secMax {
			secMax = v
		}
	}
	return secMax
}

// ListPodTemplates returns a list of PodTemplates the given daemon targets.
func ListPodTemplates(ds *extensions.DaemonSet, c clientset.Interface) (*api.PodTemplateList, error) {
	namespace := ds.Namespace
	selector, err := unversioned.LabelSelectorAsSelector(ds.Spec.Selector)
	if err != nil {
		return nil, err
	}
	options := api.ListOptions{LabelSelector: selector}
	return c.Core().PodTemplates(namespace).List(options)
}

func GetAllPodTemplates(daemon *extensions.DaemonSet, c clientset.Interface) (*api.PodTemplateList, error) {
	return ListPodTemplates(daemon, c)
}

// SetFromPodTemplate sets the desired PodTemplateSpec from a PodTemplate template to the given deployment.
func SetFromPodTemplate(daemon *extensions.DaemonSet, podTemplate *api.PodTemplate) *extensions.DaemonSet {
	daemon.Spec.Template.ObjectMeta = podTemplate.Template.ObjectMeta
	daemon.Spec.Template.Spec = podTemplate.Template.Spec
	daemon.Spec.Template.ObjectMeta.Labels = labelsutil.CloneAndRemoveLabel(
		daemon.Spec.Template.ObjectMeta.Labels,
		extensions.DefaultDaemonSetUniqueLabelKey)
	return daemon
}

// SetDaemonSetAnnotationsTo sets daemon set's annotations as given PodTemplate's annotations.
// This action should be done if and only if the daemon set is rolling back to this PodTemplate.
// Note that apply and revision annotations are not changed.
func SetDaemonSetAnnotationsTo(daemon *extensions.DaemonSet, rollbackToPT *api.PodTemplate) {
	daemon.Annotations = getSkippedAnnotations(daemon.Annotations)
	for k, v := range rollbackToPT.Annotations {
		if !skipCopyAnnotation(k) {
			daemon.Annotations[k] = v
		}
	}
}

func getSkippedAnnotations(annotations map[string]string) map[string]string {
	skippedAnnotations := make(map[string]string)
	for k, v := range annotations {
		if skipCopyAnnotation(k) {
			skippedAnnotations[k] = v
		}
	}
	return skippedAnnotations
}

func getNewPodTemplate(ds *extensions.DaemonSet, podTemplates *api.PodTemplateList) (*api.PodTemplate, error) {
	newDSTemplate := getNewDaemonSetTemplate(ds)
	for _, podTemplate := range podTemplates.Items {
		equal, err := equalIgnoreHash(podTemplate.Template, newDSTemplate)
		if err != nil {
			return nil, err
		}
		if equal {
			return &podTemplate, nil
		}
	}
	return nil, nil
}

func getNewDaemonSetTemplate(ds *extensions.DaemonSet) api.PodTemplateSpec {
	newDaemonSetTemplate := api.PodTemplateSpec{
		ObjectMeta: ds.Spec.Template.ObjectMeta,
		Spec:       ds.Spec.Template.Spec,
	}
	newDaemonSetTemplate.ObjectMeta.Labels = labelsutil.CloneAndAddLabel(
		ds.Spec.Template.ObjectMeta.Labels,
		extensions.DefaultDaemonSetUniqueLabelKey,
		podutil.GetPodTemplateSpecHash(newDaemonSetTemplate))
	return newDaemonSetTemplate
}

// This is a copy of https://github.com/kubernetes/kubernetes/blob/master/pkg/controller/deployment/util/deployment_util.go#L594
// equalIgnoreHash returns true if two given podTemplateSpec are equal, ignoring the diff in value of Labels[pod-template-hash]
// We ignore pod-template-hash because the hash result would be different upon podTemplateSpec API changes
// (e.g. the addition of a new field will cause the hash code to change)
// Note that we assume input podTemplateSpecs contain non-empty labels
func equalIgnoreHash(template1, template2 api.PodTemplateSpec) (bool, error) {
	// First, compare template.Labels (ignoring hash)
	labels1, labels2 := template1.Labels, template2.Labels
	// The podTemplateSpec must have a non-empty label so that label selectors can find them.
	// This is checked by validation (of resources contain a podTemplateSpec).
	if len(labels1) == 0 || len(labels2) == 0 {
		return false, fmt.Errorf("Unexpected empty labels found in given template")
	}
	if len(labels1) > len(labels2) {
		labels1, labels2 = labels2, labels1
	}
	// We make sure len(labels2) >= len(labels1)
	for k, v := range labels2 {
		if labels1[k] != v && k != extensions.DefaultDaemonSetUniqueLabelKey {
			return false, nil
		}
	}

	// Then, compare the templates without comparing their labels
	template1.Labels, template2.Labels = nil, nil
	result := api.Semantic.DeepEqual(template1, template2)
	return result, nil
}

// TODO: Should I make it a real controller?
type PodTemplateControllerInterface interface {
	CreatePodTemplate(podTemplate *api.PodTemplate, namespace string) (*api.PodTemplate, error)
	DeletePodTemplate(namespace string, podTemplateID string) error
}

type PodTemplateController struct {
	KubeClient clientset.Interface
}

func (ptc *PodTemplateController) CreatePodTemplate(podTemplate *api.PodTemplate, namespace string) (*api.PodTemplate, error) {
	return ptc.KubeClient.Core().PodTemplates(namespace).Create(podTemplate)
}

func (ptc *PodTemplateController) DeletePodTemplate(namespace string, podTemplateID string) error {
	options := api.DeleteOptions{}
	return ptc.KubeClient.Core().PodTemplates(namespace).Delete(podTemplateID, &options)
}
