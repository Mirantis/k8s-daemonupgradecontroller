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
	"reflect"
	"sort"
	"sync"
	"time"

	daemonutil "github.com/Mirantis/k8s-daemonupgradecontroller/pkg/controller/daemon/util"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/apis/extensions"
	"k8s.io/kubernetes/pkg/client/cache"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	unversionedcore "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset/typed/core/internalversion"
	"k8s.io/kubernetes/pkg/client/record"
	"k8s.io/kubernetes/pkg/controller"
	"k8s.io/kubernetes/pkg/controller/informers"
	"k8s.io/kubernetes/pkg/labels"
	utilerrors "k8s.io/kubernetes/pkg/util/errors"
	"k8s.io/kubernetes/pkg/util/metrics"
	utilruntime "k8s.io/kubernetes/pkg/util/runtime"
	"k8s.io/kubernetes/pkg/util/wait"
	"k8s.io/kubernetes/pkg/util/workqueue"

	"github.com/golang/glog"
)

const (
	// Daemon sets will periodically check that their daemon pods are running as expected.
	FullDaemonSetResyncPeriod = 30 * time.Second // TODO: Figure out if this time seems reasonable.

	// Realistic value of the burstReplica field for the replication manager based off
	// performance requirements for kubernetes 1.0.
	BurstReplicas = 500
)

// DaemonUpgradeController is responsible for synchronizing DaemonSet objects stored
// in the system with actual running pods.
type DaemonUpgradeController struct {
	kubeClient    clientset.Interface
	eventRecorder record.EventRecorder
	podControl    controller.PodControlInterface

	// An dsc is temporarily suspended after creating/deleting these many replicas.
	// It resumes normal action after observing the watch events for them.
	burstReplicas int

	// To allow injection of syncDaemonSet for testing.
	syncHandler func(dsKey string) error
	// A TTLCache of pod creates/deletes each ds expects to see
	expectations controller.ControllerExpectationsInterface
	// A store of daemon sets
	dsStore *cache.StoreToDaemonSetLister
	// A store of pods
	podStore *cache.StoreToPodLister
	// A store of nodes
	nodeStore *cache.StoreToNodeLister
	// podStoreSynced returns true if the pod store has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	podStoreSynced cache.InformerSynced
	// nodeStoreSynced returns true if the node store has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	nodeStoreSynced cache.InformerSynced

	// To allow injection of CreatePodTemplate for testing
	podTemplateController daemonutil.PodTemplateControllerInterface

	lookupCache *controller.MatchingCache

	// DaemonSet keys that need to be synced.
	queue workqueue.RateLimitingInterface
}

