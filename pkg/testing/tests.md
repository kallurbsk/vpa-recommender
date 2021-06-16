# Test Cases, Results and Analysis for VPA recommender

## Considerations
1. [Development Proposal document](https://github.com/kallurbsk/autoscaler/tree/vpa-recommender/vertical-pod-autoscaler/docs/proposals/crash_loop_handling)
2. [Code](https://github.com/kallurbsk/vpa-recommender/tree/vpa_v1)
3. [Stress Test tool used: [progrium/stress]](https://hub.docker.com/r/progrium/stress/) - The tool initiates a single worker spinning on malloc()/free() operation and max memory that can be used is set at 2048MB in this example. There is a one second interval between malloc() and free() operation defined
4. [Test Application Yaml](test_apps/stress_test_tool.yaml)
5. Test Cluster - kubernetes cluster on development landscape

## Unit Testing
- Unit test validating the new estimator algorithm which gives the CPU and memory scale
- Test evaluating setting CurrentCPUUsage, CurrentMemUsage, LocalMaximaCPUUsage, LocalMaximaMemUsage values in the Aggregate Container State object which is used to take care of state keeping the containers

## Functional Testing
1. Scale up recommendation to get pod state from `CrashLoopBackOff` (CLBO) to `Running` state. (compare)
2. Scale up recommendation if the current pod usage crosses threshold upper limit (compare)
3. Scale up recommendation stays intact and (if required scales up) even after recommender restart
4. Scale down recommendeation is applied if maximum of the current pod usage, local maxima within timeout window, goes below threshold lower limit (compare) and time out for scale down has surpassed.
5. Scale down recommendation is not applied if the maximum of the current pod usage, local maxima within timeout window, before threshold time out for scale down is not surpassed.
6. Recommender restart during scale down ensures scale down continues once recommender is back running
8. No scale recommendation if the pod usage is within threshold upper and lower limits
9. Recommendation sustains and does not scale resources even if recommender restarts, if pod usage is within threshold limits

**Note**: for tests having "compare", run it against old and new recommender to get a comparitive result for analysis

### Individual Test Steps
This paragraph talks about individual test steps required for running each of the test cases mentioned above

#### Scale Up Tests
1. Scale up recommendation to get pod state from `CrashLoopBackOff` (CLBO) to `Running` state. (compare)
- Test Steps
    1. Ensure the new vpa-recommender is running against the kubernetes cluster to be tested.
    2.	Define the VPA component resource for the application being tested. (Check stress_test_tool.yaml)
    3.	Once the application and VPA resource is applied, the application component starts off with an OOM Kill due to insufficient memory mentioned according to initial requests and limits.  There by goes into CrashLoopBackOff indicating a sudden choke in the memory and CPU usage.

- Expectation
    1. The VPA recommender should monitor the vpa component resource of this application and if applicable should start recommending.
    2. Considering that both CPU and memory are insufficient and the pod is already in CrashLoopBackOff state, the restart budget of the container should be calculated. If less than 0, then doubling of both CPU and memory based on current usage should be recommended as the resource to be applied for the application. (Restart Budget = Restart Budget – [CurrentRestartCount – LastSeenRestartCount])
    3. The pod should arrive back at Running state.

- Results Old VPA Recommender
- Results New VPA Recommender
- Analysis

2. Scale up recommendation if the current pod usage crosses threshold upper limit (compare)
- Test Steps
    1.	Ensure the new vpa-recommender is running against the kubernetes cluster to be tested.
    2.	Define the VPA component resource for the application being tested. (Check the yaml above)
    3.	Once the application is stable and in Running state, the VPA recommender should monitor the resource usage at the intervals of 1 minute.
    4.	Resource Request Upper Threshold Limit (RRUTL) is calculated respectively for both CPU and Memory. The threshold limit is set as a user
    defined parameter to the recommender binary. (Currently defaulted to 0.75)
- Expectation
    1.	If the resource usage goes beyond RRUTL, then the scale value for that resource should be doubled.
    2.	The pod restarts and applies the new resource requests calculated by the recommender accordingly.
    3.	The pod should stabilize and not run into CrashLoop

- Results Old VPA Recommender
- Results New VPA Recommender
- Analysis

3. Scale up recommendation stays intact and (if required scales up) even after recommender restart
- Test Steps
    1.	Ensure the new vpa-recommender is running against the kubernetes cluster to be tested.
    2.	Define the VPA component resource for the application being tested. (Check the yaml above)
    3.	Once the application is stable and in Running state, the VPA recommender should monitor the resource usage at the intervals of 1 minute.
    4.	Resource Request Upper Threshold Limit (RRUTL) is calculated respectively for both CPU and Memory. The threshold limit is set as a user
    defined parameter to the recommender binary. (Currently defaulted to 0.75)
    5.  Initiate a restart of the VPA recommender. Observe the VPA recommender comes back up post restart and starts detecting the VPA selectors

- Expectation
    1. The VPA recommendation given till now should stay intact.
    2. If there is no change in pod resource usage while the VPA is down, the pod should remain in running state.
    3. Once the VPA recommender is up, it should detect the VPA resource defined and VPA objects on the cluster using corresponding selectors.
    4. The restarted VPA should continue to recommend should there be a requirement based on the usage pattern.

- Results New VPA Recommender
- Analysis


#### Scale Down Tests
1. Scale down recommendation is applied if maximum of the current pod usage, local maxima within timeout window, goes below threshold lower limit (compare) and time out for scale down has surpassed.
- Test Steps
    1.	Ensure the new vpa-recommender is running against the kubernetes cluster to be tested.
    2.	Define the VPA component resource for the application being tested. (Check the yaml above)
    3.	Once the application is stable and in Running state, the VPA recommender should monitor the resource usage at the intervals of 1 minute.
    4.	Resource Request Lower Threshold Limit (RRLTL) is calculated respectively for both CPU and Memory. The threshold limit is set as a user defined parameter to the recommender binary. (Currently defaulted to 0.3)
    5.	Set the ScaleDownSafetyMargin, a user defined parameter to indicate what times the usage should the pod scale down. Currently defaulted to 1.2

- Expectation
    1.	If the resource usage goes below RRLTL, then the scale value for that resource should be reduced to ScaleDownSafetyLimit.
    2.  The pod restarts and applies the new resource requests calculated by the recommender accordingly.

- Results Old VPA Recommender
- Results New VPA Recommender
- Analysis

2. Recommender restart during scale down ensures scale down continues once recommender is back running
- Test Steps
    1.	Ensure the new vpa-recommender is running against the kubernetes cluster to be tested.
    2.	Define the VPA component resource for the application being tested.
    3.	Once the application is stable and in Running state, the VPA recommender should monitor the resource usage at the intervals of 1 minute.
    4.	Resource Request Lower Threshold Limit (RRLTL) is calculated respectively for both CPU and Memory. The threshold limit is set as a user
    defined parameter to the recommender binary. (Currently defaulted to 0.3)
    5.  Initiate a restart of the VPA recommender. Observe the VPA recommender comes back up post restart and starts detecting the VPA selectors

- Expectation
    1. The VPA recommendation given till now should stay intact.
    2. If there is no change in pod resource usage while the VPA is down, the pod should remain in running state.
    3. Once the VPA recommender is up, it should detect the VPA resource defined and VPA objects on the cluster using corresponding selectors.
    4. The restarted VPA should continue to recommend should there be a requirement based on the usage pattern.

- Results of New VPA Recommender
- Analysis

#### No Scale Tests
1. No scale recommendation if the pod usage is within threshold upper and lower limits
- Test Steps
    1.  Ensure the new vpa-recommender is running against the kubernetes cluster to be tested.
    2.	Define the VPA component resource for the application being tested.
    3.	Once the application is stable and in Running state, the VPA recommender should monitor the resource usage at the intervals of 1 minute.

- Expectation
    1.  If the VPA recommender finds that the resource usage is within the upper threshold limit and lower threshold limit, there should be no recommendation.

- Results of New VPA Recommender
- Analysis

2. Recommendation sustains and does not scale resources even if recommender restarts, if pod usage is within threshold limits
- Test Steps
    1.  Ensure the new vpa-recommender is running against the kubernetes cluster to be tested.
    2.	Define the VPA component resource for the application being tested.
    3.	Once the application is stable and in Running state, the VPA recommender should monitor the resource usage at the intervals of 1 minute.
    4.  Restart the VPA recommender when there is no new recommendation because the pod resource usage is within threshold limits

- Expectation
    1.  If the VPA recommender finds that the resource usage is within the upper threshold limit and lower threshold limit, there should be no recommendation.
    2. Once after restart, the VPA recommender should detect the pod and VPA resource attached to it. There should be no recommendation going forward too if the pod resource usage continues to stay in the same threshold limits.
     
- Results of New VPA Recommender
- Analysis

### Non-Functional Tests
1. Scale up test without defining limits in the initial pod configuration without VPA resource defined
- Test Steps
    1.	From the above yaml file, delete the limits of the pod and also VPA component completely.
    2.	Apply the pod configuration on the cluster and check if it runs with it.
    3.	Check if the pod is running fine till the current max limit defined for the stress tool 2048MB is reached.
    4.	Keep increasing the memory ask in multiples of 2. (2048, 4096, 8192 MB)

- Expectation
    1.	The pod should keep running till it hits a memory/CPU limit cap defined by the node.
    2.	Post that the pod observes that the node might be tainted with limits allowed for each pod to consume a specific resource and get into OOM state. 

- Results of New VPA Recommender
- Analysis

2. Scale up test without defining limits in the initial pod configuration with VPA resource defined
- Test Steps
    1.	From the above yaml file, delete the limits of the pod and also VPA component completely.
    2.	Apply the pod configuration on the cluster and check if it runs with it.
    3.	Check if the pod is running fine till the current max limit defined for the stress tool 2048MB is reached.
    4.	Keep increasing the memory ask in multiples of 2. (2048, 4096, 8192 MB)

- Expectation
    1.	If the resource consumption limit for pods are not reached for a particular node, the VPA recommender starts recommending based on existing requests. It should get the pod to running state. If still post that it fails, then the pod goes into Crash loop.

- Results of New VPA Recommender
- Analysis