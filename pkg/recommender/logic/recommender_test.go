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

package logic

import (
	"testing"
	"time"

	model "github.com/gardener/vpa-recommender/pkg/recommender/model"
	"github.com/stretchr/testify/assert"
)

var (
	timeLayout       = "2006-01-02 15:04:05"
	testTimestamp, _ = time.Parse(timeLayout, "2017-04-18 17:35:05")
)

func TestMinResourcesApplied(t *testing.T) {
	constEstimator := NewConstEstimator(model.Resources{
		model.ResourceCPU:    model.CPUAmountFromCores(0.001),
		model.ResourceMemory: model.MemoryAmountFromBytes(1e6),
	})
	recommender := podResourceRecommender{
		constEstimator,
		constEstimator,
		constEstimator}

	containerNameToAggregateStateMap := model.ContainerNameToAggregateStateMap{
		"container-1": &model.AggregateContainerState{},
	}

	recommendedResources, gotRecommended := recommender.GetRecommendedPodResources(containerNameToAggregateStateMap)
	assert.Equal(t, model.CPUAmountFromCores(*podMinCPUMillicores/1000), recommendedResources["container-1"].Target[model.ResourceCPU])
	assert.Equal(t, model.MemoryAmountFromBytes(*podMinMemoryMb*1024*1024), recommendedResources["container-1"].Target[model.ResourceMemory])
	assert.True(t, gotRecommended)
}

func TestMinResourcesSplitAcrossContainers(t *testing.T) {
	constEstimator := NewConstEstimator(model.Resources{
		model.ResourceCPU:    model.CPUAmountFromCores(0.001),
		model.ResourceMemory: model.MemoryAmountFromBytes(1e6),
	})
	recommender := podResourceRecommender{
		constEstimator,
		constEstimator,
		constEstimator}

	containerNameToAggregateStateMap := model.ContainerNameToAggregateStateMap{
		"container-1": &model.AggregateContainerState{},
		"container-2": &model.AggregateContainerState{},
	}

	recommendedResources, gotRecommended := recommender.GetRecommendedPodResources(containerNameToAggregateStateMap)
	assert.Equal(t, model.CPUAmountFromCores((*podMinCPUMillicores/1000)/2), recommendedResources["container-1"].Target[model.ResourceCPU])
	assert.Equal(t, model.CPUAmountFromCores((*podMinCPUMillicores/1000)/2), recommendedResources["container-2"].Target[model.ResourceCPU])
	assert.Equal(t, model.MemoryAmountFromBytes((*podMinMemoryMb*1024*1024)/2), recommendedResources["container-1"].Target[model.ResourceMemory])
	assert.Equal(t, model.MemoryAmountFromBytes((*podMinMemoryMb*1024*1024)/2), recommendedResources["container-2"].Target[model.ResourceMemory])
	assert.True(t, gotRecommended)
}

func TestControlledResourcesFiltered(t *testing.T) {
	constEstimator := NewConstEstimator(model.Resources{
		model.ResourceCPU:    model.CPUAmountFromCores(0.001),
		model.ResourceMemory: model.MemoryAmountFromBytes(1e6),
	})
	recommender := podResourceRecommender{
		constEstimator,
		constEstimator,
		constEstimator}

	containerName := "container-1"
	containerNameToAggregateStateMap := model.ContainerNameToAggregateStateMap{
		containerName: &model.AggregateContainerState{
			ControlledResources: &[]model.ResourceName{model.ResourceMemory},
		},
	}

	recommendedResources, gotRecommended := recommender.GetRecommendedPodResources(containerNameToAggregateStateMap)
	assert.Contains(t, recommendedResources[containerName].Target, model.ResourceMemory)
	assert.Contains(t, recommendedResources[containerName].LowerBound, model.ResourceMemory)
	assert.Contains(t, recommendedResources[containerName].UpperBound, model.ResourceMemory)
	assert.NotContains(t, recommendedResources[containerName].Target, model.ResourceCPU)
	assert.NotContains(t, recommendedResources[containerName].LowerBound, model.ResourceCPU)
	assert.NotContains(t, recommendedResources[containerName].UpperBound, model.ResourceCPU)
	assert.True(t, gotRecommended)
}

