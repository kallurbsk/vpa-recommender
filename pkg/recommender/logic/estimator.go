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

package logic

import (
	"math"
	"time"

	model "github.com/gardener/vpa-recommender/pkg/recommender/model"
)

// TODO: Split the estimator to have a separate estimator object for CPU and memory.

// ResourceEstimator is a function from AggregateContainerState to
// parent_model.Resources, e.g. a prediction of resources needed by a group of
// containers.
type ResourceEstimator interface {
	//GetResourceEstimation(s *model.AggregateContainerState) parent_model.Resources
	GetResourceEstimation(s *model.AggregateContainerState) (model.Resources, bool)
}

// Implementation of ResourceEstimator that returns constant amount of
// resources. This can be used as by a fake recommender for test purposes.
type constEstimator struct {
	resources model.Resources
}

// Simple implementation of the ResourceEstimator interface. It returns specific
// percentiles of CPU usage distribution and memory peaks distribution.
// type percentileEstimator struct {
// 	cpuPercentile    float64
// 	memoryPercentile float64
// }

type marginEstimator struct {
	marginFraction float64
	baseEstimator  ResourceEstimator
}

type minResourcesEstimator struct {
	minResources  model.Resources
	baseEstimator ResourceEstimator
}

type confidenceMultiplier struct {
	multiplier    float64
	exponent      float64
	baseEstimator ResourceEstimator
}

// Base scale up threshold estimator struct to ensure scaling happens below the scale up threshold limit
type scaleUpThresholdEstimator struct {
	thresoldScaleUp float64
	baseEstimator   ResourceEstimator
}

// Base scale down threshold estimator struct to ensure scaling happens above the scale down threshold limit
type scaleDownThresholdEstimator struct {
	thresholdScaleDown float64
	baseEstimator      ResourceEstimator
}

// Scale the resources between 1x - 2x the current usage within the safety margin for resource usage
type scaledResourceEstimator struct {
	cpuScaleValue float64
	memScaleValue float64
	// baseEstimator ResourceEstimator // TODO BSK: Remove this once we have local cpu and memory usage values
}

// NewScaleUpThresholdEstiamtor returns a new scaleUpThresholdEstimator to set upper boundary for scaling up
func NewScaleUpThresholdEstimator(thresholdScaleUp float64, baseEstimator ResourceEstimator) ResourceEstimator {
	return &scaleUpThresholdEstimator{thresholdScaleUp, baseEstimator}
}

// NewScaleDownThresholdEstiamtor returns a new scaleDownThresholdEstimator to set lower boundary for scaling down
func NewScaleDownThresholdEstimator(thresholdScaleDown float64, baseEstimator ResourceEstimator) ResourceEstimator {
	return &scaleDownThresholdEstimator{thresholdScaleDown, baseEstimator}
}

// NewScaleValueEstimator returns a new scaledResourceEstimator with scale value parameters for CPU and memory
func NewScaleValueEstimator(cpuScaleValue float64, memScaleValue float64) ResourceEstimator {
	return &scaledResourceEstimator{cpuScaleValue, memScaleValue} //baseEstimator}
}

// NewConstEstimator returns a new constEstimator with given resources.
func NewConstEstimator(resources model.Resources) ResourceEstimator {
	return &constEstimator{resources}
}

// NewPercentileEstimator returns a new percentileEstimator that uses provided percentiles.
// func NewPercentileEstimator(cpuPercentile float64, memoryPercentile float64) ResourceEstimator {
// 	return &percentileEstimator{cpuPercentile, memoryPercentile}
// }

// WithMargin returns a given ResourceEstimator with margin applied.
// The returned resources are equal to the original resources plus (originalResource * marginFraction)
func WithMargin(marginFraction float64, baseEstimator ResourceEstimator) ResourceEstimator {
	return &marginEstimator{marginFraction, baseEstimator}
}

// WithMinResources returns a given ResourceEstimator with minResources applied.
// The returned resources are equal to the max(original resources, minResources)
func WithMinResources(minResources model.Resources, baseEstimator ResourceEstimator) ResourceEstimator {
	return &minResourcesEstimator{minResources, baseEstimator}
}

// WithConfidenceMultiplier returns a given ResourceEstimator with confidenceMultiplier applied.
func WithConfidenceMultiplier(multiplier, exponent float64, baseEstimator ResourceEstimator) ResourceEstimator {
	return &confidenceMultiplier{multiplier, exponent, baseEstimator}
}

