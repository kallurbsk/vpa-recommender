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

package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	apiv1 "k8s.io/api/core/v1"
	labels "k8s.io/apimachinery/pkg/labels"

	vpa_types "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
)

var (
	testPodID1       = PodID{"namespace-1", "pod-1"}
	testPodID2       = PodID{"namespace-1", "pod-2"}
	testContainerID1 = ContainerID{testPodID1, "container-1"}
	testRequest      = Resources{
		ResourceCPU:    CPUAmountFromCores(3.14),
		ResourceMemory: MemoryAmountFromBytes(3.14e9),
	}
	timeLayout       = "2006-01-02 15:04:05"
	testTimestamp, _ = time.Parse(timeLayout, "2017-04-18 17:35:05")
)

func addTestCPUSample(cluster *ClusterState, container ContainerID, cpuCores float64) error {
	sample := ContainerUsageSampleWithKey{
		Container: container,
		ContainerUsageSample: ContainerUsageSample{
			MeasureStart: testTimestamp,
			Usage:        CPUAmountFromCores(cpuCores),
			Request:      testRequest[ResourceCPU],
			Resource:     ResourceCPU,
		},
	}
	return cluster.AddSample(&sample)
}

func addTestMemorySample(cluster *ClusterState, container ContainerID, memoryBytes float64) error {
	sample := ContainerUsageSampleWithKey{
		Container: container,
		ContainerUsageSample: ContainerUsageSample{
			MeasureStart: testTimestamp,
			Usage:        MemoryAmountFromBytes(memoryBytes),
			Request:      testRequest[ResourceMemory],
			Resource:     ResourceMemory,
		},
	}
	return cluster.AddSample(&sample)
}

func TestAggregateContainerStateLocalMaximaValues(t *testing.T) {
	cs := NewAggregateContainerState()

	// Check if CPU Local Maxima is set properly
	cs.LastCPULocalMaximaRecordedTime = time.Now()
	cpuSample := ContainerUsageSample{
		MeasureStart: testTimestamp,
		Usage:        CPUAmountFromCores(1.0),
		Request:      testRequest[ResourceCPU],
		Resource:     ResourceCPU,
	}
	cs.addCPULocalMaxima(&cpuSample)
	assert.Equal(t, cpuSample.MeasureStart, cs.LastCtrCPULocalMaxima.MeasureStart)
	assert.Equal(t, cpuSample.Request, cs.LastCtrCPULocalMaxima.Request)
	assert.Equal(t, cpuSample.Usage, cs.LastCtrCPULocalMaxima.Usage)
	assert.Equal(t, cpuSample.Resource, cs.LastCtrCPULocalMaxima.Resource)

	// Check if Memory Local Maxima is set properly
	memSample := ContainerUsageSample{
		MeasureStart: testTimestamp,
		Usage:        MemoryAmountFromBytes(1000000),
		Request:      testRequest[ResourceMemory],
		Resource:     ResourceMemory,
	}
	cs.LastMemLocalMaximaRecordedTime = time.Now()
	cs.addMemoryLocalMaxima(&memSample)
	assert.Equal(t, memSample.MeasureStart, cs.LastCtrMemoryLocalMaxima.MeasureStart)
	assert.Equal(t, memSample.Request, cs.LastCtrMemoryLocalMaxima.Request)
	assert.Equal(t, memSample.Usage, cs.LastCtrMemoryLocalMaxima.Usage)
	assert.Equal(t, memSample.Resource, cs.LastCtrMemoryLocalMaxima.Resource)

	// Check if diffDuration > 30 minutes resets CPU Local Maxima
	cs.LastCPULocalMaximaRecordedTime = cs.LastCPULocalMaximaRecordedTime.Add(time.Duration(-50) * time.Minute)
	cs.addCPULocalMaxima(&cpuSample)
	assert.Equal(t, time.Time{}, cs.LastCtrCPULocalMaxima.MeasureStart)
	assert.Equal(t, 0, int(cs.LastCtrCPULocalMaxima.Request))
	assert.Equal(t, 0, int(cs.LastCtrCPULocalMaxima.Usage))
	assert.Equal(t, "cpu", string(cs.LastCtrCPULocalMaxima.Resource))

	// Check if diffDuration > 30 minutes resets Memory Local Maxima
	cs.LastMemLocalMaximaRecordedTime = cs.LastMemLocalMaximaRecordedTime.Add(time.Duration(-50) * time.Minute)
	cs.addMemoryLocalMaxima(&memSample)
	assert.Equal(t, time.Time{}, cs.LastCtrMemoryLocalMaxima.MeasureStart)
	assert.Equal(t, 0, int(cs.LastCtrMemoryLocalMaxima.Request))
	assert.Equal(t, 0, int(cs.LastCtrMemoryLocalMaxima.Usage))
	assert.Equal(t, "memory", string(cs.LastCtrMemoryLocalMaxima.Resource))

}