func TestControlledResourcesFilteredDefault(t *testing.T) {
	constEstimator := NewConstEstimator(model.Resources{
		model.ResourceCPU:    model.CPUAmountFromCores(0.001),
		model.ResourceMemory: model.MemoryAmountFromBytes(1e6),
	})
	recommender := podResourceRecommender{
		constEstimator,
		constEstimator,
		constEstimator}

	containerName := "container-1"
	containerNameToAggregateStateMap := model.ContainerNameToAggregateStateMap{
		containerName: &model.AggregateContainerState{
			ControlledResources: &[]model.ResourceName{model.ResourceMemory, model.ResourceCPU},
		},
	}

	recommendedResources, gotRecommended := recommender.GetRecommendedPodResources(containerNameToAggregateStateMap)
	assert.Contains(t, recommendedResources[containerName].Target, model.ResourceMemory)
	assert.Contains(t, recommendedResources[containerName].LowerBound, model.ResourceMemory)
	assert.Contains(t, recommendedResources[containerName].UpperBound, model.ResourceMemory)
	assert.Contains(t, recommendedResources[containerName].Target, model.ResourceCPU)
	assert.Contains(t, recommendedResources[containerName].LowerBound, model.ResourceCPU)
	assert.Contains(t, recommendedResources[containerName].UpperBound, model.ResourceCPU)
	assert.True(t, gotRecommended)
}

func TestScaledResourceEstimatorResources(t *testing.T) {
	targetEstimator := NewScaleValueEstimator(2, 2)
	lowerBoundEstimator := NewScaleValueEstimator(1, 1)
	upperBoundEstimator := NewScaleValueEstimator(4, 4)
	recommender := podResourceRecommender{
		targetEstimator,
		lowerBoundEstimator,
		upperBoundEstimator,
	}

	containerName := "container-1"
	cpuSample := model.ContainerUsageSample{
		MeasureStart: testTimestamp,
		Usage:        model.CPUAmountFromCores(1.0),
		Request:      testRequest[model.ResourceCPU],
		Resource:     model.ResourceCPU,
	}

	lastCPULocalMaxima := model.ContainerUsageSample{
		MeasureStart: testTimestamp,
		Usage:        model.CPUAmountFromCores(1.2),
		Request:      testRequest[model.ResourceCPU],
		Resource:     model.ResourceCPU,
	}

	memSample := model.ContainerUsageSample{
		MeasureStart: testTimestamp,
		Usage:        model.MemoryAmountFromBytes(1000000),
		Request:      testRequest[model.ResourceMemory],
		Resource:     model.ResourceMemory,
	}

	lastMemLocalMaxima := model.ContainerUsageSample{
		MeasureStart: testTimestamp,
		Usage:        model.MemoryAmountFromBytes(1000005),
		Request:      testRequest[model.ResourceMemory],
		Resource:     model.ResourceMemory,
	}
	containerNameToAggregateStateMap := model.ContainerNameToAggregateStateMap{
		containerName: &model.AggregateContainerState{
			ControlledResources:      &[]model.ResourceName{model.ResourceMemory, model.ResourceCPU},
			CurrentCtrCPUUsage:       &cpuSample,
			CurrentCtrMemUsage:       &memSample,
			LastCtrCPULocalMaxima:    &lastCPULocalMaxima,
			LastCtrMemoryLocalMaxima: &lastMemLocalMaxima,
			RestartBudget:            3,
		},
	}

	recommendedResources, gotRecommended := recommender.GetRecommendedPodResources(containerNameToAggregateStateMap)
	assert.Contains(t, recommendedResources[containerName].Target, model.ResourceMemory)
	assert.Contains(t, recommendedResources[containerName].LowerBound, model.ResourceMemory)
	assert.Contains(t, recommendedResources[containerName].UpperBound, model.ResourceMemory)
	assert.Contains(t, recommendedResources[containerName].Target, model.ResourceCPU)
	assert.Contains(t, recommendedResources[containerName].LowerBound, model.ResourceCPU)
	assert.Contains(t, recommendedResources[containerName].UpperBound, model.ResourceCPU)
	assert.True(t, gotRecommended)
}