func NewDaemonUpgradeController(daemonSetInformer informers.DaemonSetInformer, podInformer informers.PodInformer, nodeInformer informers.NodeInformer, kubeClient clientset.Interface, lookupCacheSize int) *DaemonUpgradeController {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.Infof)
	// TODO: remove the wrapper when every clients have moved to use the clientset.
	eventBroadcaster.StartRecordingToSink(&unversionedcore.EventSinkImpl{Interface: kubeClient.Core().Events("")})

	if kubeClient != nil && kubeClient.Core().RESTClient().GetRateLimiter() != nil {
		metrics.RegisterMetricAndTrackRateLimiterUsage("daemonupdater_controller", kubeClient.Core().RESTClient().GetRateLimiter())
	}
	dsc := &DaemonUpgradeController{
		kubeClient:    kubeClient,
		eventRecorder: eventBroadcaster.NewRecorder(api.EventSource{Component: "daemonupdater-controller"}),
		podControl: controller.RealPodControl{
			KubeClient: kubeClient,
			Recorder:   eventBroadcaster.NewRecorder(api.EventSource{Component: "daemonupdater-set"}),
		},
		podTemplateController: &daemonutil.PodTemplateController{
			KubeClient: kubeClient,
		},
		burstReplicas: BurstReplicas,
		expectations:  controller.NewControllerExpectations(),
		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "daemonset"),
	}

	daemonSetInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ds := obj.(*extensions.DaemonSet)
			glog.V(4).Infof("Adding daemon set %s", ds.Name)
			dsc.enqueueDaemonSet(ds)
		},
		UpdateFunc: func(old, cur interface{}) {
			oldDS := old.(*extensions.DaemonSet)
			curDS := cur.(*extensions.DaemonSet)
			// We should invalidate the whole lookup cache if a DS's selector has been updated.
			//
			// Imagine that you have two RSs:
			// * old DS1
			// * new DS2
			// You also have a pod that is attached to DS2 (because it doesn't match DS1 selector).
			// Now imagine that you are changing DS1 selector so that it is now matching that pod,
			// in such case we must invalidate the whole cache so that pod could be adopted by DS1
			//
			// This makes the lookup cache less helpful, but selector update does not happen often,
			// so it's not a big problem
			if !reflect.DeepEqual(oldDS.Spec.Selector, curDS.Spec.Selector) {
				dsc.lookupCache.InvalidateAll()
			}

			glog.V(4).Infof("Updating daemon set %s", oldDS.Name)
			dsc.enqueueDaemonSet(curDS)
		},
		DeleteFunc: dsc.deleteDaemonset,
	})
	dsc.dsStore = daemonSetInformer.Lister()

	// Watch for creation/deletion of pods. The reason we watch is that we don't want a daemon set to create/delete
	// more pods until all the effects (expectations) of a daemon set's create/delete have been observed.
	podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    dsc.addPod,
		UpdateFunc: dsc.updatePod,
		DeleteFunc: dsc.deletePod,
	})
	dsc.podStore = podInformer.Lister()
	dsc.podStoreSynced = podInformer.Informer().HasSynced

	dsc.nodeStoreSynced = nodeInformer.Informer().HasSynced
	dsc.nodeStore = nodeInformer.Lister()

	dsc.syncHandler = dsc.syncDaemonSet
	dsc.lookupCache = controller.NewMatchingCache(lookupCacheSize)
	return dsc
}

func (dsc *DaemonUpgradeController) deleteDaemonset(obj interface{}) {
	//TODO: delete pod templates?
}

// Run begins watching and syncing daemon sets.
func (dsc *DaemonUpgradeController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer dsc.queue.ShutDown()

	glog.Infof("Starting Daemon Sets controller manager")

	if !cache.WaitForCacheSync(stopCh, dsc.podStoreSynced, dsc.nodeStoreSynced) {
		return
	}

	for i := 0; i < workers; i++ {
		go wait.Until(dsc.runWorker, time.Second, stopCh)
	}

	<-stopCh
	glog.Infof("Shutting down Daemon Set Controller")
}

func (dsc *DaemonUpgradeController) runWorker() {
	glog.Infof("Starting Daemon Sets controller worker")
	for dsc.processNextWorkItem() {
	}
}

// processNextWorkItem deals with one key off the queue.  It returns false when it's time to quit.
func (dsc *DaemonUpgradeController) processNextWorkItem() bool {
	dsKey, quit := dsc.queue.Get()
	if quit {
		return false
	}
	defer dsc.queue.Done(dsKey)

	err := dsc.syncHandler(dsKey.(string))
	if err == nil {
		dsc.queue.Forget(dsKey)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("%v failed with : %v", dsKey, err))
	dsc.queue.AddRateLimited(dsKey)

	return true
}

func (dsc *DaemonUpgradeController) enqueueDaemonSet(ds *extensions.DaemonSet) {
	key, err := controller.KeyFunc(ds)
	if err != nil {
		glog.Errorf("Couldn't get key for object %#v: %v", ds, err)
		return
	}

	// TODO: Handle overlapping controllers better. See comment in ReplicationManager.
	dsc.queue.Add(key)
}

