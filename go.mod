module github.com/gardener/vpa-recommender

go 1.15

require (
	github.com/golang/mock v1.4.1
	github.com/prometheus/client_golang v1.7.1
	github.com/prometheus/common v0.10.0
	github.com/stretchr/testify v1.7.0
	k8s.io/api v0.20.5
	k8s.io/apimachinery v0.20.5
	k8s.io/autoscaler/vertical-pod-autoscaler v0.9.2
	k8s.io/client-go v0.20.5
	k8s.io/component-base v0.20.5
	k8s.io/klog v1.0.0
	k8s.io/metrics v0.18.3
)
