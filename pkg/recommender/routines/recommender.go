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

package routines

import (
	//"autoscaler/vertical-pod-autoscaler/pkg/recommender/logic"
	//"autoscaler/vertical-pod-autoscaler/pkg/recommender/model"
	"context"
	"encoding/json"
	"flag"
	"time"

	apiequality "k8s.io/apimachinery/pkg/api/equality"

	"github.com/gardener/vpa-recommender/pkg/recommender/checkpoint"
	"github.com/gardener/vpa-recommender/pkg/recommender/input"
	"github.com/gardener/vpa-recommender/pkg/recommender/logic"

	"github.com/gardener/vpa-recommender/pkg/recommender/model"
	metrics_recommender "github.com/gardener/vpa-recommender/pkg/utils/metrics/recommender"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	vpa_types "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	vpa_clientset "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/client/clientset/versioned"
	vpa_api "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/client/clientset/versioned/typed/autoscaling.k8s.io/v1"

	vpa_utils "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/utils/vpa"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
)

// AggregateContainerStateGCInterval defines how often expired AggregateContainerStates are garbage collected.
const AggregateContainerStateGCInterval = 60 * time.Minute

var (
	checkpointsWriteTimeout = flag.Duration("checkpoints-timeout", time.Minute, `Timeout for writing checkpoints since the start of the recommender's main loop`)
	minCheckpointsPerRun    = flag.Int("min-checkpoints", 10, "Minimum number of checkpoints to write per recommender's main loop")
	memorySaver             = flag.Bool("memory-saver", false, `If true, only track pods which have an associated VPA`)
)

// Recommender recommend resources for certain containers, based on utilization periodically got from metrics api.
type Recommender interface {
	// RunOnce performs one iteration of recommender duties followed by update of recommendations in VPA objects.
	RunOnce()
	// GetClusterState returns ClusterState used by Recommender
	GetClusterState() *model.ClusterState
	// GetClusterStateFeeder returns ClusterStateFeeder used by Recommender
	GetClusterStateFeeder() input.ClusterStateFeeder
	// UpdateVPAs computes recommendations and sends VPAs status updates to API Server
	UpdateVPAs()
	// MaintainCheckpoints stores current checkpoints in API Server and garbage collect old ones
	// MaintainCheckpoints writes at least minCheckpoints if there are more checkpoints to write.
	// Checkpoints are written until ctx permits or all checkpoints are written.
	MaintainCheckpoints(ctx context.Context, minCheckpoints int)
	// GarbageCollect removes old AggregateCollectionStates
	GarbageCollect()
}

type recommender struct {
	clusterState                  *model.ClusterState
	clusterStateFeeder            input.ClusterStateFeeder
	checkpointWriter              checkpoint.CheckpointWriter
	checkpointsGCInterval         time.Duration
	lastCheckpointGC              time.Time
	vpaClient                     vpa_api.VerticalPodAutoscalersGetter
	podResourceRecommender        logic.PodResourceRecommender
	useCheckpoints                bool
	lastAggregateContainerStateGC time.Time
}

func (r *recommender) GetClusterState() *model.ClusterState {
	return r.clusterState
}

func (r *recommender) GetClusterStateFeeder() input.ClusterStateFeeder {
	return r.clusterStateFeeder
}

type patchRecord struct {
	Op    string      `json:"op,inline"`
	Path  string      `json:"path,inline"`
	Value interface{} `json:"value"`
}

func patchVpa(vpaClient vpa_api.VerticalPodAutoscalerInterface, vpaName string, patches []patchRecord) (result *vpa_types.VerticalPodAutoscaler, err error) {
	bytes, err := json.Marshal(patches)
	if err != nil {
		klog.Errorf("Cannot marshal VPA status patches %+v. Reason: %+v", patches, err)
		return
	}

	return vpaClient.Patch(context.TODO(), vpaName, types.JSONPatchType, bytes, meta.PatchOptions{})
}

