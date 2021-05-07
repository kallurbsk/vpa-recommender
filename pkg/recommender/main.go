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

package main

import (
	"flag"
	"os"
	"time"

	"github.com/gardener/vpa-recommender/pkg/recommender/model"

	"github.com/gardener/vpa-recommender/pkg/recommender/input/history"
	"github.com/gardener/vpa-recommender/pkg/recommender/routines"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/autoscaler/vertical-pod-autoscaler/common"
	"k8s.io/autoscaler/vertical-pod-autoscaler/pkg/utils/metrics"
	metrics_quality "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/utils/metrics/quality"
	metrics_recommender "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/utils/metrics/recommender"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	kube_flag "k8s.io/component-base/cli/flag"
	"k8s.io/klog"
)

var (
	metricsFetcherInterval = flag.Duration("recommender-interval", 1*time.Minute, `How often metrics should be fetched`)
	checkpointsGCInterval  = flag.Duration("checkpoints-gc-interval", 10*time.Minute, `How often orphaned checkpoints should be garbage collected`)
	prometheusAddress      = flag.String("prometheus-address", "", `Where to reach for Prometheus metrics`)
	prometheusJobName      = flag.String("prometheus-cadvisor-job-name", "kubernetes-cadvisor", `Name of the prometheus job name which scrapes the cAdvisor metrics`)
	address                = flag.String("address", ":8942", "The address to expose Prometheus metrics.")
	kubeApiQps             = flag.Float64("kube-api-qps", 5.0, `QPS limit when making requests to Kubernetes apiserver`)
	kubeApiBurst           = flag.Float64("kube-api-burst", 10.0, `QPS burst limit when making requests to Kubernetes apiserver`)
	storage                = flag.String("storage", "", `Specifies storage mode. Supported values: prometheus, checkpoint (default)`)
	// prometheus history provider configs
	historyLength       = flag.String("history-length", "8d", `How much time back prometheus have to be queried to get historical metrics`)
	historyResolution   = flag.String("history-resolution", "1h", `Resolution at which Prometheus is queried for historical metrics`)
	queryTimeout        = flag.String("prometheus-query-timeout", "5m", `How long to wait before killing long queries`)
	podLabelPrefix      = flag.String("pod-label-prefix", "pod_label_", `Which prefix to look for pod labels in metrics`)
	podLabelsMetricName = flag.String("metric-for-pod-labels", "up{job=\"kubernetes-pods\"}", `Which metric to look for pod labels in metrics`)
	podNamespaceLabel   = flag.String("pod-namespace-label", "kubernetes_namespace", `Label name to look for container names`)
	podNameLabel        = flag.String("pod-name-label", "kubernetes_pod_name", `Label name to look for container names`)
	ctrNamespaceLabel   = flag.String("container-namespace-label", "namespace", `Label name to look for container names`)
	ctrPodNameLabel     = flag.String("container-pod-name-label", "pod_name", `Label name to look for container names`)
	ctrNameLabel        = flag.String("container-name-label", "name", `Label name to look for container names`)
	vpaObjectNamespace  = flag.String("vpa-object-namespace", apiv1.NamespaceAll, "Namespace to search for VPA objects and pod stats. Empty means all namespaces will be used.")
	// VPA Recommender configurations
	scaleDownSafetyMargin = flag.Float64("scale-down-safety-margin", 1.2, `Factor by which VPA recommender should suggest scale down based on current usage`)
	thresholdScaleUp      = flag.Float64("threshold-scale-up", 0.75, "threshold value beyond which VPA scale up should kick in")
	thresholdScaleDown    = flag.Float64("threshold-scale-down", 0.3, "threshold value below which VPA scale down should kick in")
	updateVpaStatus       = flag.Bool("update-vpa-status", false, "If the VPA status should not be updated but kept as read only set to false else true")
	allowOutsideClient    = flag.Bool("allow-outside-client", false, "Allowing the cluster access to run recommender from outside the cluster")
)

