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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var (
	TestRequest = Resources{
		ResourceCPU:    CPUAmountFromCores(2.3),
		ResourceMemory: MemoryAmountFromBytes(5e8),
	}
)

const (
	kb = 1024
	mb = 1024 * kb
)

func newUsageSample(timestamp time.Time, usage int64, resource ResourceName) *ContainerUsageSample {
	return &ContainerUsageSample{
		MeasureStart: timestamp,
		Usage:        ResourceAmount(usage),
		Request:      TestRequest[resource],
		Resource:     resource,
	}
}

type ContainerTest struct {
	aggregateContainerState *AggregateContainerState
	container               *ContainerState
}

func newContainerTest() ContainerTest {
	currentCPUUsage := ContainerUsageSample{
		MeasureStart: testTimestamp,
		Usage:        CPUAmountFromCores(1.0),
		Request:      testRequest[ResourceCPU],
		Resource:     ResourceCPU,
	}
	cpuLocalMaxima := ContainerUsageSample{
		MeasureStart: testTimestamp,
		Usage:        CPUAmountFromCores(1.2),
		Request:      testRequest[ResourceCPU],
		Resource:     ResourceCPU,
	}
	currentMemUsage := ContainerUsageSample{
		MeasureStart: testTimestamp,
		Usage:        MemoryAmountFromBytes(1000000),
		Request:      testRequest[ResourceMemory],
		Resource:     ResourceMemory,
	}
	memLocalMaxima := ContainerUsageSample{
		MeasureStart: testTimestamp,
		Usage:        MemoryAmountFromBytes(1000006),
		Request:      testRequest[ResourceMemory],
		Resource:     ResourceMemory,
	}
	aggregateContainerState := &AggregateContainerState{
		CurrentCtrCPUUsage:             &currentCPUUsage,
		CurrentCtrMemUsage:             &currentMemUsage,
		TimeWindowForLocalMaxima:       time.Duration(30 * time.Minute),
		LastCPULocalMaximaRecordedTime: time.Now(),
		LastMemLocalMaximaRecordedTime: time.Now(),
		LastCtrCPULocalMaxima:          &cpuLocalMaxima,
		LastCtrMemoryLocalMaxima:       &memLocalMaxima,
	}
	container := &ContainerState{
		Request:    TestRequest,
		aggregator: aggregateContainerState,
	}
	return ContainerTest{

		aggregateContainerState: aggregateContainerState,
		container:               container,
	}
}

// Add 6 usage samples (3 valid, 3 invalid) to a container.
// Verifies that invalid samples (out-of-order or negative usage) are ignored.
func TestAggregateContainerUsageSamples(t *testing.T) {
	test := newContainerTest()
	c := test.container
	// Verify that CPU measures are added to the CPU histogram.
	// The weight should be equal to the current request.

	// Add three CPU and memory usage samples.
	assert.True(t, c.AddSample(newUsageSample(
		testTimestamp, 3140, ResourceCPU)))
	assert.True(t, c.AddSample(newUsageSample(
		testTimestamp, 5, ResourceMemory)))

	assert.True(t, c.AddSample(newUsageSample(
		testTimestamp.Add(1*time.Minute), 6280, ResourceCPU)))
	assert.True(t, c.AddSample(newUsageSample(
		testTimestamp.Add(1*time.Minute), 10, ResourceMemory)))

	assert.True(t, c.AddSample(newUsageSample(
		testTimestamp.Add(2*time.Minute), 1570, ResourceCPU)))
	assert.True(t, c.AddSample(newUsageSample(
		testTimestamp.Add(2*time.Minute), 2, ResourceMemory)))

	// Discard invalid samples.
	assert.False(t, c.AddSample(newUsageSample( // Out of order sample.
		testTimestamp.Add(-60*time.Minute), 1000, ResourceCPU)))
	assert.False(t, c.AddSample(newUsageSample( // Negative CPU usage.
		testTimestamp.Add(3*time.Minute), -1000, ResourceCPU)))
	assert.False(t, c.AddSample(newUsageSample( // Negative memory usage.
		testTimestamp.Add(3*time.Minute), -1000, ResourceMemory)))
}

