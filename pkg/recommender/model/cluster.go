/*
Copyright 2017 The Kubernetes Authors.

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

package model

import (
	"fmt"
	"time"

	apiv1 "k8s.io/api/core/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	vpa_types "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	vpa_utils "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/utils/vpa"
	"k8s.io/klog"
)

const (
	// RecommendationMissingMaxDuration is maximum time that we accept the recommendation can be missing.
	RecommendationMissingMaxDuration = 30 * time.Minute
)

// ClusterState holds all runtime information about the cluster required for the
// VPA operations, i.e. configuration of resources (pods, containers,
// VPA objects), aggregated utilization of compute resources (CPU, memory) and
// events (container OOMs).
// All input to the VPA Recommender algorithm lives in this structure.
type ClusterState struct {
	// Pods in the cluster.
	Pods map[PodID]*PodState
	// VPA objects in the cluster.
	Vpas map[VpaID]*Vpa
	// VPA objects in the cluster that have no recommendation mapped to the first
	// time we've noticed the recommendation missing or last time we logged
	// a warning about it.
	EmptyVPAs map[VpaID]time.Time
	// Observed VPAs. Used to check if there are updates needed.
	ObservedVpas []*vpa_types.VerticalPodAutoscaler

	// All container aggregations where the usage samples are stored.
	aggregateStateMap aggregateContainerStatesMap
	// Map with all label sets used by the aggregations. It serves as a cache
	// that allows to quickly access labels.Set corresponding to a labelSetKey.
	labelSetMap labelSetMap
}

// StateMapSize is the number of pods being tracked by the VPA
func (cluster *ClusterState) StateMapSize() int {
	return len(cluster.aggregateStateMap)
}

// AggregateStateKey determines the set of containers for which the usage samples
// are kept aggregated in the model.
type AggregateStateKey interface {
	Namespace() string
	ContainerName() string
	Labels() labels.Labels
}

// String representation of the labels.LabelSet. This is the value returned by
// labelSet.String(). As opposed to the LabelSet object, it can be used as a map key.
type labelSetKey string

// Map of label sets keyed by their string representation.
type labelSetMap map[labelSetKey]labels.Set

// AggregateContainerStatesMap is a map from AggregateStateKey to AggregateContainerState.
type aggregateContainerStatesMap map[AggregateStateKey]*AggregateContainerState

// PodState holds runtime information about a single Pod.
type PodState struct {
	// Unique id of the Pod.
	ID PodID
	// Set of labels attached to the Pod.
	labelSetKey labelSetKey
	// Containers that belong to the Pod, keyed by the container name.
	Containers map[string]*ContainerState
	// PodPhase describing current life cycle phase of the Pod.
	Phase apiv1.PodPhase
}

// NewClusterState returns a new ClusterState with no pods.
func NewClusterState() *ClusterState {
	return &ClusterState{
		Pods:              make(map[PodID]*PodState),
		Vpas:              make(map[VpaID]*Vpa),
		EmptyVPAs:         make(map[VpaID]time.Time),
		aggregateStateMap: make(aggregateContainerStatesMap),
		labelSetMap:       make(labelSetMap),
	}
}

// ContainerUsageSampleWithKey holds a ContainerUsageSample together with the
// ID of the container it belongs to.
type ContainerUsageSampleWithKey struct {
	ContainerUsageSample
	Container    ContainerID
	RestartCount int32
}

// AddOrUpdatePod updates the state of the pod with a given PodID, if it is
// present in the cluster object. Otherwise a new pod is created and added to
// the Cluster object.
// If the labels of the pod have changed, it updates the links between the containers
// and the aggregations.
func (cluster *ClusterState) AddOrUpdatePod(PodID PodID, newLabels labels.Set, phase apiv1.PodPhase) {
	pod, podExists := cluster.Pods[PodID]
	if !podExists {
		pod = newPod(PodID)
		cluster.Pods[PodID] = pod
	}

	newlabelSetKey := cluster.getLabelSetKey(newLabels)
	if podExists && pod.labelSetKey != newlabelSetKey {
		//if pod.labelSetKey != newlabelSetKey {
		// This Pod is already counted in the old VPA, remove the link.
		cluster.removePodFromItsVpa(pod)
	}
	if !podExists || pod.labelSetKey != newlabelSetKey {
		pod.labelSetKey = newlabelSetKey
		// Set the links between the containers and aggregations based on the current pod labels.
		for containerName, container := range pod.Containers {
			containerID := ContainerID{PodID: PodID, ContainerName: containerName}
			container.aggregator = cluster.findOrCreateAggregateContainerState(containerID)
		}

		cluster.addPodToItsVpa(pod)
	}
	pod.Phase = phase
}

// addPodToItsVpa increases the count of Pods associated with a VPA object.
// Does a scan similar to findOrCreateAggregateContainerState so could be optimized if needed.
func (cluster *ClusterState) addPodToItsVpa(pod *PodState) {
	for _, vpa := range cluster.Vpas {
		if vpa_utils.PodLabelsMatchVPA(pod.ID.Namespace, cluster.labelSetMap[pod.labelSetKey], vpa.ID.Namespace, vpa.PodSelector) {
			vpa.PodCount++
		}
	}
}

// removePodFromItsVpa decreases the count of Pods associated with a VPA object.
func (cluster *ClusterState) removePodFromItsVpa(pod *PodState) {
	for _, vpa := range cluster.Vpas {
		if vpa_utils.PodLabelsMatchVPA(pod.ID.Namespace, cluster.labelSetMap[pod.labelSetKey], vpa.ID.Namespace, vpa.PodSelector) {
			vpa.PodCount--
		}
	}
}

// GetContainer returns the ContainerState object for a given ContainerID or
// null if it's not present in the model.
func (cluster *ClusterState) GetContainer(containerID ContainerID) *ContainerState {
	pod, podExists := cluster.Pods[containerID.PodID]
	if podExists {
		container, containerExists := pod.Containers[containerID.ContainerName]
		if containerExists {
			return container
		}
	}
	return nil
}

// DeletePod removes an existing pod from the cluster.
func (cluster *ClusterState) DeletePod(PodID PodID) {
	pod, found := cluster.Pods[PodID]
	if found {
		cluster.removePodFromItsVpa(pod)
	}
	delete(cluster.Pods, PodID)
}

// AddOrUpdateContainer creates a new container with the given ContainerID and
// adds it to the parent pod in the ClusterState object, if not yet present.
// Requires the pod to be added to the ClusterState first. Otherwise an error is
// returned.
func (cluster *ClusterState) AddOrUpdateContainer(containerID ContainerID, request Resources, restartCount int, ready bool, curState ContainerCurrentState) error {
	pod, podExists := cluster.Pods[containerID.PodID]
	if !podExists {
		return NewKeyError(containerID.PodID)
	}

	acs := cluster.findOrCreateAggregateContainerState(containerID)
	if container, containerExists := pod.Containers[containerID.ContainerName]; !containerExists {
		pod.Containers[containerID.ContainerName] = NewContainerState(request, restartCount, NewContainerStateAggregatorProxy(cluster, containerID))
		restartBudget := acs.RestartBudget - (restartCount - acs.LastSeenRestartCount)
		pod.Containers[containerID.ContainerName].RecordRestartCount(restartCount, restartBudget)
		pod.Containers[containerID.ContainerName].aggregator.SetCurrentContainerState(curState)
		if acs.CurrentCtrCPUUsage.Request == 0 {
			acs.CurrentCtrCPUUsage.Request = request[ResourceCPU]
		}
		if acs.CurrentCtrMemUsage.Request == 0 {
			acs.CurrentCtrMemUsage.Request = request[ResourceMemory]
		}
	} else {
		// Container aleady exists. Possibly update the request.
		container.Request = request
		container.RecordRestartCount(restartCount, restartCount-acs.LastSeenRestartCount)
		container.aggregator.SetCurrentContainerState(curState)
	}

	return nil
}

// AddSample adds a new usage sample to the proper container in the ClusterState
// object. Requires the container as well as the parent pod to be added to the
// ClusterState first. Otherwise an error is returned.
func (cluster *ClusterState) AddSample(sample *ContainerUsageSampleWithKey) error {
	pod, podExists := cluster.Pods[sample.Container.PodID]

	if !podExists {
		return NewKeyError(sample.Container.PodID)
	}
	ContainerState, containerExists := pod.Containers[sample.Container.ContainerName]
	if !containerExists {
		return NewKeyError(sample.Container)
	}

	if !ContainerState.AddSample(&sample.ContainerUsageSample) {
		return fmt.Errorf("sample discarded (invalid or out of order)")
	}

	return nil
}

// RecordOOM adds info regarding OOM event in the model as an artificial memory sample.
func (cluster *ClusterState) RecordOOM(containerID ContainerID, timestamp time.Time, requestedMemory ResourceAmount) error {
	pod, podExists := cluster.Pods[containerID.PodID]
	if !podExists {
		return NewKeyError(containerID.PodID)
	}
	ContainerState, containerExists := pod.Containers[containerID.ContainerName]
	if !containerExists {
		return NewKeyError(containerID.ContainerName)
	}
	err := ContainerState.RecordOOM(timestamp, requestedMemory)
	if err != nil {
		return fmt.Errorf("error while recording OOM for %v, Reason: %v", containerID, err)
	}
	return nil
}

// AddOrUpdateVpa adds a new VPA with a given ID to the ClusterState if it
// didn't yet exist. If the VPA already existed but had a different pod
// selector, the pod selector is updated. Updates the links between the VPA and
// all aggregations it matches.
func (cluster *ClusterState) AddOrUpdateVpa(apiObject *vpa_types.VerticalPodAutoscaler, selector labels.Selector) error {
	VpaID := VpaID{Namespace: apiObject.Namespace, VpaName: apiObject.Name}
	annotationsMap := apiObject.Annotations
	conditionsMap := make(vpaConditionsMap)
	for _, condition := range apiObject.Status.Conditions {
		conditionsMap[condition.Type] = condition
	}
	var currentRecommendation *vpa_types.RecommendedPodResources
	if conditionsMap[vpa_types.RecommendationProvided].Status == apiv1.ConditionTrue {
		currentRecommendation = apiObject.Status.Recommendation
	}

	vpa, vpaExists := cluster.Vpas[VpaID]
	if vpaExists && (vpa.PodSelector.String() != selector.String()) {
		// Pod selector was changed. Delete the VPA object and recreate
		// it with the new selector.
		if err := cluster.DeleteVpa(VpaID); err != nil {
			return err
		}
		vpaExists = false
	}
	if !vpaExists {
		vpa = NewVpa(VpaID, selector, apiObject.CreationTimestamp.Time)
		cluster.Vpas[VpaID] = vpa
		for aggregationKey, aggregation := range cluster.aggregateStateMap {
			vpa.UseAggregationIfMatching(aggregationKey, aggregation)
		}
		vpa.PodCount = len(cluster.GetMatchingPods(vpa))
	}
	vpa.TargetRef = apiObject.Spec.TargetRef
	vpa.Annotations = annotationsMap
	vpa.Conditions = conditionsMap
	vpa.Recommendation = currentRecommendation
	vpa.SetUpdateMode(apiObject.Spec.UpdatePolicy)
	vpa.SetResourcePolicy(apiObject.Spec.ResourcePolicy)
	return nil
}

// DeleteVpa removes a VPA with the given ID from the ClusterState.
func (cluster *ClusterState) DeleteVpa(VpaID VpaID) error {
	vpa, vpaExists := cluster.Vpas[VpaID]
	if !vpaExists {
		return NewKeyError(VpaID)
	}
	for _, state := range vpa.aggregateContainerStates {
		state.MarkNotAutoscaled()
	}
	delete(cluster.Vpas, VpaID)
	delete(cluster.EmptyVPAs, VpaID)
	return nil
}

func newPod(id PodID) *PodState {
	return &PodState{
		ID:         id,
		Containers: make(map[string]*ContainerState),
	}
}

// getLabelSetKey puts the given labelSet in the global labelSet map and returns a
// corresponding labelSetKey.
func (cluster *ClusterState) getLabelSetKey(labelSet labels.Set) labelSetKey {
	labelSetKey := labelSetKey(labelSet.String())
	cluster.labelSetMap[labelSetKey] = labelSet
	return labelSetKey
}

// MakeAggregateStateKey returns the AggregateStateKey that should be used
// to aggregate usage samples from a container with the given name in a given pod.
func (cluster *ClusterState) MakeAggregateStateKey(pod *PodState, containerName string) AggregateStateKey {
	return aggregateStateKey{
		namespace:     pod.ID.Namespace,
		containerName: containerName,
		labelSetKey:   pod.labelSetKey,
		labelSetMap:   &cluster.labelSetMap,
	}
}

// aggregateStateKeyForContainerID returns the AggregateStateKey for the ContainerID.
// The pod with the corresponding PodID must already be present in the ClusterState.
func (cluster *ClusterState) aggregateStateKeyForContainerID(containerID ContainerID) AggregateStateKey {
	pod, podExists := cluster.Pods[containerID.PodID]

	if !podExists {
		panic(fmt.Sprintf("Pod not present or terminating in the ClusterState: %v", containerID.PodID))
	}

	// var aggregateStateKey AggregateStateKey
	// if pod.Phase == "Running" {
	// 	aggregateStateKey = cluster.MakeAggregateStateKey(pod, containerID.ContainerName)
	// }
	return cluster.MakeAggregateStateKey(pod, containerID.ContainerName)
}

// findOrCreateAggregateContainerState returns (possibly newly created) AggregateContainerState
// that should be used to aggregate usage samples from container with a given ID.
// The pod with the corresponding PodID must already be present in the ClusterState.
func (cluster *ClusterState) findOrCreateAggregateContainerState(containerID ContainerID) *AggregateContainerState {
	aggregateStateKey := cluster.aggregateStateKeyForContainerID(containerID)
	aggregateContainerState, aggregateStateExists := cluster.aggregateStateMap[aggregateStateKey]
	if !aggregateStateExists {
		aggregateContainerState = NewAggregateContainerState()
		cluster.aggregateStateMap[aggregateStateKey] = aggregateContainerState
		// Link the new aggregation to the existing VPAs.
		for _, vpa := range cluster.Vpas {
			vpa.UseAggregationIfMatching(aggregateStateKey, aggregateContainerState)
		}
	}

	return aggregateContainerState
}

// GarbageCollectAggregateCollectionStates removes obsolete
// AggregateCollectionStates from the ClusterState which are more than an hour old
func (cluster *ClusterState) GarbageCollectAggregateCollectionStates() {
	klog.V(1).Info("Garbage collection of AggregateCollectionStates triggered")
	keysToDelete := make([]AggregateStateKey, 0)
	activeKeys := cluster.getActiveAggregateStateKeys()
	for key, _ := range cluster.aggregateStateMap {
		isKeyActive := activeKeys[key]
		if !isKeyActive {
			keysToDelete = append(keysToDelete, key)
			klog.V(1).Infof("Removing expired AggregateCollectionState for %+v", key)
		}
	}
	for _, key := range keysToDelete {
		delete(cluster.aggregateStateMap, key)
		for _, vpa := range cluster.Vpas {
			vpa.DeleteAggregation(key)
		}
	}
}

func (cluster *ClusterState) getActiveAggregateStateKeys() map[AggregateStateKey]bool {
	activeKeys := map[AggregateStateKey]bool{}
	for _, pod := range cluster.Pods {
		// Pods that will not run anymore are considered inactive.
		if pod.Phase != apiv1.PodRunning || pod.Phase == apiv1.PodSucceeded || pod.Phase == apiv1.PodFailed {
			continue
		}
		for container := range pod.Containers {
			activeKeys[cluster.MakeAggregateStateKey(pod, container)] = true
		}
	}
	return activeKeys
}

// RecordRecommendation marks the state of recommendation in the cluster. We
// keep track of empty recommendations and log information about them
// periodically.
func (cluster *ClusterState) RecordRecommendation(vpa *Vpa, now time.Time) error {
	if vpa.Recommendation != nil && len(vpa.Recommendation.ContainerRecommendations) > 0 {
		delete(cluster.EmptyVPAs, vpa.ID)
		return nil
	}
	lastLogged, ok := cluster.EmptyVPAs[vpa.ID]
	if !ok {
		cluster.EmptyVPAs[vpa.ID] = now
	} else {
		if lastLogged.Add(RecommendationMissingMaxDuration).Before(now) {
			cluster.EmptyVPAs[vpa.ID] = now
			return fmt.Errorf("VPA %v/%v is missing recommendation for more than %v", vpa.ID.Namespace, vpa.ID.VpaName, RecommendationMissingMaxDuration)
		}
	}
	return nil
}

// GetMatchingPods returns a list of currently active pods that match the
// given VPA. Traverses through all pods in the cluster - use sparingly.
func (cluster *ClusterState) GetMatchingPods(vpa *Vpa) []PodID {
	matchingPods := []PodID{}
	for PodID, pod := range cluster.Pods {
		if vpa_utils.PodLabelsMatchVPA(PodID.Namespace, cluster.labelSetMap[pod.labelSetKey],
			vpa.ID.Namespace, vpa.PodSelector) {
			matchingPods = append(matchingPods, PodID)
		}
	}
	return matchingPods
}

// Implementation of the AggregateStateKey interface. It can be used as a map key.
type aggregateStateKey struct {
	namespace     string
	containerName string
	labelSetKey   labelSetKey
	// Pointer to the global map from labelSetKey to labels.Set.
	// Note: a pointer is used so that two copies of the same key are equal.
	labelSetMap *labelSetMap
}

// Labels returns the namespace for the aggregateStateKey.
func (k aggregateStateKey) Namespace() string {
	return k.namespace
}

// ContainerName returns the name of the container for the aggregateStateKey.
func (k aggregateStateKey) ContainerName() string {
	return k.containerName
}

// Labels returns the set of labels for the aggregateStateKey.
func (k aggregateStateKey) Labels() labels.Labels {
	if k.labelSetMap == nil {
		return labels.Set{}
	}
	return (*k.labelSetMap)[k.labelSetKey]
}
