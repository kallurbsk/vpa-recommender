module github.com/gardener/vpa-recommender

go 1.15

require (
	github.com/golang/mock v1.6.0
	github.com/prometheus/client_golang v1.11.0
	github.com/prometheus/common v0.29.0 // indirect
	github.com/stretchr/testify v1.7.0
	k8s.io/api v0.21.2
	k8s.io/apimachinery v0.21.2
	k8s.io/autoscaler/vertical-pod-autoscaler v0.9.2
	k8s.io/client-go v0.21.2
	k8s.io/component-base v0.21.2
	k8s.io/klog v1.0.0
	k8s.io/metrics v0.21.2
)