func TestRecordOOMIncreasedByBumpUp(t *testing.T) {
	test := newContainerTest()
	assert.NoError(t, test.container.RecordOOM(testTimestamp, ResourceAmount(1000*mb)))
}

func TestRecordRestartCount(t *testing.T) {
	test := newContainerTest()
	assert.Equal(t, true, test.container.RecordRestartCount(3, 3))
}

func TestRecordCurrentContainerState(t *testing.T) {
	test := newContainerTest()
	ctrCurState := ContainerCurrentState{
		Reason: "Testing",
	}
	assert.Equal(t, true, test.container.RecordCurrentContainerState(ctrCurState))
}

// func TestRecordOOMDontRunAway(t *testing.T) {
// 	test := newContainerTest()
// 	memoryAggregationWindowEnd := testTimestamp.Add(GetAggregationsConfig().MemoryAggregationInterval)

// 	// Bump Up factor is 20%.
// 	test.mockMemoryHistogram.On("AddSample", 1200.0*mb, 1.0, memoryAggregationWindowEnd)
// 	assert.NoError(t, test.container.RecordOOM(testTimestamp, ResourceAmount(1000*mb)))

// 	// new smaller OOMs don't influence the sample value (oomPeak)
// 	assert.NoError(t, test.container.RecordOOM(testTimestamp, ResourceAmount(999*mb)))
// 	assert.NoError(t, test.container.RecordOOM(testTimestamp, ResourceAmount(999*mb)))

// 	test.mockMemoryHistogram.On("SubtractSample", 1200.0*mb, 1.0, memoryAggregationWindowEnd)
// 	test.mockMemoryHistogram.On("AddSample", 2400.0*mb, 1.0, memoryAggregationWindowEnd)
// 	// a larger OOM should increase the sample value
// 	assert.NoError(t, test.container.RecordOOM(testTimestamp, ResourceAmount(2000*mb)))
// }

// func TestRecordOOMIncreasedByMin(t *testing.T) {
// 	test := newContainerTest()
// 	memoryAggregationWindowEnd := testTimestamp.Add(GetAggregationsConfig().MemoryAggregationInterval)
// 	// Min grow by 100Mb.
// 	test.mockMemoryHistogram.On("AddSample", 101.0*mb, 1.0, memoryAggregationWindowEnd)

// 	assert.NoError(t, test.container.RecordOOM(testTimestamp, ResourceAmount(1*mb)))
// }

// func TestRecordOOMMaxedWithKnownSample(t *testing.T) {
// 	test := newContainerTest()
// 	memoryAggregationWindowEnd := testTimestamp.Add(GetAggregationsConfig().MemoryAggregationInterval)

// 	test.mockMemoryHistogram.On("AddSample", 3000.0*mb, 1.0, memoryAggregationWindowEnd)
// 	assert.True(t, test.container.AddSample(newUsageSample(testTimestamp, 3000*mb, ResourceMemory)))

// 	// Last known sample is higher than request, so it is taken.
// 	test.mockMemoryHistogram.On("SubtractSample", 3000.0*mb, 1.0, memoryAggregationWindowEnd)
// 	test.mockMemoryHistogram.On("AddSample", 3600.0*mb, 1.0, memoryAggregationWindowEnd)

// 	assert.NoError(t, test.container.RecordOOM(testTimestamp, ResourceAmount(1000*mb)))
// }

// func TestRecordOOMDiscardsOldSample(t *testing.T) {
// 	test := newContainerTest()
// 	assert.True(t, test.container.AddSample(newUsageSample(testTimestamp, 1000*mb, ResourceMemory)))

// 	// OOM is stale, mem not changed.
// 	assert.Error(t, test.container.RecordOOM(testTimestamp.Add(-30*time.Hour), ResourceAmount(1000*mb)))
// }