// UpdateVpaIfNeeded updates the status field of the VPA API object.
// It prevents race conditions by verifying that the lastUpdateTime of the
// API object and its model representation are equal.
func UpdateVpaIfNeeded(vpaClient vpa_api.VerticalPodAutoscalerInterface, vpaName string, newStatus,
	oldStatus *vpa_types.VerticalPodAutoscalerStatus, updateStatus bool) (result *vpa_types.VerticalPodAutoscaler, err error) {

	if apiequality.Semantic.DeepEqual(*oldStatus, *newStatus) {
		return nil, nil
	}

	newVPAStatusData, err := json.Marshal(newStatus)
	if err != nil {
		klog.Errorf(
			"JSON Marshalling error for VPA object %v. Reason: %+v", vpaName, err)
	}

	vpaObj, err := vpaClient.Get(context.TODO(), vpaName, meta.GetOptions{})
	if err != nil {
		klog.Errorf("Error getting vpaObj for vpaName %v to update. Reason: %v", vpaName, err)
	}

	vpaObj.ObjectMeta.Annotations["vpa-recommender.gardener.cloud/status"] = string(newVPAStatusData)
	_, err = vpaClient.Update(context.TODO(), vpaObj, meta.UpdateOptions{})
	if err != nil {
		klog.Errorf("Error updating annotations to vpa object. Reason: %v", err)
	}
	patches := []patchRecord{}

	if updateStatus {
		patches = append(patches, patchRecord{
			Op:    "add",
			Path:  "/status",
			Value: *newStatus,
		})
	}

	return patchVpa(vpaClient, vpaName, patches)
}

// Updates VPA CRD objects' statuses.
func (r *recommender) UpdateVPAs() {
	cnt := metrics_recommender.NewObjectCounter()
	defer cnt.Observe()

	for _, observedVpa := range r.clusterState.ObservedVpas {
		key := model.VpaID{
			Namespace: observedVpa.Namespace,
			VpaName:   observedVpa.Name,
		}
		vpa, found := r.clusterState.Vpas[key]
		// if VPA is not found continue without updating any new recommendations
		if !found {
			continue
		}

		resources, isRecommendation := r.podResourceRecommender.GetRecommendedPodResources(GetContainerNameToAggregateStateMap(vpa))
		// Continue without updating the VPA if no new recommendations
		if !isRecommendation {
			continue
		}

		had := vpa.HasRecommendation()
		vpa.UpdateRecommendation(getCappedRecommendation(vpa.ID, resources, observedVpa.Spec.ResourcePolicy))

		if vpa.HasRecommendation() && !had {
			metrics_recommender.ObserveRecommendationLatency(vpa.Created)
		}
		hasMatchingPods := vpa.PodCount > 0
		vpa.UpdateConditions(hasMatchingPods)
		if err := r.clusterState.RecordRecommendation(vpa, time.Now()); err != nil {
			klog.Warningf("%v", err)
			klog.V(4).Infof("VPA dump")
			klog.V(4).Infof("%+v", vpa)
			klog.V(4).Infof("HasMatchingPods: %v", hasMatchingPods)
			klog.V(4).Infof("PodCount: %v", vpa.PodCount)
			pods := r.clusterState.GetMatchingPods(vpa)
			klog.V(4).Infof("MatchingPods: %+v", pods)
			if len(pods) != vpa.PodCount {
				klog.Errorf("ClusterState pod count and matching pods disagree for vpa %v/%v", vpa.ID.Namespace, vpa.ID.VpaName)
			}
		}
		cnt.Add(vpa)

		_, err := UpdateVpaIfNeeded(
			r.vpaClient.VerticalPodAutoscalers(vpa.ID.Namespace), vpa.ID.VpaName, vpa.AsStatus(), &observedVpa.Status, model.GetAggregationsConfig().UpdateVpaStatus)
		if err != nil {
			klog.Errorf(
				"Cannot update VPA %v object. Reason: %+v", vpa.ID.VpaName, err)
		}

	}
}

// getCappedRecommendation creates a recommendation based on recommended pod
// resources, setting the UncappedTarget to the calculated recommended target
// and if necessary, capping the Target, LowerBound and UpperBound according
// to the ResourcePolicy.
func getCappedRecommendation(vpaID model.VpaID, resources logic.RecommendedPodResources,
	policy *vpa_types.PodResourcePolicy) *vpa_types.RecommendedPodResources {
	containerResources := make([]vpa_types.RecommendedContainerResources, 0, len(resources))
	for containerName, res := range resources {
		containerResources = append(containerResources, vpa_types.RecommendedContainerResources{
			ContainerName:  containerName,
			Target:         model.ResourcesAsResourceList(res.Target),
			LowerBound:     model.ResourcesAsResourceList(res.LowerBound),
			UpperBound:     model.ResourcesAsResourceList(res.UpperBound),
			UncappedTarget: model.ResourcesAsResourceList(res.Target),
		})
	}
	recommendation := &vpa_types.RecommendedPodResources{containerResources}
	cappedRecommendation, err := vpa_utils.ApplyVPAPolicy(recommendation, policy)
	if err != nil {
		klog.Errorf("Failed to apply policy for VPA %v/%v: %v", vpaID.Namespace, vpaID.VpaName, err)
		return recommendation
	}
	return cappedRecommendation
}