func (dsc *DaemonUpgradeController) getPodDaemonSet(pod *api.Pod) *extensions.DaemonSet {
	// look up in the cache, if cached and the cache is valid, just return cached value
	if obj, cached := dsc.lookupCache.GetMatchingObject(pod); cached {
		ds, ok := obj.(*extensions.DaemonSet)
		if !ok {
			// This should not happen
			glog.Errorf("lookup cache does not retuen a ReplicationController object")
			return nil
		}
		if dsc.isCacheValid(pod, ds) {
			return ds
		}
	}
	sets, err := dsc.dsStore.GetPodDaemonSets(pod)
	if err != nil {
		glog.V(4).Infof("No daemon sets found for pod %v, daemon set controller will avoid syncing", pod.Name)
		return nil
	}
	if len(sets) > 1 {
		// More than two items in this list indicates user error. If two daemon
		// sets overlap, sort by creation timestamp, subsort by name, then pick
		// the first.
		glog.Errorf("user error! more than one daemon is selecting pods with labels: %+v", pod.Labels)
		sort.Sort(byCreationTimestamp(sets))
	}

	// update lookup cache
	dsc.lookupCache.Update(pod, &sets[0])

	return &sets[0]
}

// isCacheValid check if the cache is valid
func (dsc *DaemonUpgradeController) isCacheValid(pod *api.Pod, cachedDS *extensions.DaemonSet) bool {
	_, exists, err := dsc.dsStore.Get(cachedDS)
	// ds has been deleted or updated, cache is invalid
	if err != nil || !exists || !isDaemonSetMatch(pod, cachedDS) {
		return false
	}
	return true
}

// isDaemonSetMatch take a Pod and DaemonSet, return whether the Pod and DaemonSet are matching
// TODO(mqliang): This logic is a copy from GetPodDaemonSets(), remove the duplication
func isDaemonSetMatch(pod *api.Pod, ds *extensions.DaemonSet) bool {
	if ds.Namespace != pod.Namespace {
		return false
	}
	selector, err := unversioned.LabelSelectorAsSelector(ds.Spec.Selector)
	if err != nil {
		err = fmt.Errorf("invalid selector: %v", err)
		return false
	}

	// If a ReplicaSet with a nil or empty selector creeps in, it should match nothing, not everything.
	if selector.Empty() || !selector.Matches(labels.Set(pod.Labels)) {
		return false
	}
	return true
}

func (dsc *DaemonUpgradeController) addPod(obj interface{}) {
	pod := obj.(*api.Pod)
	glog.V(4).Infof("Pod %s added.", pod.Name)
	if ds := dsc.getPodDaemonSet(pod); ds != nil {
		dsKey, err := controller.KeyFunc(ds)
		if err != nil {
			glog.Errorf("Couldn't get key for object %#v: %v", ds, err)
			return
		}
		dsc.expectations.CreationObserved(dsKey)
		dsc.enqueueDaemonSet(ds)
	}
}

// When a pod is updated, figure out what sets manage it and wake them
// up. If the labels of the pod have changed we need to awaken both the old
// and new set. old and cur must be *api.Pod types.
func (dsc *DaemonUpgradeController) updatePod(old, cur interface{}) {
}

func (dsc *DaemonUpgradeController) deletePod(obj interface{}) {
	pod, ok := obj.(*api.Pod)
	// When a delete is dropped, the relist will notice a pod in the store not
	// in the list, leading to the insertion of a tombstone object which contains
	// the deleted key/value. Note that this value might be stale. If the pod
	// changed labels the new daemonset will not be woken up till the periodic
	// resync.
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			glog.Errorf("Couldn't get object from tombstone %#v", obj)
			return
		}
		pod, ok = tombstone.Obj.(*api.Pod)
		if !ok {
			glog.Errorf("Tombstone contained object that is not a pod %#v", obj)
			return
		}
	}
	glog.V(4).Infof("Pod %s deleted.", pod.Name)
	if ds := dsc.getPodDaemonSet(pod); ds != nil {
		dsKey, err := controller.KeyFunc(ds)
		if err != nil {
			glog.Errorf("Couldn't get key for object %#v: %v", ds, err)
			return
		}
		dsc.expectations.DeletionObserved(dsKey)
		dsc.enqueueDaemonSet(ds)
	}
}