// Aggregation configuration flags
var (
	memoryAggregationInterval    = flag.Duration("memory-aggregation-interval", model.DefaultMemoryAggregationInterval, `The length of the interval for which the current aggregate container state has to be kept alive`)
	daysToPreserveContainerState = flag.Int64("days-to-preserve-container-state", model.DefaultDaysToPreserveContainerState, `Total number of days for which the recorded aggregate container state has to be kept alive`)
	thresholdMonitorTimeWindow   = flag.Duration("threshold-monitor-time-window", 30*time.Minute, `Time window to get local maxima of CPU and memory usage till the curren time`)
	scaleUpMultiple              = flag.Float64("scale-up-multiple", model.DefaultScaleUpMultiple, "Scaling factor which needs to applied for resource scale up")
	thresholdNumCrashes          = flag.Int("threshold-num-crashes", model.DefaultThresholdNumCrashes, "Total number of crashes to withstand before doubling both CPU and memory irrespective of usage")
)

func main() {
	klog.InitFlags(nil)
	kube_flag.InitFlags()
	klog.V(1).Infof("Vertical Pod Autoscaler %s Recommender", common.VerticalPodAutoscalerVersion)

	config := createKubeConfig(float32(*kubeApiQps), int(*kubeApiBurst))

	model.InitializeAggregationsConfig(
		model.NewAggregationsConfig(
			*memoryAggregationInterval,
			*daysToPreserveContainerState,
			*thresholdMonitorTimeWindow,
			*thresholdScaleUp,
			*thresholdScaleDown,
			*scaleDownSafetyMargin,
			*scaleUpMultiple,
			*thresholdNumCrashes,
			*updateVpaStatus))

	healthCheck := metrics.NewHealthCheck(*metricsFetcherInterval*5, true)
	metrics.Initialize(*address, healthCheck)
	metrics_recommender.Register()
	metrics_quality.Register()

	useCheckpoints := *storage != "prometheus"
	recommender := routines.NewRecommender(config, *checkpointsGCInterval, useCheckpoints, *vpaObjectNamespace)

	promQueryTimeout, err := time.ParseDuration(*queryTimeout)
	if err != nil {
		klog.Fatalf("Could not parse --prometheus-query-timeout as a time.Duration: %v", err)
	}

	if useCheckpoints {
		recommender.GetClusterStateFeeder().InitFromCheckpoints()
	} else {
		config := history.PrometheusHistoryProviderConfig{
			Address:                *prometheusAddress,
			QueryTimeout:           promQueryTimeout,
			HistoryLength:          *historyLength,
			HistoryResolution:      *historyResolution,
			PodLabelPrefix:         *podLabelPrefix,
			PodLabelsMetricName:    *podLabelsMetricName,
			PodNamespaceLabel:      *podNamespaceLabel,
			PodNameLabel:           *podNameLabel,
			CtrNamespaceLabel:      *ctrNamespaceLabel,
			CtrPodNameLabel:        *ctrPodNameLabel,
			CtrNameLabel:           *ctrNameLabel,
			CadvisorMetricsJobName: *prometheusJobName,
			Namespace:              *vpaObjectNamespace,
		}
		provider, err := history.NewPrometheusHistoryProvider(config)
		if err != nil {
			klog.Fatalf("Could not initialize history provider: %v", err)
		}
		recommender.GetClusterStateFeeder().InitFromHistoryProvider(provider)
	}

	ticker := time.Tick(*metricsFetcherInterval)
	for range ticker {
		recommender.RunOnce()
		healthCheck.UpdateLastActivity()
	}
}

func createKubeConfig(kubeApiQps float32, kubeApiBurst int) *rest.Config {

	// From inside client
	if !*allowOutsideClient {
		config, err := rest.InClusterConfig()
		if err != nil {
			klog.Fatalf("Failed to create config: %v", err)
		}
		config.QPS = kubeApiQps
		config.Burst = kubeApiBurst

		return config
	}

	// From outside client.
	// TODO: Remove the below configuration post testing
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if len(kubeconfigPath) == 0 {
		klog.Fatalf("Failed to get KUBECONFIG os environment variable")
		return nil
	}
	config, err := clientcmd.BuildConfigFromFlags("", *&kubeconfigPath)
	if err != nil {
		klog.Fatalf("Failed to create config: %v", err)
	}

	config.QPS = kubeApiQps
	config.Burst = kubeApiBurst

	return config
}
