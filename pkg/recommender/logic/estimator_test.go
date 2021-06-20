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
	"testing"
	"time"

	model "github.com/gardener/vpa-recommender/pkg/recommender/model"
	"github.com/stretchr/testify/assert"
)

var (
	anyTime     = time.Unix(0, 0)
	testRequest = model.Resources{
		model.ResourceCPU:    model.CPUAmountFromCores(3.14),
		model.ResourceMemory: model.MemoryAmountFromBytes(3.14e9),
	}
)

// Verifies that the MarginEstimator adds margin to the originally
// estimated resources.
func TestMarginEstimator(t *testing.T) {
	// Use 10% margin on top of the recommended resources.
	marginFraction := 0.1
	baseEstimator := NewConstEstimator(model.Resources{
		model.ResourceCPU:    model.CPUAmountFromCores(3.14),
		model.ResourceMemory: model.MemoryAmountFromBytes(3.14e9),
	})
	testedEstimator := &marginEstimator{
		marginFraction: marginFraction,
		baseEstimator:  baseEstimator,
	}
	s := model.NewAggregateContainerState()
	resourceEstimation, _ := testedEstimator.GetResourceEstimation(s)
	assert.Equal(t, 3.14*1.1, model.CoresFromCPUAmount(resourceEstimation[model.ResourceCPU]))
	assert.Equal(t, 3.14e9*1.1, model.BytesFromMemoryAmount(resourceEstimation[model.ResourceMemory]))
}

// Verifies that the MinResourcesEstimator returns at least MinResources.
func TestMinResourcesEstimator(t *testing.T) {

	minResources := model.Resources{
		model.ResourceCPU:    model.CPUAmountFromCores(0.2),
		model.ResourceMemory: model.MemoryAmountFromBytes(4e8),
	}
	baseEstimator := NewConstEstimator(model.Resources{
		model.ResourceCPU:    model.CPUAmountFromCores(3.14),
		model.ResourceMemory: model.MemoryAmountFromBytes(2e7),
	})

	testedEstimator := &minResourcesEstimator{
		minResources:  minResources,
		baseEstimator: baseEstimator,
	}
	s := model.NewAggregateContainerState()
	resourceEstimation, _ := testedEstimator.GetResourceEstimation(s)
	// Original CPU is above min resources
	assert.Equal(t, 3.14, model.CoresFromCPUAmount(resourceEstimation[model.ResourceCPU]))
	// Original Memory is below min resources
	assert.Equal(t, 4e8, model.BytesFromMemoryAmount(resourceEstimation[model.ResourceMemory]))
}

func TestScaleUpResourceEstimator(t *testing.T) {
	scaledEstimator := &scaledResourceEstimator{
		cpuScaleValue: 2.0,
		memScaleValue: 2.0,
	}

	acs := model.NewAggregateContainerState()
	timestamp := anyTime

	acs.AddSample(&model.ContainerUsageSample{
		timestamp, model.CPUAmountFromCores(1.0), testRequest[model.ResourceCPU], model.ResourceCPU})

	acs.SetCPUUsage(&model.ContainerUsageSample{
		timestamp, model.CPUAmountFromCores(6), testRequest[model.ResourceCPU], model.ResourceCPU})

	acs.SetMemUsage(&model.ContainerUsageSample{
		timestamp, model.MemoryAmountFromBytes(3.14e8), testRequest[model.ResourceMemory], model.ResourceMemory})

	acs.LastCtrCPULocalMaxima = &model.ContainerUsageSample{
		timestamp, model.CPUAmountFromCores(4.0), testRequest[model.ResourceCPU], model.ResourceCPU}

	resourceEstimation, gotRecommended := scaledEstimator.GetResourceEstimation(acs)
	assert.Equal(t, 12, int(model.CoresFromCPUAmount(resourceEstimation[model.ResourceCPU])))
	assert.True(t, gotRecommended)

	acs.LastCtrMemoryLocalMaxima = &model.ContainerUsageSample{
		timestamp, model.MemoryAmountFromBytes(3.14e8), testRequest[model.ResourceMemory], model.ResourceMemory}

	resourceEstimation, gotRecommended = scaledEstimator.GetResourceEstimation(acs)
	assert.Equal(t, 3.768e8, model.BytesFromMemoryAmount(resourceEstimation[model.ResourceMemory]))
	assert.True(t, gotRecommended)
}