// Returns a constant amount of resources.
func (e *constEstimator) GetResourceEstimation(s *model.AggregateContainerState) (model.Resources, bool) {
	return e.resources, true
}

// Returns specific percentiles of CPU and memory peaks distributions.
// TODO BSK : We are not using this function. Hence changed the import object to parent_model.
// Percentile function call was returning an error and hence wanted to avoid it
// func (e *percentileEstimator) GetResourceEstimation(s *parent_model.AggregateContainerState) (parent_model.Resources, bool) {
// 	return parent_model.Resources{
// 		parent_model.ResourceCPU: parent_model.CPUAmountFromCores(
// 			s.AggregateCPUUsage.Percentile(e.cpuPercentile)),
// 		parent_model.ResourceMemory: parent_model.MemoryAmountFromBytes(
// 			s.AggregateMemoryPeaks.Percentile(e.memoryPercentile)),
// 	}, true
// }

// Returns a non-negative real number that heuristically measures how much
// confidence the history aggregated in the AggregateContainerState provides.
// For a workload producing a steady stream of samples over N days at the rate
// of 1 sample per minute, this metric is equal to N.
// This implementation is a very simple heuristic which looks at the total count
// of samples and the time between the first and the last sample.
func getConfidence(s *model.AggregateContainerState) float64 {
	// Distance between the first and the last observed sample time, measured in days.
	lifespanInDays := float64(s.LastSampleStart.Sub(s.FirstSampleStart)) / float64(time.Hour*24)
	// Total count of samples normalized such that it equals the number of days for
	// frequency of 1 sample/minute.
	samplesAmount := float64(s.TotalSamplesCount) / (60 * 24)
	return math.Min(lifespanInDays, samplesAmount)
}

// Returns resources computed by the underlying estimator, scaled based on the
// confidence metric, which depends on the amount of available historical data.
// Each resource is transformed as follows:
//     scaledResource = originalResource * (1 + 1/confidence)^exponent.
// This can be used to widen or narrow the gap between the lower and upper bound
// estimators depending on how much input data is available to the estimators.
func (e *confidenceMultiplier) GetResourceEstimation(s *model.AggregateContainerState) (model.Resources, bool) {
	confidence := getConfidence(s)
	originalResources, _ := e.baseEstimator.GetResourceEstimation(s)
	scaledResources := make(model.Resources)
	for resource, resourceAmount := range originalResources {
		scaledResources[resource] = model.ScaleResource(
			resourceAmount, math.Pow(1.+e.multiplier/confidence, e.exponent))
	}
	return scaledResources, true
}

func (e *marginEstimator) GetResourceEstimation(s *model.AggregateContainerState) (model.Resources, bool) {
	originalResources, _ := e.baseEstimator.GetResourceEstimation(s)
	newResources := make(model.Resources)
	for resource, resourceAmount := range originalResources {
		margin := model.ScaleResource(resourceAmount, e.marginFraction)
		newResources[resource] = originalResources[resource] + margin
	}
	return newResources, true
}

func (e *minResourcesEstimator) GetResourceEstimation(s *model.AggregateContainerState) (model.Resources, bool) {
	originalResources, _ := e.baseEstimator.GetResourceEstimation(s)
	newResources := make(model.Resources)
	for resource, resourceAmount := range originalResources {
		if resourceAmount < e.minResources[resource] {
			resourceAmount = e.minResources[resource]
		}
		newResources[resource] = resourceAmount
	}
	return newResources, true
}

// BSK : New VPA estimators

