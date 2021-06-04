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

	corev1 "k8s.io/api/core/v1"
	metrics_quality "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/utils/metrics/quality"

	"k8s.io/klog"
)

const (
	// OOMBumpUpRatio specifies how much memory will be added after observing OOM.
	OOMBumpUpRatio float64 = 1.2
	// OOMMinBumpUp specifies minimal increase of memory after observing OOM.
	OOMMinBumpUp float64 = 100 * 1024 * 1024 // 100MB
)

// ContainerUsageSample is a measure of resource usage of a container over some
// interval.
type ContainerUsageSample struct {
	// Start of the measurement interval.
	MeasureStart time.Time
	// Average CPU usage in cores or memory usage in bytes.
	Usage ResourceAmount
	// CPU or memory request at the time of measurment.
	Request ResourceAmount
	// Which resource is this sample for.
	Resource ResourceName
}

// ContainerState stores information about a single container instance.
// Each ContainerState has a pointer to the aggregation that is used for
// aggregating its usage samples.
// It holds the recent history of CPU and memory utilization.
//   Note: samples are added to intervals based on their start timestamps.
type ContainerState struct {
	// Current request.
	Request Resources
	// Number of restarts seen for the container
	RestartCount int
	// Start of the latest CPU usage sample that was aggregated.
	LastCPUSampleStart time.Time
	// Max memory usage observed in the current aggregation interval.
	memoryPeak ResourceAmount
	// Max memory usage estimated from an OOM event in the current aggregation interval.
	oomPeak ResourceAmount
	// End time of the current memory aggregation interval (not inclusive).
	WindowEnd time.Time
	// Start of the latest memory usage sample that was aggregated.
	LastMemorySampleStart time.Time
	// Aggregation to add usage samples to.
	aggregator ContainerStateAggregator
}

// NewContainerState returns a new ContainerState.
func NewContainerState(request Resources, restartCount int, aggregator ContainerStateAggregator) *ContainerState {
	return &ContainerState{
		Request:               request,
		LastCPUSampleStart:    time.Time{},
		WindowEnd:             time.Time{},
		LastMemorySampleStart: time.Time{},
		aggregator:            aggregator,
		RestartCount:          restartCount,
	}
}

func (sample *ContainerUsageSample) isValid(expectedResource ResourceName) bool {
	return sample.Usage >= 0 && sample.Resource == expectedResource
}

func (container *ContainerState) addCPUSample(sample *ContainerUsageSample) bool {
	if !sample.isValid(ResourceCPU) || !sample.MeasureStart.After(container.LastCPUSampleStart) {
		return false // Discard invalid, duplicate or out-of-order samples.
	}
	container.observeQualityMetrics(sample.Usage, false, corev1.ResourceCPU)
	container.aggregator.AddSample(sample)
	container.LastCPUSampleStart = sample.MeasureStart
	return true
}

func (container *ContainerState) observeQualityMetrics(usage ResourceAmount, isOOM bool, resource corev1.ResourceName) {
	if !container.aggregator.NeedsRecommendation() {
		return
	}
	updateMode := container.aggregator.GetUpdateMode()
	var usageValue float64
	switch resource {
	case corev1.ResourceCPU:
		usageValue = CoresFromCPUAmount(usage)
	case corev1.ResourceMemory:
		usageValue = BytesFromMemoryAmount(usage)
	}
	if container.aggregator.GetLastRecommendation() == nil {
		metrics_quality.ObserveQualityMetricsRecommendationMissing(usageValue, isOOM, resource, updateMode)
		return
	}
	recommendation := container.aggregator.GetLastRecommendation()[resource]
	if recommendation.IsZero() {
		metrics_quality.ObserveQualityMetricsRecommendationMissing(usageValue, isOOM, resource, updateMode)
		return
	}
	var recommendationValue float64
	switch resource {
	case corev1.ResourceCPU:
		recommendationValue = float64(recommendation.MilliValue()) / 1000.0
	case corev1.ResourceMemory:
		recommendationValue = float64(recommendation.Value())
	default:
		klog.Warningf("Unknown resource: %v", resource)
		return
	}
	metrics_quality.ObserveQualityMetrics(usageValue, recommendationValue, isOOM, resource, updateMode)
}

// GetMaxMemoryPeak returns maximum memory usage in the sample, possibly estimated from OOM
func (container *ContainerState) GetMaxMemoryPeak() ResourceAmount {
	return ResourceAmountMax(container.memoryPeak, container.oomPeak)
}

func (container *ContainerState) addMemorySample(sample *ContainerUsageSample, isOOM bool) bool {
	if !sample.isValid(ResourceMemory) || (!isOOM && !sample.MeasureStart.After(container.LastMemorySampleStart)) {
		return false // Discard invalid, duplicate or out-of-order samples.
	}

	container.observeQualityMetrics(sample.Usage, isOOM, corev1.ResourceMemory)
	newPeak := ContainerUsageSample{
		MeasureStart: sample.MeasureStart,
		Usage:        sample.Usage,
		Request:      sample.Request,
		Resource:     ResourceMemory,
	}

	container.aggregator.AddSample(&newPeak)
	if isOOM {
		container.oomPeak = sample.Usage
	} else {
		container.memoryPeak = sample.Usage
	}
	return true
}

// RecordOOM adds info regarding OOM event in the model as an artificial memory sample.
func (container *ContainerState) RecordOOM(timestamp time.Time, requestedMemory ResourceAmount) error {
	// Get max of the request and the recent usage-based memory peak.
	// Omitting oomPeak here to protect against recommendation running too high on subsequent OOMs.
	memoryUsed := ResourceAmountMax(requestedMemory, container.memoryPeak)
	memoryNeeded := ResourceAmountMax(memoryUsed+MemoryAmountFromBytes(OOMMinBumpUp),
		ScaleResource(memoryUsed, OOMBumpUpRatio))

	oomMemorySample := ContainerUsageSample{
		MeasureStart: timestamp,
		Usage:        memoryNeeded,
		Resource:     ResourceMemory,
	}
	if !container.addMemorySample(&oomMemorySample, true) {
		return fmt.Errorf("adding OOM sample failed")
	}
	return nil
}

// RecordRestartCount records the latest restart count observed for a particular container
func (container *ContainerState) RecordRestartCount(restartCount int, restartBudget int) {
	container.aggregator.SetContainerRestartCounts(restartCount, restartBudget)
}

// RecordCurrentContainerState records the current container state reason if waiting or terminated
func (container *ContainerState) RecordCurrentContainerState(curState ContainerCurrentState) {
	container.aggregator.SetCurrentContainerState(curState)
}

// AddSample adds a resource usage sample to the aggregate containrer state.
// Each sample is further checked with already recorded local maxima of the
// resource within a local maxima recording cooling window and updated accordingly.
// Each sample however is used to update the current container usage struct.
func (container *ContainerState) AddSample(sample *ContainerUsageSample) bool {
	switch sample.Resource {
	case ResourceCPU:
		return container.addCPUSample(sample)
	case ResourceMemory:
		return container.addMemorySample(sample, false)
	default:
		return false
	}
}

// Truncate returns the result of rounding d toward zero to a multiple of m.
// If m <= 0, Truncate returns d unchanged.
// This helper function is introduced to support older implementations of the
// time package that don't provide Duration.Truncate function.
func truncate(d, m time.Duration) time.Duration {
	if m <= 0 {
		return d
	}
	return d - d%m
}