func (r *recommender) MaintainCheckpoints(ctx context.Context, minCheckpointsPerRun int) {
	now := time.Now()
	if r.useCheckpoints {
		if err := r.checkpointWriter.StoreCheckpoints(ctx, now, minCheckpointsPerRun); err != nil {
			klog.Warningf("Failed to store checkpoints. Reason: %+v", err)
		}
		if time.Now().Sub(r.lastCheckpointGC) > r.checkpointsGCInterval {
			r.lastCheckpointGC = now
			r.clusterStateFeeder.GarbageCollectCheckpoints()
		}
	}
}

func (r *recommender) GarbageCollect() {
	gcTime := time.Now()
	if gcTime.Sub(r.lastAggregateContainerStateGC) > AggregateContainerStateGCInterval {
		r.clusterState.GarbageCollectAggregateCollectionStates()
		r.lastAggregateContainerStateGC = gcTime
	}
}

func (r *recommender) RunOnce() {
	timer := metrics_recommender.NewExecutionTimer()
	defer timer.ObserveTotal()

	ctx := context.Background()
	ctx, cancelFunc := context.WithDeadline(ctx, time.Now().Add(*checkpointsWriteTimeout))
	defer cancelFunc()

	klog.V(3).Infof("Recommender Run")

	r.clusterStateFeeder.LoadVPAs()
	timer.ObserveStep("LoadVPAs")

	r.clusterStateFeeder.LoadPods()
	timer.ObserveStep("LoadPods")

	r.clusterStateFeeder.LoadRealTimeMetrics()
	timer.ObserveStep("LoadMetrics")
	klog.V(3).Infof("ClusterState is tracking %v PodStates and %v VPAs", len(r.clusterState.Pods), len(r.clusterState.Vpas))

	r.UpdateVPAs()
	timer.ObserveStep("UpdateVPAs")

	r.MaintainCheckpoints(ctx, *minCheckpointsPerRun)
	timer.ObserveStep("MaintainCheckpoints")

	r.GarbageCollect()
	timer.ObserveStep("GarbageCollect")
	klog.V(3).Infof("ClusterState is tracking %d aggregated container states", r.clusterState.StateMapSize())
}

// RecommenderFactory makes instances of Recommender.
type RecommenderFactory struct {
	ClusterState *model.ClusterState

	ClusterStateFeeder     input.ClusterStateFeeder
	CheckpointWriter       checkpoint.CheckpointWriter
	PodResourceRecommender logic.PodResourceRecommender
	VpaClient              vpa_api.VerticalPodAutoscalersGetter

	CheckpointsGCInterval time.Duration
	UseCheckpoints        bool
}

// Make creates a new recommender instance,
// which can be run in order to provide continuous resource recommendations for containers.
func (c RecommenderFactory) Make() Recommender {
	recommender := &recommender{
		clusterState:                  c.ClusterState,
		clusterStateFeeder:            c.ClusterStateFeeder,
		checkpointWriter:              c.CheckpointWriter,
		checkpointsGCInterval:         c.CheckpointsGCInterval,
		useCheckpoints:                c.UseCheckpoints,
		vpaClient:                     c.VpaClient,
		podResourceRecommender:        c.PodResourceRecommender,
		lastAggregateContainerStateGC: time.Now(),
		lastCheckpointGC:              time.Now(),
	}
	klog.V(3).Infof("New Recommender created %+v", recommender)
	return recommender
}

// NewRecommender creates a new recommender instance.
// Dependencies are created automatically.
// Deprecated; use RecommenderFactory instead.
func NewRecommender(config *rest.Config, checkpointsGCInterval time.Duration, useCheckpoints bool, namespace string) Recommender {
	clusterState := model.NewClusterState()
	return RecommenderFactory{
		ClusterState:           clusterState,
		ClusterStateFeeder:     input.NewClusterStateFeeder(config, clusterState, *memorySaver, namespace),
		CheckpointWriter:       checkpoint.NewCheckpointWriter(clusterState, vpa_clientset.NewForConfigOrDie(config).AutoscalingV1()),
		VpaClient:              vpa_clientset.NewForConfigOrDie(config).AutoscalingV1(),
		PodResourceRecommender: logic.CreatePodResourceRecommender(),
		CheckpointsGCInterval:  checkpointsGCInterval,
		UseCheckpoints:         useCheckpoints,
	}.Make()
}
