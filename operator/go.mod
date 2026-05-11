module github.com/openserve/openserve/operator

go 1.23

require (
	cloud.google.com/go/bigquery v1.60.0
	cloud.google.com/go/storage v1.38.0
	google.golang.org/api v0.169.0
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/api v0.29.3
	k8s.io/apimachinery v0.29.3
	k8s.io/client-go v0.29.3
	sigs.k8s.io/controller-runtime v0.17.3
	sigs.k8s.io/keda/v2 v2.13.1
)

require (
	github.com/go-logr/logr v1.4.1
	github.com/go-logr/zapr v1.3.0
	go.uber.org/zap v1.27.0
)
