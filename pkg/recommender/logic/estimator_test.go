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

func TestScaledResourceEstimator(t *testing.T) {
	scaledEstimator := &scaledResourceEstimator{
		cpuScaleValue: 2.0,
		memScaleValue: 2.0,
	}

	acs := model.NewAggregateContainerState()
	// Add 9 CPU samples at the frequency of 1/(2 mins).
	timestamp := anyTime
	for i := 1; i <= 9; i++ {
		acs.AddSample(&model.ContainerUsageSample{
			timestamp, model.CPUAmountFromCores(1.0), testRequest[model.ResourceCPU], model.ResourceCPU})
		timestamp = timestamp.Add(time.Minute * 2)
	}

	acs.SetCPUUsage(&model.ContainerUsageSample{
		timestamp, model.CPUAmountFromCores(6), testRequest[model.ResourceCPU], model.ResourceCPU})

	acs.SetMemUsage(&model.ContainerUsageSample{
		timestamp, model.MemoryAmountFromBytes(3.14e8), testRequest[model.ResourceMemory], model.ResourceMemory})

	acs.LastCtrCPULocalMaxima = &model.ContainerUsageSample{
		timestamp, model.CPUAmountFromCores(4.0), testRequest[model.ResourceCPU], model.ResourceCPU}

	resourceEstimation, _ := scaledEstimator.GetResourceEstimation(acs)
	assert.Equal(t, 12000, int(model.CoresFromCPUAmount(resourceEstimation[model.ResourceCPU])))

	acs.LastCtrMemoryLocalMaxima = &model.ContainerUsageSample{
		timestamp, model.MemoryAmountFromBytes(3.14e9), testRequest[model.ResourceMemory], model.ResourceMemory}

	resourceEstimation, _ = scaledEstimator.GetResourceEstimation(acs)
	assert.Equal(t, 3.14e8, model.BytesFromMemoryAmount(resourceEstimation[model.ResourceMemory]))
}