func TestScaleDownResourceEstimator(t *testing.T) {
	scaledEstimator := &scaledResourceEstimator{
		cpuScaleValue: 2.0,
		memScaleValue: 2.0,
	}

	acs := model.NewAggregateContainerState()
	timestamp := anyTime

	acs.AddSample(&model.ContainerUsageSample{
		timestamp, model.CPUAmountFromCores(1.0), testRequest[model.ResourceCPU], model.ResourceCPU})

	acs.SetCPUUsage(&model.ContainerUsageSample{
		timestamp, model.CPUAmountFromCores(1.1), testRequest[model.ResourceCPU], model.ResourceCPU})

	acs.SetMemUsage(&model.ContainerUsageSample{
		timestamp, model.MemoryAmountFromBytes(3.14e5), testRequest[model.ResourceMemory], model.ResourceMemory})

	acs.LastCtrCPULocalMaxima = &model.ContainerUsageSample{
		timestamp, model.CPUAmountFromCores(4.0), testRequest[model.ResourceCPU], model.ResourceCPU}

	resourceEstimation, gotRecommended := scaledEstimator.GetResourceEstimation(acs)
	assert.Equal(t, 3, int(model.CoresFromCPUAmount(resourceEstimation[model.ResourceCPU])))
	assert.True(t, gotRecommended)

	acs.LastCtrMemoryLocalMaxima = &model.ContainerUsageSample{
		timestamp, model.MemoryAmountFromBytes(3.14e8), testRequest[model.ResourceMemory], model.ResourceMemory}

	resourceEstimation, gotRecommended = scaledEstimator.GetResourceEstimation(acs)
	assert.Equal(t, 3.768e5, model.BytesFromMemoryAmount(resourceEstimation[model.ResourceMemory]))
	assert.True(t, gotRecommended)
}

func TestNoScaleResourceEstimator(t *testing.T) {
	scaledEstimator := &scaledResourceEstimator{
		cpuScaleValue: 2.0,
		memScaleValue: 2.0,
	}

	acs := model.NewAggregateContainerState()
	timestamp := anyTime

	acs.AddSample(&model.ContainerUsageSample{
		timestamp, model.CPUAmountFromCores(1.0), testRequest[model.ResourceCPU], model.ResourceCPU})

	acs.SetCPUUsage(&model.ContainerUsageSample{
		timestamp, model.CPUAmountFromCores(2.0), testRequest[model.ResourceCPU], model.ResourceCPU})

	acs.SetMemUsage(&model.ContainerUsageSample{
		timestamp, model.MemoryAmountFromBytes(2.18e8), testRequest[model.ResourceMemory], model.ResourceMemory})

	acs.LastCtrCPULocalMaxima = &model.ContainerUsageSample{
		timestamp, model.CPUAmountFromCores(3.14), testRequest[model.ResourceCPU], model.ResourceCPU}

	acs.LastCtrMemoryLocalMaxima = &model.ContainerUsageSample{
		timestamp, model.MemoryAmountFromBytes(3.14e9), testRequest[model.ResourceMemory], model.ResourceMemory}

	resourceEstimation, gotRecommended := scaledEstimator.GetResourceEstimation(acs)
	assert.Equal(t, 2, int(model.CoresFromCPUAmount(resourceEstimation[model.ResourceCPU])))
	assert.False(t, gotRecommended)

	resourceEstimation, gotRecommended = scaledEstimator.GetResourceEstimation(acs)
	assert.Equal(t, 2.18e8, model.BytesFromMemoryAmount(resourceEstimation[model.ResourceMemory]))
	assert.False(t, gotRecommended)
}

func TestDoubleResourcesOnCrashLoopBackOff(t *testing.T) {
	scaledEstimator := &scaledResourceEstimator{
		cpuScaleValue: 2.0,
		memScaleValue: 2.0,
	}

	acs := model.NewAggregateContainerState()
	timestamp := anyTime

	acs.AddSample(&model.ContainerUsageSample{
		timestamp, model.CPUAmountFromCores(2.0), testRequest[model.ResourceCPU], model.ResourceCPU})

	acs.SetCPUUsage(&model.ContainerUsageSample{
		timestamp, model.CPUAmountFromCores(3.14), testRequest[model.ResourceCPU], model.ResourceCPU})

	acs.SetMemUsage(&model.ContainerUsageSample{
		timestamp, model.MemoryAmountFromBytes(3.14e9), testRequest[model.ResourceMemory], model.ResourceMemory})

	// Check with restart budget
	acs.RestartBudget = -1

	resourceEstimation, gotRecommended := scaledEstimator.GetResourceEstimation(acs)
	assert.Equal(t, 6, int(model.CoresFromCPUAmount(resourceEstimation[model.ResourceCPU])))
	assert.True(t, gotRecommended)

	resourceEstimation, gotRecommended = scaledEstimator.GetResourceEstimation(acs)
	assert.Equal(t, 6.28e9, model.BytesFromMemoryAmount(resourceEstimation[model.ResourceMemory]))
	assert.True(t, gotRecommended)

	// Container state in crash loop back off
	acs.RestartBudget = 3
	acs.CurrentContainerState.Reason = crashLoopBackOff
	resourceEstimation, gotRecommended = scaledEstimator.GetResourceEstimation(acs)
	assert.Equal(t, 6, int(model.CoresFromCPUAmount(resourceEstimation[model.ResourceCPU])))
	assert.True(t, gotRecommended)

	resourceEstimation, gotRecommended = scaledEstimator.GetResourceEstimation(acs)
	assert.Equal(t, 6.28e9, model.BytesFromMemoryAmount(resourceEstimation[model.ResourceMemory]))
	assert.True(t, gotRecommended)
}
