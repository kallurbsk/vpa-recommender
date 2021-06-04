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
	"time"
)

// AggregationsConfig is used to configure aggregation behaviour.
type AggregationsConfig struct {
	// MemoryAggregationInterval is the length of a single interval, for
	// which the peak memory usage is computed.
	// Memory usage peaks are aggregated in multiples of this interval. In other words
	// there is one memory usage sample per interval (the maximum usage over that
	// interval).
	// Currently it is used as more of a counter for keeping the aggregate container state
	// relevant with the amount of samples gone in
	// MemoryAggregationInterval time.Duration
	// DaysToPreserveContainerState is the total number of days to preserve the
	// aggregate container state
	// DaysToPreserveContainerState int64
	// ThresholdMonitorTimeWindow is the time window for setting the local maxima of usage
	// of resources. This window helps in calculating a local maxima within it for a given
	// resource which inturn is used during the scale down to not recommend resources below it
	ThresholdMonitorTimeWindow time.Duration
	// ThresholdScaleDown is the scale down value multiple of the current usage of resource
	// above which the scale value is not altered
	ThresholdScaleDown float64
	// ThresholdScaleUp is the scale up value multiple of the current usage of resource
	// below which the scale value is not altered
	ThresholdScaleUp float64
	// ScaleDownSafetyFactor is the scale down safety lower margin of the current usage
	// which will act as the least value recommended during scale down
	ScaleDownSafetyFactor float64
	// ScaleUpFactor is the actual multiple by which the scale up recommendation of the
	// resource based on current usage is recommended
	ScaleUpFactor float64
	// ThresholdNumCrashes is the minimum number of crashes that is withstood before doubling
	// both CPU and Memory resource based on the current usage
	ThresholdNumCrashes int
	// UpdateVpaStatus flag suggests if whether to update the VPA status with the newer recommendation
	// value or just keep it a read only in annotations
	UpdateVpaStatus bool
}

const (
	// DefaultThresholdMonitorTimeWindow is the default time window to get local maxima of CPU and memory usage till the curren time
	DefaultThresholdMonitorTimeWindow = time.Minute * 30
	// DefaultThresholdScaleUp is the default threshold value beyond which VPA scale up should kick in
	DefaultThresholdScaleUp = 0.7
	// DefaultThresholdScaleDown is the default threshold value beyond which VPA scale down should kick in
	DefaultThresholdScaleDown = 0.3
	// DefaultScaleDownSafetyFactor is the default factor by which VPA recommender should suggest scale down based on current usage
	DefaultScaleDownSafetyFactor = 1.2
	// DefaultScaleUpFactor is the default scaling factor which needs to applied for resource scale up
	DefaultScaleUpFactor = 2.0
	// DefaultThresholdNumCrashes is the default total number of crashes to withstand before doubling both CPU and memory irrespective of usage
	DefaultThresholdNumCrashes = 3
	// UpdateVpaStatus is set to false by default. This enables read only mode for the VPA recommender and prevents updating details in status
	DefaultUpdateVpaStatus = false
)

// GetMemoryAggregationWindowLength returns the total length of the memory usage history aggregated by VPA.
// func (a *AggregationsConfig) GetMemoryAggregationWindowLength() time.Duration {
// 	return a.MemoryAggregationInterval * time.Duration(a.DaysToPreserveContainerState)
// }

// NewAggregationsConfig creates a new AggregationsConfig based on the supplied parameters and default values.
func NewAggregationsConfig(
	//MemoryAggregationInterval time.Duration,
	//DaysToPreserveContainerState int64,
	thresholdMonitorTimeWindow time.Duration,
	thresholdScaleUp float64,
	thresholdScaleDown float64,
	ScaleDownSafetyFactor float64,
	ScaleUpFactor float64,
	thresholdNumCrashes int,
	updateVpaStatus bool) *AggregationsConfig {

	a := &AggregationsConfig{
		// MemoryAggregationInterval:    MemoryAggregationInterval,
		// DaysToPreserveContainerState: DaysToPreserveContainerState,
		ThresholdMonitorTimeWindow: thresholdMonitorTimeWindow,
		ThresholdScaleUp:           thresholdScaleUp,
		ThresholdScaleDown:         thresholdScaleDown,
		ScaleDownSafetyFactor:      ScaleDownSafetyFactor,
		ScaleUpFactor:              ScaleUpFactor,
		ThresholdNumCrashes:        thresholdNumCrashes,
		UpdateVpaStatus:            updateVpaStatus,
	}

	return a
}

var aggregationsConfig *AggregationsConfig

// GetAggregationsConfig gets the aggregations config. Initializes to default values if not initialized already.
func GetAggregationsConfig() *AggregationsConfig {
	if aggregationsConfig == nil {
		aggregationsConfig = NewAggregationsConfig(DefaultThresholdMonitorTimeWindow, DefaultThresholdScaleUp, DefaultThresholdScaleDown, DefaultScaleDownSafetyFactor, DefaultScaleUpFactor, DefaultThresholdNumCrashes, DefaultUpdateVpaStatus)

	}

	return aggregationsConfig
}

// InitializeAggregationsConfig initializes the global aggregations configuration. Not thread-safe.
func InitializeAggregationsConfig(config *AggregationsConfig) {
	aggregationsConfig = config
}
