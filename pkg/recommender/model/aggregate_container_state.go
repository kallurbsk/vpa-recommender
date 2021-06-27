/*
Copyright 2018 The Kubernetes Authors.

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

// VPA collects CPU and memory usage measurements from all containers running in
// the cluster and aggregates them in memory in structures called
// AggregateContainerState.
// During aggregation the usage samples are grouped together by the key called
// AggregateStateKey and stored in structures such as histograms of CPU and
// memory usage, that are parts of the AggregateContainerState.
//
// The AggregateStateKey consists of the container name, the namespace and the
// set of labels on the pod the container belongs to. In other words, whenever
// two samples come from containers with the same name, in the same namespace
// and with the same pod labels, they end up in the same histogram.
//
// Recall that VPA produces one recommendation for all containers with a given
// name and namespace, having pod labels that match a given selector. Therefore
// for each VPA object and container name the recommender has to take all
// matching AggregateContainerStates and further aggregate them together, in
// order to obtain the final aggregation that is the input to the recommender
// function.

package model

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	vpa_types "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
)

// ContainerNameToAggregateStateMap maps a container name to AggregateContainerState
// that aggregates state of containers with that name.
type ContainerNameToAggregateStateMap map[string]*AggregateContainerState

const (
	// SupportedCheckpointVersion is the tag of the supported version of serialized checkpoints.
	// Version id should be incremented on every non incompatible change, i.e. if the new
	// version of the recommender binary can't initialize from the old checkpoint format or the
	// previous version of the recommender binary can't initialize from the new checkpoint format.
	SupportedCheckpointVersion = "v3"
)

var (
	// DefaultControlledResources is a default value of Spec.ResourcePolicy.ContainerPolicies[].ControlledResources.
	DefaultControlledResources = []ResourceName{ResourceCPU, ResourceMemory}
)

// ContainerCurrentState container contains the Reason of the current container status
// It is filled if the container is stuck in "Waiting" or "Terminating" state
type ContainerCurrentState struct {
	Reason string
}

// ContainerStateAggregator is an interface for objects that consume and
// aggregate container usage samples.
type ContainerStateAggregator interface {
	// AddSample aggregates a single usage sample.
	AddSample(sample *ContainerUsageSample)
	// GetLastRecommendation returns last recommendation calculated for this
	// aggregator.
	GetLastRecommendation() corev1.ResourceList
	// NeedsRecommendation returns true if this aggregator should have
	// a recommendation calculated.
	NeedsRecommendation() bool
	// GetUpdateMode returns the update mode of VPA controlling this aggregator,
	// nil if aggregator is not autoscaled.
	GetUpdateMode() *vpa_types.UpdateMode
	// SetContainerRestartCounts sets the container restart count of the container,
	// in observation till the last OOM event
	SetContainerRestartCounts(restartCount int, restartBudget int)
	// SetCurrentContainerState sets the last terminated state of the container along
	// with timeline and reason
	SetCurrentContainerState(lastState ContainerCurrentState)
}

// AggregateContainerState holds input signals aggregated from a set of containers.
// It can be used as an input to compute the recommendation.
// (see NewAggregateContainerState()).
// Implements ContainerStateAggregator interface.
type AggregateContainerState struct {
	// Following fields are needed to correctly report quality metrics
	// for VPA. When we record a new sample in an AggregateContainerState
	// we want to know if it needs recommendation, if the recommendation
	// is present and if the automatic updates are on (are we able to
	// apply the recommendation to the pods).
	CreationTime                   time.Time
	LastRecommendation             corev1.ResourceList
	IsUnderVPA                     bool
	UpdateMode                     *vpa_types.UpdateMode
	ScalingMode                    *vpa_types.ContainerScalingMode
	ControlledResources            *[]ResourceName
	LastCtrCPULocalMaxima          *ContainerUsageSample
	LastCtrMemoryLocalMaxima       *ContainerUsageSample
	CurrentCtrCPUUsage             *ContainerUsageSample
	CurrentCtrMemUsage             *ContainerUsageSample
	LastCPULocalMaximaRecordedTime time.Time
	LastMemLocalMaximaRecordedTime time.Time
	TimeWindowForLocalMaxima       time.Duration
	LastSeenRestartCount           int
	RestartBudget                  int
	CurrentContainerState          ContainerCurrentState
	TotalCPUSamplesCount           int
	TotalMemorySamplesCount        int
	ThresholdScaleDown             float64
	ThresholdScaleUp               float64
	ScaleDownSafetyFactor          float64
	ScaleUpFactor                  float64
}

// GetLastRecommendation returns last recorded recommendation.
func (a *AggregateContainerState) GetLastRecommendation() corev1.ResourceList {
	return a.LastRecommendation
}

// NeedsRecommendation returns true if the state should have recommendation calculated.
func (a *AggregateContainerState) NeedsRecommendation() bool {
	return a.IsUnderVPA && a.ScalingMode != nil && *a.ScalingMode != vpa_types.ContainerScalingModeOff
}

// GetUpdateMode returns the update mode of VPA controlling this aggregator,
// nil if aggregator is not autoscaled.
func (a *AggregateContainerState) GetUpdateMode() *vpa_types.UpdateMode {
	return a.UpdateMode
}

// GetScalingMode returns the container scaling mode of the container
// represented byt his aggregator, nil if aggregator is not autoscaled.
func (a *AggregateContainerState) GetScalingMode() *vpa_types.ContainerScalingMode {
	return a.ScalingMode
}

// GetControlledResources returns the list of resources controlled by VPA controlling this aggregator.
// Returns default if not set.
func (a *AggregateContainerState) GetControlledResources() []ResourceName {
	if a.ControlledResources != nil {
		return *a.ControlledResources
	}
	return DefaultControlledResources
}

// MarkNotAutoscaled registers that this container state is not controlled by
// a VPA object.
func (a *AggregateContainerState) MarkNotAutoscaled() {
	a.IsUnderVPA = false
	a.LastRecommendation = nil
	a.UpdateMode = nil
	a.ScalingMode = nil
	a.ControlledResources = nil
}

// MergeContainerState merges two AggregateContainerStates.
func (a *AggregateContainerState) MergeContainerState(other *AggregateContainerState) {

	cpuMaximaRecordTimeDiff := time.Now().Sub(other.LastCPULocalMaximaRecordedTime)
	if a.LastCtrCPULocalMaxima.Usage < other.LastCtrCPULocalMaxima.Usage &&
		cpuMaximaRecordTimeDiff < other.TimeWindowForLocalMaxima { //&&
		a.LastCtrCPULocalMaxima = other.LastCtrCPULocalMaxima
	}

	memMaximaRecordTimeDiff := time.Now().Sub(other.LastMemLocalMaximaRecordedTime)
	if a.LastCtrMemoryLocalMaxima.Usage < other.LastCtrMemoryLocalMaxima.Usage &&
		memMaximaRecordTimeDiff < other.TimeWindowForLocalMaxima { //&&
		a.LastCtrMemoryLocalMaxima = other.LastCtrMemoryLocalMaxima
	}

	if a.CurrentCtrCPUUsage.MeasureStart.IsZero() ||
		(!other.CurrentCtrCPUUsage.MeasureStart.IsZero() &&
			other.CurrentCtrCPUUsage.MeasureStart.After(a.CurrentCtrCPUUsage.MeasureStart)) {
		a.CurrentCtrCPUUsage = other.CurrentCtrCPUUsage
	}

	if a.CurrentCtrMemUsage.MeasureStart.IsZero() ||
		(!other.CurrentCtrMemUsage.MeasureStart.IsZero() &&
			other.CurrentCtrMemUsage.MeasureStart.After(a.CurrentCtrMemUsage.MeasureStart)) {
		a.CurrentCtrMemUsage = other.CurrentCtrMemUsage
	}

	if other.CurrentContainerState.Reason != "" {
		a.CurrentContainerState = other.CurrentContainerState
	}

	if a.LastSeenRestartCount <= other.LastSeenRestartCount {
		a.LastSeenRestartCount = other.LastSeenRestartCount
	}

	if other.LastRecommendation != nil {
		a.LastRecommendation = other.LastRecommendation
	}

}

// NewAggregateContainerState returns a new, empty AggregateContainerState.
func NewAggregateContainerState() *AggregateContainerState {
	config := GetAggregationsConfig()

	lastCtrCPULocalMaxima := &ContainerUsageSample{
		Request:  0,
		Usage:    0,
		Resource: ResourceCPU,
	}

	lastCtrMemLocalMaxima := &ContainerUsageSample{
		Request:  0,
		Usage:    0,
		Resource: ResourceMemory,
	}

	currentCtrCPUUsage := &ContainerUsageSample{
		Request:  0,
		Usage:    0,
		Resource: ResourceCPU,
	}

	currentCtrMemUsage := &ContainerUsageSample{
		Request:  0,
		Usage:    0,
		Resource: ResourceMemory,
	}

	return &AggregateContainerState{
		CreationTime:                   time.Now(),
		TimeWindowForLocalMaxima:       config.ThresholdMonitorTimeWindow,
		ThresholdScaleDown:             config.ThresholdScaleDown,
		ThresholdScaleUp:               config.ThresholdScaleUp,
		ScaleDownSafetyFactor:          config.ScaleDownSafetyFactor,
		ScaleUpFactor:                  config.ScaleUpFactor,
		RestartBudget:                  config.ThresholdNumCrashes,
		CurrentCtrCPUUsage:             currentCtrCPUUsage,
		CurrentCtrMemUsage:             currentCtrMemUsage,
		LastCtrCPULocalMaxima:          lastCtrCPULocalMaxima,
		LastCtrMemoryLocalMaxima:       lastCtrMemLocalMaxima,
		LastCPULocalMaximaRecordedTime: time.Now(),
		LastMemLocalMaximaRecordedTime: time.Now(),
		LastSeenRestartCount:           0,
		TotalCPUSamplesCount:           0,
		TotalMemorySamplesCount:        0,
	}
}

// SetCPUUsage sets the container's current CPU usage
func (a *AggregateContainerState) SetCPUUsage(sample *ContainerUsageSample) {
	a.CurrentCtrCPUUsage = sample
}

// SetMemUsage sets the container's current memory usage
func (a *AggregateContainerState) SetMemUsage(sample *ContainerUsageSample) {
	a.CurrentCtrMemUsage = sample
}

// SetContainerRestartCount sets the container's current restart count
func (a *AggregateContainerState) SetContainerRestartCounts(restartCount int, restartBudget int) {
	a.LastSeenRestartCount = restartCount
	a.RestartBudget = restartBudget
}

// SetCurrentContainerState sets the container's last terminated state
func (a *AggregateContainerState) SetCurrentContainerState(curState ContainerCurrentState) {
	a.CurrentContainerState = curState
}

// AddSample aggregates a single usage sample.
func (a *AggregateContainerState) AddSample(sample *ContainerUsageSample) {
	switch sample.Resource {
	case ResourceCPU:
		a.SetCPUUsage(sample)
		a.addCPULocalMaxima(sample)
		a.TotalCPUSamplesCount++
	case ResourceMemory:
		a.SetMemUsage(sample)
		a.addMemoryLocalMaxima(sample)
		a.TotalMemorySamplesCount++
	default:
		panic(fmt.Sprintf("AddSample doesn't support resource '%s'", sample.Resource))
	}
}

// addCPULocalMaxima is the local maxima value for getting the CPU usage local maxima in threshold time window
func (a *AggregateContainerState) addCPULocalMaxima(sample *ContainerUsageSample) {
	cpuUsageCores := CoresFromCPUAmount(sample.Usage)

	// thresholdMonitorTimeWindow = 30 * time.Minute by default
	diffDuration := time.Now().Sub(a.LastCPULocalMaximaRecordedTime)
	if diffDuration > a.TimeWindowForLocalMaxima && !a.LastCPULocalMaximaRecordedTime.IsZero() {
		// reset CPU Local Maxima Request and Usage
		a.LastCtrCPULocalMaxima.Usage = 0
		a.LastCtrCPULocalMaxima.Request = 0
		a.LastCtrCPULocalMaxima.MeasureStart = time.Time{}
		a.LastCtrCPULocalMaxima.Resource = sample.Resource
		a.LastCPULocalMaximaRecordedTime = time.Now()
	} else {
		// assign LastCtrCPULocalMaxima if only it is less than current CPU usage
		if CoresFromCPUAmount(a.LastCtrCPULocalMaxima.Usage) < cpuUsageCores {
			a.LastCtrCPULocalMaxima.Usage = sample.Usage
			a.LastCtrCPULocalMaxima.Request = sample.Request
			a.LastCtrCPULocalMaxima.MeasureStart = sample.MeasureStart
			a.LastCtrCPULocalMaxima.Resource = sample.Resource
			a.LastCPULocalMaximaRecordedTime = time.Now()
		}
	}

}

// addMemoryLocalMaxima is the local maxima value for getting the CPU usage local maxima in threshold time window
func (a *AggregateContainerState) addMemoryLocalMaxima(sample *ContainerUsageSample) {
	memoryUsage := BytesFromMemoryAmount(sample.Usage)

	// thresholdMonitorTimeWindow = 30 * time.Minute by default
	diffDuration := time.Now().Sub(a.LastMemLocalMaximaRecordedTime)
	if diffDuration > a.TimeWindowForLocalMaxima && !a.LastMemLocalMaximaRecordedTime.IsZero() {
		// reset Memory Local Maxima Request and Usage
		a.LastCtrMemoryLocalMaxima.Usage = 0
		a.LastCtrMemoryLocalMaxima.Request = 0
		a.LastCtrMemoryLocalMaxima.MeasureStart = time.Time{}
		a.LastCtrMemoryLocalMaxima.Resource = sample.Resource
		a.LastMemLocalMaximaRecordedTime = time.Now()
	} else {
		// assign LastCtrCPULocalMaxima if only it is less than current memory usage
		if BytesFromMemoryAmount(a.LastCtrMemoryLocalMaxima.Usage) < memoryUsage {
			a.LastCtrMemoryLocalMaxima.Usage = sample.Usage
			a.LastCtrMemoryLocalMaxima.Request = sample.Request
			a.LastCtrMemoryLocalMaxima.MeasureStart = sample.MeasureStart
			a.LastCtrMemoryLocalMaxima.Resource = sample.Resource
			a.LastMemLocalMaximaRecordedTime = time.Now()
		}
	}

}

// UpdateFromPolicy updates container state scaling mode and controlled resources based on resource
// policy of the VPA object.
func (a *AggregateContainerState) UpdateFromPolicy(resourcePolicy *vpa_types.ContainerResourcePolicy) {
	// ContainerScalingModeAuto is the default scaling mode
	scalingModeAuto := vpa_types.ContainerScalingModeAuto
	a.ScalingMode = &scalingModeAuto
	if resourcePolicy != nil && resourcePolicy.Mode != nil {
		a.ScalingMode = resourcePolicy.Mode
	}
	a.ControlledResources = &DefaultControlledResources
	if resourcePolicy != nil && resourcePolicy.ControlledResources != nil {
		a.ControlledResources = ResourceNamesApiToModel(*resourcePolicy.ControlledResources)
	}
}

// AggregateStateByContainerName takes a set of AggregateContainerStates and merge them
// grouping by the container name. The result is a map from the container name to the aggregation
// from all input containers with the given name.
func AggregateStateByContainerName(aggregateContainerStateMap aggregateContainerStatesMap) ContainerNameToAggregateStateMap {
	// var maxMergeTimeMonitor time.Duration
	containerNameToAggregateStateMap := make(ContainerNameToAggregateStateMap)
	for aggregationKey, aggregation := range aggregateContainerStateMap {
		containerName := aggregationKey.ContainerName()
		aggregateContainerState, isInitialized := containerNameToAggregateStateMap[containerName]
		if !isInitialized {
			aggregateContainerState = NewAggregateContainerState()
			containerNameToAggregateStateMap[containerName] = aggregateContainerState
		}

		aggregateContainerState.MergeContainerState(aggregation)
	}
	return containerNameToAggregateStateMap
}

// ContainerStateAggregatorProxy is a wrapper for ContainerStateAggregator
// that creates ContainerStateAgregator for container if it is no longer
// present in the cluster state.
type ContainerStateAggregatorProxy struct {
	containerID ContainerID
	cluster     *ClusterState
}

// NewContainerStateAggregatorProxy creates a ContainerStateAggregatorProxy
// pointing to the cluster state.
func NewContainerStateAggregatorProxy(cluster *ClusterState, containerID ContainerID) ContainerStateAggregator {
	return &ContainerStateAggregatorProxy{containerID, cluster}
}

// AddSample adds a container sample to the aggregator.
func (p *ContainerStateAggregatorProxy) AddSample(sample *ContainerUsageSample) {
	aggregator := p.cluster.findOrCreateAggregateContainerState(p.containerID)
	aggregator.AddSample(sample)
}

// GetLastRecommendation returns last recorded recommendation.
func (p *ContainerStateAggregatorProxy) GetLastRecommendation() corev1.ResourceList {
	aggregator := p.cluster.findOrCreateAggregateContainerState(p.containerID)
	return aggregator.GetLastRecommendation()
}

// NeedsRecommendation returns true if the aggregator should have recommendation calculated.
func (p *ContainerStateAggregatorProxy) NeedsRecommendation() bool {
	aggregator := p.cluster.findOrCreateAggregateContainerState(p.containerID)
	return aggregator.NeedsRecommendation()
}

// GetUpdateMode returns update mode of VPA controlling the aggregator.
func (p *ContainerStateAggregatorProxy) GetUpdateMode() *vpa_types.UpdateMode {
	aggregator := p.cluster.findOrCreateAggregateContainerState(p.containerID)
	return aggregator.GetUpdateMode()
}

// GetScalingMode returns scaling mode of container represented by the aggregator.
func (p *ContainerStateAggregatorProxy) GetScalingMode() *vpa_types.ContainerScalingMode {
	aggregator := p.cluster.findOrCreateAggregateContainerState(p.containerID)
	return aggregator.GetScalingMode()
}

// SetContainerRestartCount sets the restart count since last OOM event for the container
func (p *ContainerStateAggregatorProxy) SetContainerRestartCounts(restartCount int, restartBudget int) {
	aggregator := p.cluster.findOrCreateAggregateContainerState(p.containerID)
	aggregator.SetContainerRestartCounts(restartCount, restartBudget)
}

// SetContainerCurrentContainerState sets the last terminated state of the container including
// reason and timelines
func (p *ContainerStateAggregatorProxy) SetCurrentContainerState(curState ContainerCurrentState) {
	aggregator := p.cluster.findOrCreateAggregateContainerState(p.containerID)
	aggregator.SetCurrentContainerState(curState)
}
