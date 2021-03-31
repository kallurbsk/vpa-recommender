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
	"flag"

	"k8s.io/autoscaler/vertical-pod-autoscaler/pkg/recommender/model"
	"k8s.io/klog"
)

var (
	safetyMarginFraction = flag.Float64("recommendation-margin-fraction", 0.15, `Fraction of usage added as the safety margin to the recommended request`)
	podMinCPUMillicores  = flag.Float64("pod-recommendation-min-cpu-millicores", 25, `Minimum CPU recommendation for a pod`)
	podMinMemoryMb       = flag.Float64("pod-recommendation-min-memory-mb", 250, `Minimum memory recommendation for a pod`)
)

// PodResourceRecommender computes resource recommendation for a Vpa object.
type PodResourceRecommender interface {
	GetRecommendedPodResources(containerNameToAggregateStateMap model.ContainerNameToAggregateStateMap) RecommendedPodResources
}

// RecommendedPodResources is a Map from container name to recommended resources.
type RecommendedPodResources map[string]RecommendedContainerResources

// RecommendedContainerResources is the recommendation of resources for a
// container.
type RecommendedContainerResources struct {
	// Recommended optimal amount of resources.
	Target model.Resources
	// Recommended minimum amount of resources.
	LowerBound model.Resources
	// Recommended maximum amount of resources.
	UpperBound model.Resources
}

type podResourceRecommender struct {
	targetEstimator     ResourceEstimator
	lowerBoundEstimator ResourceEstimator
	upperBoundEstimator ResourceEstimator
}

func (r *podResourceRecommender) GetRecommendedPodResources(containerNameToAggregateStateMap model.ContainerNameToAggregateStateMap) RecommendedPodResources {
	var recommendation = make(RecommendedPodResources)
	if len(containerNameToAggregateStateMap) == 0 {
		return recommendation
	}

	fraction := 1.0 / float64(len(containerNameToAggregateStateMap))
	minResources := model.Resources{
		model.ResourceCPU:    model.ScaleResource(model.CPUAmountFromCores(*podMinCPUMillicores*0.001), fraction),
		model.ResourceMemory: model.ScaleResource(model.MemoryAmountFromBytes(*podMinMemoryMb*1024*1024), fraction),
	}

	recommender := &podResourceRecommender{
		WithMinResources(minResources, r.targetEstimator),
		WithMinResources(minResources, r.lowerBoundEstimator),
		WithMinResources(minResources, r.upperBoundEstimator),
	}

	for containerName, aggregatedContainerState := range containerNameToAggregateStateMap {
		recommendation[containerName] = recommender.estimateContainerResources(aggregatedContainerState)
	}
	return recommendation
}

// Takes AggregateContainerState and returns a container recommendation.
func (r *podResourceRecommender) estimateContainerResources(s *model.AggregateContainerState) RecommendedContainerResources {
	// Perculate the resource estimation scale recommendation value using boolean
	estimateTargetEstimator, toScaleTarget := r.targetEstimator.GetResourceEstimation(s)
	// if false to scale, then just reset the value
	if !toScaleTarget {
		estimateTargetEstimator = r.targetEstimator
	}

	estimateLowerBoundEstimator, toScaleLB := r.lowerBoundEstimator.GetResourceEstimation(s)
	if !toScaleLB {
		estimateLowerBoundEstimator = r.lowerBoundEstimator
	}

	estimateUpperBoundEstimator, toScaleUB := r.upperBoundEstimator.GetResourceEstimation(s)
	if !toScaleUB {
		estimateUpperBoundEstimator = r.upperBoundEstimator
	}

	return RecommendedContainerResources{
		// FilterControlledResources(r.targetEstimator.GetResourceEstimation(s), s.GetControlledResources())
		FilterControlledResources(estimateTargetEstimator, s.GetControlledResources()),
		// add an if else to get the GetREsourceEstimation
		// FilterControlledResources(r.lowerBoundEstimator.GetResourceEstimation(s), s.GetControlledResources()),
		FilterControlledResources(estimateLowerBoundEstimator, s.GetControlledResources()),
		// FilterControlledResources(r.upperBoundEstimator.GetResourceEstimation(s), s.GetControlledResources()),
		FilterControlledResources(estimateUpperBoundEstimator, s.GetControlledResources()),
	}
}

// FilterControlledResources returns estimations from 'estimation' only for resources present in 'controlledResources'.
func FilterControlledResources(estimation model.Resources, controlledResources []model.ResourceName) model.Resources {
	result := make(model.Resources)
	for _, resource := range controlledResources {
		if value, ok := estimation[resource]; ok {
			result[resource] = value
		}
	}
	return result
}

// CreatePodResourceRecommender returns the primary recommender.
func CreatePodResourceRecommender() PodResourceRecommender {
	// BSK: New VPA recommender code

	targetCPUFactor := 2.0
	lowerBoundCPUFactor := targetCPUFactor / 2.0
	upperBoundCPUFactor := 2.0 * targetCPUFactor

	targetMemoryFactor := 2.0
	lowerBoundMemoryFactor := targetMemoryFactor / 2.0
	upperBoundMemoryFactor := 2.0 * targetMemoryFactor

	targetScaleEstimator := NewScaleValueEstimator(targetCPUFactor, targetMemoryFactor)
	lowerBoundScaleEstimator := NewScaleValueEstimator(lowerBoundCPUFactor, lowerBoundMemoryFactor)
	upperBoundScaleEstimator := NewScaleValueEstimator(upperBoundCPUFactor, upperBoundMemoryFactor)
	klog.V(1).Infof("BSK recommender logic targetScaleEstimator = %+v", targetScaleEstimator)
	klog.V(1).Infof("BSK recommender logic lowerBoundScaleEstimator = %+v", lowerBoundScaleEstimator)
	klog.V(1).Infof("BSK recommender logic upperBoundScaleEstimator = %+v", upperBoundScaleEstimator)

	targetEstimator := WithMargin(*safetyMarginFraction, targetScaleEstimator)
	lowerBoundEstimator := WithMargin(*safetyMarginFraction, lowerBoundScaleEstimator)
	upperBoundEstimator := WithMargin(*safetyMarginFraction, upperBoundScaleEstimator)
	klog.V(1).Infof("BSK 2x recommender logic targetEstimator = %+v", targetEstimator)
	klog.V(1).Infof("BSK 2x recommender logic lowerBoundEstimator = %+v", lowerBoundEstimator)
	klog.V(1).Infof("BSK 2x recommender logic upperBoundEstimator = %+v", upperBoundEstimator)

	return &podResourceRecommender{
		targetEstimator,
		lowerBoundEstimator,
		upperBoundEstimator}
}
