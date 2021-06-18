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

	model "github.com/gardener/vpa-recommender/pkg/recommender/model"
	"k8s.io/klog"
)

const (
	crashLoopBackOff = "CrashLoopBackOff"
)

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
func (e *constEstimator) GetResourceEstimation(s *model.AggregateContainerState) (model.Resources, bool) {
	return e.resources, true
}

func (e *marginEstimator) GetResourceEstimation(s *model.AggregateContainerState) (model.Resources, bool) {
	originalResources, toScale := e.baseEstimator.GetResourceEstimation(s)

	if toScale {
		newResources := make(model.Resources)
		for resource, resourceAmount := range originalResources {
			margin := model.ScaleResource(resourceAmount, e.marginFraction)
			newResources[resource] = originalResources[resource] + margin
		}
		return newResources, true
	}

	return originalResources, false

}

func (e *minResourcesEstimator) GetResourceEstimation(s *model.AggregateContainerState) (model.Resources, bool) {
	originalResources, toScale := e.baseEstimator.GetResourceEstimation(s)

	if toScale {
		newResources := make(model.Resources)
		for resource, resourceAmount := range originalResources {
			if resourceAmount < e.minResources[resource] {
				resourceAmount = e.minResources[resource]
			}
			newResources[resource] = resourceAmount
		}

		return newResources, true
	}

	return originalResources, false
}

func getScaleValue(s *model.AggregateContainerState) (float64, float64) {
	// 1. Scale Up  : If current usage is greater than thresholdScaleUp * current requests
	// 2. Scale Down: If current usage is lesser than thresholdScaleDown * current requests
	// 3. Stay same : If current usage is greater than thresholdScaleDown * current requests
	//                and lesser than thresholdScaleUp * current requests
	var cpuScaleValue, memScaleValue float64 = 1.0, 1.0
	cpuLocalMaxima := s.LastCtrCPULocalMaxima
	memLocalMaxima := s.LastCtrMemoryLocalMaxima
	currentCPUUsage := s.CurrentCtrCPUUsage
	currentMemUsage := s.CurrentCtrMemUsage

	// CPU
	currentCPURequestLowerThreshold := float64(currentCPUUsage.Request) * s.ThresholdScaleDown
	currentCPURequestUpperThreshold := float64(currentCPUUsage.Request) * s.ThresholdScaleUp
	// Memory
	currentMemRequestLowerThreshold := float64(currentMemUsage.Request) * s.ThresholdScaleDown
	currentMemRequestUpperThreshold := float64(currentMemUsage.Request) * s.ThresholdScaleUp

	if s.RestartBudget <= 0 || s.CurrentContainerState.Reason == crashLoopBackOff {
		s.RestartBudget = model.DefaultThresholdNumCrashes
		memScaleValue = s.ScaleUpFactor
		cpuScaleValue = s.ScaleUpFactor
		klog.Infof("Doubling CPU and Memory: CPU Scale = %v, Memory Scale = %v", cpuScaleValue, memScaleValue)
		return cpuScaleValue, memScaleValue
	}

	var maxOfCurrentCPUAndLocalMaxima float64
	if currentCPUUsage.Usage != 0 {
		maxOfCurrentCPUAndLocalMaxima = math.Max(float64(currentCPUUsage.Usage), float64(cpuLocalMaxima.Usage))
	} else {
		// Handling a case where CPU usage is 0 when the pod has just started or just out of crash loop
		maxOfCurrentCPUAndLocalMaxima = float64(currentCPUUsage.Request)
	}

	if float64(currentCPUUsage.Usage) > currentCPURequestUpperThreshold { // Scale Up
		cpuScaleValue = s.ScaleUpFactor
	} else if maxOfCurrentCPUAndLocalMaxima < float64(currentCPURequestLowerThreshold) { // Scale Down
		cpuScaleValue = s.ScaleDownSafetyFactor
	} else { // No Scale
		cpuScaleValue = 1.0
	}

	maxOfCurrentMemAndLocalMaxima := math.Max(float64(currentMemUsage.Usage), float64(memLocalMaxima.Usage))
	if float64(currentMemUsage.Usage) > currentMemRequestUpperThreshold { // Scale Up
		memScaleValue = s.ScaleUpFactor
	} else if maxOfCurrentMemAndLocalMaxima < float64(currentMemRequestLowerThreshold) { // Scale Down
		memScaleValue = s.ScaleDownSafetyFactor
	} else { // No Scale
		memScaleValue = 1.0
	}

	klog.Infof("CPU Scale = %v, Memory Scale = %v", cpuScaleValue, memScaleValue)
	return cpuScaleValue, memScaleValue
}

func (e *scaledResourceEstimator) GetResourceEstimation(s *model.AggregateContainerState) (model.Resources, bool) {

	currentCPU := s.CurrentCtrCPUUsage
	currentMem := s.CurrentCtrMemUsage
	originalResources := model.Resources{
		model.ResourceCPU:    currentCPU.Usage,
		model.ResourceMemory: currentMem.Usage,
	}

	// If currentCPU or currentMem setting original resources above is 0 due to Crash Loop Back Off,
	// then set the corresponding resource to the max(usage, requests) of that resource.
	if s.CurrentContainerState.Reason == crashLoopBackOff {
		originalResources[model.ResourceCPU] = model.ResourceAmount(math.Max(float64(currentCPU.Request), float64(currentCPU.Usage)))
	}

	if s.CurrentContainerState.Reason == crashLoopBackOff {
		originalResources[model.ResourceMemory] = model.ResourceAmount(math.Max(float64(currentMem.Request), float64(currentMem.Usage)))
	}

	scaledResources := make(model.Resources)
	cpuScale, memScale := getScaleValue(s)

	// skip scaling if both CPU and memory scale values are 1.0
	if cpuScale == 1.0 && memScale == 1.0 {
		return originalResources, false
	}

	for resource, resourceAmount := range originalResources {
		scaleValue := 0.0
		scaledResources[resource] = originalResources[resource]

		if resource == "cpu" {
			scaleValue = cpuScale
			// If CPU recommendation is 1.0 current CPU request is already doing better
			// No need to change the resource value again based on current usage
			if scaleValue == 1.0 {
				scaledResources[resource] = currentCPU.Request
			}
		} else if resource == "memory" {
			scaleValue = memScale
			// If memory recommendation is 1.0 current memory request is already doing better
			// No need to change the resource value again based on current usage
			if scaleValue == 1.0 {
				scaledResources[resource] = currentMem.Request
			}
		}

		if scaleValue > 1.0 { // Scale Up or Down
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