// getNodesToDaemonSetPods returns a map from nodes to daemon pods (corresponding to ds) running on the nodes.
func (dsc *DaemonUpgradeController) getNodesToDaemonPods(ds *extensions.DaemonSet) (map[string][]*api.Pod, error) {
	nodeToDaemonPods := make(map[string][]*api.Pod)
	selector, err := unversioned.LabelSelectorAsSelector(ds.Spec.Selector)
	if err != nil {
		return nil, err
	}
	daemonPods, err := dsc.podStore.Pods(ds.Namespace).List(selector)
	if err != nil {
		return nodeToDaemonPods, err
	}
	for i := range daemonPods {
		// TODO: Do we need to copy here?
		daemonPod := &(*daemonPods[i])
		nodeName := daemonPod.Spec.NodeName
		nodeToDaemonPods[nodeName] = append(nodeToDaemonPods[nodeName], daemonPod)
	}
	return nodeToDaemonPods, nil
}

func (dsc *DaemonUpgradeController) manage(ds *extensions.DaemonSet) error {
	// Find out which nodes are running the daemon pods selected by ds.
	nodeToDaemonPods, err := dsc.getNodesToDaemonPods(ds)
	if err != nil {
		return fmt.Errorf("error getting node to daemon pod mapping for daemon set %#v: %v", ds, err)
	}

	nodeList, err := dsc.nodeStore.List()
	if err != nil {
		return fmt.Errorf("couldn't get list of nodes when syncing daemon set %#v: %v", ds, err)
	}

	podTemplate, err := daemonutil.GetOrCreatePodTemplate(dsc.podTemplateController, ds, dsc.kubeClient)
	if err != nil {
		glog.Errorf("Couldn't get PodTemplate for daemon set %s: %v", ds.Name, err)
		return err
	}

	err = daemonutil.UpdateDsSpecHashLabel(dsc.kubeClient, ds, podTemplate)
	if err != nil {
		glog.Errorf("Couldn't update daemon set %s pod template spec hash label: %v", ds.Name, err)
		return err
	}

	selector, err := unversioned.LabelSelectorAsSelector(ds.Spec.Selector)
	if err != nil {
		return err
	}
	daemonPods, err := dsc.podStore.Pods(ds.Namespace).List(selector)
	if err != nil {
		return err
	}
	err = daemonutil.UpdatePodsTemplateHashLabel(dsc.kubeClient, ds, daemonPods, podTemplate)
	if err != nil {
		glog.Errorf("Couldn't update daemon set %s pods template hash label: %v", ds.Name, err)
		return err
	}

	var podsToDelete []string
	numUnavailable := 0
	for _, node := range nodeList.Items {
		daemonPods, isRunning := nodeToDaemonPods[node.Name]
		if isRunning {
			sort.Sort(podByCreationTimestamp(daemonPods))
			_, needsUpdate := dsc.needsUpdate(ds, daemonPods, numUnavailable, len(nodeList.Items))

			if needsUpdate {
				// If the daemon pods on this node needs update, delete all the daemon pods
				// running on this node
				for i := range daemonPods {
					podsToDelete = append(podsToDelete, daemonPods[i].Name)
				}
				numUnavailable++
			}
		}
	}

	// We need to set expectations before deleting pods to avoid race conditions.
	dsKey, err := controller.KeyFunc(ds)
	if err != nil {
		return fmt.Errorf("couldn't get key for object %#v: %v", ds, err)
	}

	deleteDiff := len(podsToDelete)

	if deleteDiff > dsc.burstReplicas {
		deleteDiff = dsc.burstReplicas
	}

	// dsc.expectations.SetExpectations(dsKey, createDiff, deleteDiff)
	dsc.expectations.SetExpectations(dsKey, 0, deleteDiff)

	// error channel to communicate back failures.  make the buffer big enough to avoid any blocking
	errCh := make(chan error, deleteDiff)

	//glog.V(4).Infof("Nodes needing daemon pods for daemon set %s: %+v, creating %d", ds.Name, nodesNeedingDaemonPods, createDiff)
	//	template := podtemplate.Template
	//	createWait := sync.WaitGroup{}
	//	createWait.Add(createDiff)
	//	for i := 0; i < createDiff; i++ {
	//		go func(ix int) {
	//			defer createWait.Done()
	//
	//			if err := dsc.podControl.CreatePodsOnNode(nodesNeedingDaemonPods[ix], ds.Namespace, &template, ds); err != nil {
	//				glog.V(2).Infof("Failed creation, decrementing expectations for set %q/%q", ds.Namespace, ds.Name)
	//				dsc.expectations.CreationObserved(dsKey)
	//				errCh <- err
	//				utilruntime.HandleError(err)
	//			}
	//		}(i)
	//
	//	}
	//	createWait.Wait()

	glog.V(4).Infof("Pods to delete for daemon set %s: %+v, deleting %d", ds.Name, podsToDelete, deleteDiff)
	deleteWait := sync.WaitGroup{}
	deleteWait.Add(deleteDiff)
	for i := 0; i < deleteDiff; i++ {
		go func(ix int) {
			defer deleteWait.Done()
			if err := dsc.podControl.DeletePod(ds.Namespace, podsToDelete[ix], ds); err != nil {
				glog.V(2).Infof("Failed deletion, decrementing expectations for set %q/%q", ds.Namespace, ds.Name)
				dsc.expectations.DeletionObserved(dsKey)
				errCh <- err
				utilruntime.HandleError(err)
			}
		}(i)
	}
	deleteWait.Wait()

	// collect errors if any for proper reporting/retry logic in the controller
	errors := []error{}
	close(errCh)
	for err := range errCh {
		errors = append(errors, err)
	}
	return utilerrors.NewAggregate(errors)
}

