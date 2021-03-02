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
	"k8s.io/autoscaler/vertical-pod-autoscaler/pkg/recommender/model"
)

// TODO: Split the estimator to have a separate estimator object for CPU and memory.

// ResourceEstimator is a function from AggregateContainerState to
// model.Resources, e.g. a prediction of resources needed by a group of
// containers.
type ResourceEstimator interface {
	GetResourceEstimation(s *model.AggregateContainerState) model.Resources
}

// Implementation of ResourceEstimator that returns constant amount of
// resources. This can be used as by a fake recommender for test purposes.
type constEstimator struct {
	resources model.Resources
}

type marginEstimator struct {
	marginFraction float64
	baseEstimator  ResourceEstimator
}

type minResourcesEstimator struct {
	minResources  model.Resources
	baseEstimator ResourceEstimator
}

// Base scale up threshold estimator struct to ensure scaling happens below the scale up threshold limit
type scaleUpThresholdEstimator struct {
	thresoldScaleUp float64
}

// Base scale down threshold estimator struct to ensure scaling happens above the scale down threshold limit
type scaleDownThresholdEstimator struct {
	thresholdScaleDown float64
}

// Scale the resources between 1x - 2x the current usage within the safety margin for resource usage
type scaledResourceEstimator struct {
	cpuScaleValue float64
	memScaleValue float64
	// baseEstimator ResourceEstimator
}

// NewScaleUpThresholdEstiamtor returns a new scaleUpThresholdEstimator to set upper boundary for scaling up
func NewScaleUpThresholdEstimator(thresholdScaleUp float64) ResourceEstimator {
	return &scaleUpThresholdEstimator{thresholdScaleUp}
}

// NewScaleDownThresholdEstiamtor returns a new scaleDownThresholdEstimator to set lower boundary for scaling down
func NewScaleDownThresholdEstimator(thresholdScaleDown float64) ResourceEstimator {
	return &scaleDownThresholdEstimator{thresholdScaleDown}
}

// NewConstEstimator returns a new constEstimator with given resources.
func NewConstEstimator(resources model.Resources) ResourceEstimator {
	return &constEstimator{resources}
}

// NewScaleValueEstimator returns a new scaledResourceEstimator with scale value parameters for CPU and memory
func NewScaleValueEstimator(cpuScaleValue float64, memScaleValue float64) ResourceEstimator {
	return &scaledResourceEstimator{cpuScaleValue, memScaleValue}
}

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

// Returns a constant amount of resources.
func (e *constEstimator) GetResourceEstimation(s *model.AggregateContainerState) model.Resources {
	return e.resources
}

// TODO BSK: complete this function
func getScaleValue(s *model.AggregateContainerState) float64 {
	// CPUUsage = s.AggregateCPUUsage
	// memoryUsage = s.AggregateMemoryPeaks
	// TODO BSK:
	/*
		1. Compare the current usage CPU and memory with previously stored values from VerticalPodAutoscalerCheckpointStatus vertical-pod-autoscaler/e2e/vendor/k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1/types.go
		2. If it is greater than the previously stored value, double the resource
		3. If it is lesser than the previously stored value, set to a user defined scale value which is taken as input during recommender initiation
	*/

	cpuLocalMaxima := s.LocalMaximaCPU
	memoryLocalMaxima := s.LocalMaximaMemory

	cpuUsageLowerThreshold := cpuLocalMaxima.Usage * thresholdScaleDown
	memUsageLowerThreshold := memLocalMaxima.Usage * thresholdScaleDown

	cpuUsageUpperThreshold := cpuLocalMaxima.Usage * thresholdScaleUp
	memUsageUpperThrehsold := memLocalMaxima.Usage * thresholdScaleUp

	if cpuUsageLowerThreshold < cpuLocalMaxima.Usage && cpuLocalMaxima.Usage > cpuUsageUpperThreshold {
		cpuScaleValue = 1.0 // keep it as is
	} else if cpuUsageLowerThreshold < cpuLocalMaxima.Request {
		cpuScaleValue = scaleDownSafetyMargin
	} else if cpuUsageUpperThreshold > cpuLocalMaxima.Request {
		cpuScaleValue = scaleUpValue
	}

	if memUsageLowerThreshold < memLocalMaxima.Usage && memLocalMaxima.Usage > memUsageUpperThreshold {
		memScaleValue = 1.0 // keep it as is
	} else if memUsageLowerThreshold < memLocalMaxima.Request {
		memScaleValue = scaleDownSafetyMargin
	} else if memUsageUpperThrehsold > memLocalMaxima.Request {
		memScaleValue = scaleUpValue
	}

	if s.RestartCountSinceLastOOM > thresholdNoCrashes {
		s.RestartCountSinceLastOOM = 0
		memScaleValue = scaleUpValue
		cpuScaleValue = scaleUpValue
	}

	return cpuScaleValue, memScaleValue
}

func (e *scaledResourceEstimator) GetResourceEstimation(s *model.AggregateContainerState) model.Resources {
	originalResources := e.baseEstimator.GetResourceEstimation(s)
	scaledResources := make(model.Resources)
	cpuScale, memScale := getScaleValue(s)
	for resource, resourceAmount := range originalResources {
		scaleValue := 0.0
		if resource == "cpu" {
			scaleValue = cpuScale
		} else if resource == "memory" {
			scaleValue = memScale
		}
		scaledResources[resource] = model.ScaleResource(resourceAmount, scaleValue)
	}
	return scaledResources
}

func (e *scaleUpThresholdEstimator) GetResourceEstimation(s *model.AggregateCollectionState) model.Resources {
	originalResources := e.baseEstimator.GetResourceEstimation(s)
	thresholdScaleUpResources := make(model.Resources)
	for resource, resourceAmount := range originalResources {
		thresholdScaleUpResources[resource] = model.ScaleResource(resourceAmount, e.thresoldScaleUp)
	}

	return thresholdScaleUpResources
}

func (e *scaleDownThresholdEstimator) GetResourceEstimation(s *model.AggregateCollectionState) model.Resources {
	originalResources := e.baseEstimator.GetResourceEstimation(s)
	thresholdScaleDownResources := make(model.Resources)
	for resource, resourceAmount := range originalResources {
		thresholdScaleDownResources[resource] = model.ScaleResource(resourceAmount, e.thresholdScaleDown)
	}

	return thresholdScaleDownResources
}

func (e *marginEstimator) GetResourceEstimation(s *model.AggregateContainerState) model.Resources {
	originalResources := e.baseEstimator.GetResourceEstimation(s)
	newResources := make(model.Resources)
	for resource, resourceAmount := range originalResources {
		margin := model.ScaleResource(resourceAmount, e.marginFraction)
		newResources[resource] = originalResources[resource] + margin
	}
	return newResources
}

func (e *minResourcesEstimator) GetResourceEstimation(s *model.AggregateContainerState) model.Resources {
	originalResources := e.baseEstimator.GetResourceEstimation(s)
	newResources := make(model.Resources)
	for resource, resourceAmount := range originalResources {
		if resourceAmount < e.minResources[resource] {
			resourceAmount = e.minResources[resource]
		}
		newResources[resource] = resourceAmount
	}
	return newResources
}