func TestAggregateStateByContainerName(t *testing.T) {
	cluster := NewClusterState()
	cluster.AddOrUpdatePod(testPodID1, testLabels, apiv1.PodRunning)
	otherLabels := labels.Set{"label-2": "value-2"}
	cluster.AddOrUpdatePod(testPodID2, otherLabels, apiv1.PodRunning)

	// Create 4 containers: 2 with the same name and 2 with different names.
	containers := []ContainerID{
		{testPodID1, "app-A"},
		{testPodID1, "app-B"},
		{testPodID2, "app-A"},
		{testPodID2, "app-C"},
	}
	for _, c := range containers {
		assert.NoError(t, cluster.AddOrUpdateContainer(c, testRequest, restartCount, ctrReady, ctrCurState))
	}

	// Add CPU usage samples to all containers.
	assert.NoError(t, addTestCPUSample(cluster, containers[0], 1.0)) // app-A
	assert.NoError(t, addTestCPUSample(cluster, containers[1], 5.0)) // app-B
	assert.NoError(t, addTestCPUSample(cluster, containers[2], 3.0)) // app-A
	assert.NoError(t, addTestCPUSample(cluster, containers[3], 5.0)) // app-C
	// Add Memory usage samples to all containers.
	assert.NoError(t, addTestMemorySample(cluster, containers[0], 2e9))  // app-A
	assert.NoError(t, addTestMemorySample(cluster, containers[1], 10e9)) // app-B
	assert.NoError(t, addTestMemorySample(cluster, containers[2], 4e9))  // app-A
	assert.NoError(t, addTestMemorySample(cluster, containers[3], 10e9)) // app-C

	// Build the AggregateContainerStateMap.
	aggregateResources := AggregateStateByContainerName(cluster.aggregateStateMap)
	assert.Contains(t, aggregateResources, "app-A")
	assert.Contains(t, aggregateResources, "app-B")
	assert.Contains(t, aggregateResources, "app-C")

}

func TestAggregateContainerStateSetCurrentUsage(t *testing.T) {
	cs := NewAggregateContainerState()

	// Check if CPU Current Usage is set properly
	cpuSample := ContainerUsageSample{
		MeasureStart: testTimestamp,
		Usage:        CPUAmountFromCores(1.0),
		Request:      testRequest[ResourceCPU],
		Resource:     ResourceCPU,
	}
	cs.SetCPUUsage(&cpuSample)
	assert.Equal(t, cpuSample.MeasureStart, cs.CurrentCtrCPUUsage.MeasureStart)
	assert.Equal(t, cpuSample.Request, cs.CurrentCtrCPUUsage.Request)
	assert.Equal(t, cpuSample.Usage, cs.CurrentCtrCPUUsage.Usage)
	assert.Equal(t, cpuSample.Resource, cs.CurrentCtrCPUUsage.Resource)

	// Check if Memory Current Usage is set properly
	memSample := ContainerUsageSample{
		MeasureStart: testTimestamp,
		Usage:        MemoryAmountFromBytes(1000000),
		Request:      testRequest[ResourceMemory],
		Resource:     ResourceMemory,
	}

	cs.SetMemUsage(&memSample)
	assert.Equal(t, memSample.MeasureStart, cs.CurrentCtrMemUsage.MeasureStart)
	assert.Equal(t, memSample.Request, cs.CurrentCtrMemUsage.Request)
	assert.Equal(t, memSample.Usage, cs.CurrentCtrMemUsage.Usage)
	assert.Equal(t, memSample.Resource, cs.CurrentCtrMemUsage.Resource)
}

func TestUpdateFromPolicyScalingMode(t *testing.T) {
	scalingModeAuto := vpa_types.ContainerScalingModeAuto
	scalingModeOff := vpa_types.ContainerScalingModeOff
	testCases := []struct {
		name     string
		policy   *vpa_types.ContainerResourcePolicy
		expected *vpa_types.ContainerScalingMode
	}{
		{
			name: "Explicit auto scaling mode",
			policy: &vpa_types.ContainerResourcePolicy{
				Mode: &scalingModeAuto,
			},
			expected: &scalingModeAuto,
		}, {
			name: "Off scaling mode",
			policy: &vpa_types.ContainerResourcePolicy{
				Mode: &scalingModeOff,
			},
			expected: &scalingModeOff,
		}, {
			name:     "No mode specified - default to Auto",
			policy:   &vpa_types.ContainerResourcePolicy{},
			expected: &scalingModeAuto,
		}, {
			name:     "Nil policy - default to Auto",
			policy:   nil,
			expected: &scalingModeAuto,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cs := NewAggregateContainerState()
			cs.UpdateFromPolicy(tc.policy)
			assert.Equal(t, tc.expected, cs.GetScalingMode())
		})
	}
}

func TestUpdateFromPolicyControlledResources(t *testing.T) {
	testCases := []struct {
		name     string
		policy   *vpa_types.ContainerResourcePolicy
		expected []ResourceName
	}{
		{
			name: "Explicit ControlledResources",
			policy: &vpa_types.ContainerResourcePolicy{
				ControlledResources: &[]apiv1.ResourceName{apiv1.ResourceCPU, apiv1.ResourceMemory},
			},
			expected: []ResourceName{ResourceCPU, ResourceMemory},
		}, {
			name: "Empty ControlledResources",
			policy: &vpa_types.ContainerResourcePolicy{
				ControlledResources: &[]apiv1.ResourceName{},
			},
			expected: []ResourceName{},
		}, {
			name: "ControlledResources with one resource",
			policy: &vpa_types.ContainerResourcePolicy{
				ControlledResources: &[]apiv1.ResourceName{apiv1.ResourceMemory},
			},
			expected: []ResourceName{ResourceMemory},
		}, {
			name:     "No ControlledResources specified - used default",
			policy:   &vpa_types.ContainerResourcePolicy{},
			expected: []ResourceName{ResourceCPU, ResourceMemory},
		}, {
			name:     "Nil policy - use default",
			policy:   nil,
			expected: []ResourceName{ResourceCPU, ResourceMemory},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cs := NewAggregateContainerState()
			cs.UpdateFromPolicy(tc.policy)
			assert.Equal(t, tc.expected, cs.GetControlledResources())
		})
	}
}
