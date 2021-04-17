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

	model "github.com/gardener/vpa-recommender/pkg/recommender/model"
)

var (
	safetyMarginFraction = flag.Float64("recommendation-margin-fraction", 0.15, `Fraction of usage added as the safety margin to the recommended request`)
	podMinCPUMillicores  = flag.Float64("pod-recommendation-min-cpu-millicores", 25, `Minimum CPU recommendation for a pod`)
	podMinMemoryMb       = flag.Float64("pod-recommendation-min-memory-mb", 250, `Minimum memory recommendation for a pod`)
)

// PodResourceRecommender computes resource recommendation for a Vpa object.
type PodResourceRecommender interface {
	GetRecommendedPodResources(containerNameToAggregateStateMap model.ContainerNameToAggregateStateMap) (RecommendedPodResources, bool)
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

func (r *podResourceRecommender) GetRecommendedPodResources(containerNameToAggregateStateMap model.ContainerNameToAggregateStateMap) (RecommendedPodResources, bool) {
	var recommendation = make(RecommendedPodResources)
	if len(containerNameToAggregateStateMap) == 0 {
		return recommendation, false
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
		estimatedContainerResources, containerUpdated := recommender.estimateContainerResources(aggregatedContainerState)
		if containerUpdated {
			recommendation[containerName] = estimatedContainerResources
		}
	}

	if len(recommendation) == 0 {
		return recommendation, false
	}

	return recommendation, true
}

// Takes AggregateContainerState and returns a container recommendation.
func (r *podResourceRecommender) estimateContainerResources(s *model.AggregateContainerState) (RecommendedContainerResources, bool) {
	// Perculate the resource estimation scale recommendation value using boolean
	estimateTargetEstimator, toScaleTarget := r.targetEstimator.GetResourceEstimation(s)
	// if false to scale, then just reset the value
	if !toScaleTarget {
		// No recommendation
		return RecommendedContainerResources{model.Resources{}, model.Resources{}, model.Resources{}}, false
	}

	estimateLowerBoundEstimator := model.Resources{
		model.ResourceCPU:    model.ScaleResource(estimateTargetEstimator[model.ResourceCPU], 0.5),
		model.ResourceMemory: model.ScaleResource(estimateTargetEstimator[model.ResourceMemory], 0.5),
	}

	estimateUpperBoundEstimator := model.Resources{
		model.ResourceCPU:    model.ScaleResource(estimateTargetEstimator[model.ResourceCPU], 2.0),
		model.ResourceMemory: model.ScaleResource(estimateTargetEstimator[model.ResourceMemory], 2.0),
	}

	return RecommendedContainerResources{
		FilterControlledResources(estimateTargetEstimator, s.GetControlledResources()),
		FilterControlledResources(estimateLowerBoundEstimator, s.GetControlledResources()),
		FilterControlledResources(estimateUpperBoundEstimator, s.GetControlledResources()),
	}, true
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
	//New VPA recommender code

	targetCPUFactor := 2.0
	lowerBoundCPUFactor := targetCPUFactor / 2.0
	upperBoundCPUFactor := 2.0 * targetCPUFactor

	targetMemoryFactor := 2.0
	lowerBoundMemoryFactor := targetMemoryFactor / 2.0
	upperBoundMemoryFactor := 2.0 * targetMemoryFactor

	targetScaleEstimator := NewScaleValueEstimator(targetCPUFactor, targetMemoryFactor)
	lowerBoundScaleEstimator := NewScaleValueEstimator(lowerBoundCPUFactor, lowerBoundMemoryFactor)
	upperBoundScaleEstimator := NewScaleValueEstimator(upperBoundCPUFactor, upperBoundMemoryFactor)

	targetEstimator := WithMargin(*safetyMarginFraction, targetScaleEstimator)
	lowerBoundEstimator := WithMargin(*safetyMarginFraction, lowerBoundScaleEstimator)
	upperBoundEstimator := WithMargin(*safetyMarginFraction, upperBoundScaleEstimator)

	return &podResourceRecommender{
		targetEstimator,
		lowerBoundEstimator,
		upperBoundEstimator}
}