func (dsc *DaemonUpgradeController) syncDaemonSet(key string) error {
	startTime := time.Now()
	defer func() {
		glog.V(4).Infof("Finished syncing daemon set %q (%v)", key, time.Now().Sub(startTime))
	}()

	obj, exists, err := dsc.dsStore.Store.GetByKey(key)
	if err != nil {
		return fmt.Errorf("unable to retrieve ds %v from store: %v", key, err)
	}
	if !exists {
		glog.V(3).Infof("daemon set has been deleted %v", key)
		dsc.expectations.DeleteExpectations(key)
		return nil
	}
	ds := obj.(*extensions.DaemonSet)

	// Don't process a daemon set until all its creations and deletions have been processed.
	// For example if daemon set foo asked for 3 new daemon pods in the previous call to manage,
	// then we do not want to call manage on foo until the daemon pods have been created.
	dsKey, err := controller.KeyFunc(ds)
	if err != nil {
		return fmt.Errorf("couldn't get key for object %#v: %v", ds, err)
	}

	// paused := dsc.isPaused(ds)
	paused := daemonutil.IsPaused(ds)
	// rollbackTo := dsc.getRollbackTo(dsc)
	rollbackTo := daemonutil.GetRollbackTo(ds)

	// if !paused && ds.Spec.RollbackTo != nil {
	if !paused && rollbackTo != nil {
		// revision := ds.Spec.RollbackTo.Revision
		revision := rollbackTo.Revision
		if ds, err = dsc.rollback(ds, &revision); err != nil {
			return err
		}
	}

	dsNeedsSync := dsc.expectations.SatisfiedExpectations(dsKey)
	if dsNeedsSync && ds.DeletionTimestamp == nil {
		if err := dsc.manage(ds); err != nil {
			return err
		}
	}

	return nil
}

// byCreationTimestamp sorts a list by creation timestamp, using their names as a tie breaker.
type byCreationTimestamp []extensions.DaemonSet

func (o byCreationTimestamp) Len() int      { return len(o) }
func (o byCreationTimestamp) Swap(i, j int) { o[i], o[j] = o[j], o[i] }

func (o byCreationTimestamp) Less(i, j int) bool {
	if o[i].CreationTimestamp.Equal(o[j].CreationTimestamp) {
		return o[i].Name < o[j].Name
	}
	return o[i].CreationTimestamp.Before(o[j].CreationTimestamp)
}

type podByCreationTimestamp []*api.Pod

func (o podByCreationTimestamp) Len() int      { return len(o) }
func (o podByCreationTimestamp) Swap(i, j int) { o[i], o[j] = o[j], o[i] }

func (o podByCreationTimestamp) Less(i, j int) bool {
	if o[i].CreationTimestamp.Equal(o[j].CreationTimestamp) {
		return o[i].Name < o[j].Name
	}
	return o[i].CreationTimestamp.Before(o[j].CreationTimestamp)
}