func getScaleValue(s *model.AggregateContainerState) (float64, float64) {
	/*
		1. Compare the current usage CPU and memory with previously stored values from VerticalPodAutoscalerCheckpointStatus vertical-pod-autoscaler/e2e/vendor/k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1/types.go
		2. If it is greater than the previously stored value, double the resource
		3. If it is lesser than the previously stored value, set to a user defined scale value which is taken as input during recommender initiation
	*/
	var cpuScaleValue, memScaleValue float64

	cpuScaleValue = 1.0
	memScaleValue = 1.0
	cpuLocalMaxima := s.LastCtrCPULocalMaxima
	memLocalMaxima := s.LastCtrMemoryLocalMaxima
	currentCPUUsage := s.CurrentCtrCPUUsage
	currentMemUsage := s.CurrentCtrMemUsage

	// CPU
	currentCPURequestLowerThreshold := float64(currentCPUUsage.Request) * s.ThresholdScaleDown
	currentCPURequestUpperThreshold := float64(currentCPUUsage.Request) * s.ThresholdScaleUp

	if currentCPURequestLowerThreshold < float64(currentCPUUsage.Usage) && float64(currentCPUUsage.Usage) < currentCPURequestUpperThreshold {
		cpuScaleValue = 1.0
	} else if float64(currentCPUUsage.Usage) > currentCPURequestUpperThreshold { // Scale Up
		cpuScaleValue = s.ScaleUpValue
	} else if math.Max(float64(currentCPUUsage.Usage), float64(cpuLocalMaxima.Usage)) < float64(currentCPURequestLowerThreshold) { // Scale Down
		cpuScaleValue = s.ScaleDownSafetyMargin
	} // if none of the conditions hold good, the scale value is pegged at 1.0

	// Memory
	currentMemRequestLowerThreshold := float64(currentMemUsage.Request) * s.ThresholdScaleDown
	currentMemRequestUpperThreshold := float64(currentMemUsage.Request) * s.ThresholdScaleUp

	if currentMemRequestLowerThreshold < float64(currentMemUsage.Usage) && float64(currentMemUsage.Usage) < currentMemRequestUpperThreshold {
		memScaleValue = 1.0
	} else if float64(currentMemUsage.Usage) > currentMemRequestUpperThreshold { // Scale Up
		memScaleValue = s.ScaleUpValue
	} else if math.Max(float64(currentMemUsage.Usage), float64(memLocalMaxima.Usage)) < float64(currentMemRequestLowerThreshold) { // Scale Down
		memScaleValue = s.ScaleDownSafetyMargin
	} // if none of the conditions hold good, the scale value is pegged at 1.0

	if int(s.RestartCountSinceLastOOM) > s.ThresholdNumCrashes {
		s.RestartCountSinceLastOOM = 0
		memScaleValue = s.ScaleUpValue
		cpuScaleValue = s.ScaleUpValue
	}

	return cpuScaleValue, memScaleValue
}

func (e *scaledResourceEstimator) GetResourceEstimation(s *model.AggregateContainerState) (model.Resources, bool) {
	// We need current CPU usage and memory than a histogram like picture.
	// Use the function which we will declare in ACS go module and get the current CPU and memory usage value
	// See test case example for this

	// Instead of getting the original resources we need to make sure
	// we get CurrentCPUUsage and CurrentMemUsage here to determine and apply scale values
	// to them. So check how to get them here.
	cpuUsage := s.CurrentCtrCPUUsage
	memUsage := s.CurrentCtrMemUsage
	originalResources := model.Resources{
		model.ResourceCPU:    cpuUsage.Usage,
		model.ResourceMemory: memUsage.Usage,
	}

	scaledResources := make(model.Resources)
	cpuScale, memScale := getScaleValue(s)

	// skip scaling if both CPU and memory scale values are 1.0
	if cpuScale == 1.0 && memScale == 1.0 {
		return originalResources, false
	}

	for resource, resourceAmount := range originalResources {

		scaleValue := 0.0
		if resource == "cpu" {
			scaleValue = cpuScale
		} else if resource == "memory" {
			scaleValue = memScale
		}
		scaledResources[resource] = originalResources[resource]

		if scaleValue > float64(1.0) {
			scaledResources[resource] = model.ScaleResource(resourceAmount, scaleValue)
		}
	}
	return scaledResources, true
}

func (e *scaleUpThresholdEstimator) GetResourceEstimation(s *model.AggregateContainerState) (model.Resources, bool) {
	originalResources, _ := e.baseEstimator.GetResourceEstimation(s)
	thresholdScaleUpResources := make(model.Resources)
	for resource, resourceAmount := range originalResources {
		thresholdScaleUpResources[resource] = model.ScaleResource(resourceAmount, e.thresoldScaleUp)
	}

	return thresholdScaleUpResources, true
}

func (e *scaleDownThresholdEstimator) GetResourceEstimation(s *model.AggregateContainerState) (model.Resources, bool) {
	originalResources, _ := e.baseEstimator.GetResourceEstimation(s)
	thresholdScaleDownResources := make(model.Resources)
	for resource, resourceAmount := range originalResources {
		thresholdScaleDownResources[resource] = model.ScaleResource(resourceAmount, e.thresholdScaleDown)
	}

	return thresholdScaleDownResources, true
}
