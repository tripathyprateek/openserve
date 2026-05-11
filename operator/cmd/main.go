package main

import (
	"context"
	"flag"
	"os"

	"go.uber.org/zap/zapcore"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	openservev1alpha1 "github.com/openserve/openserve/operator/api/v1alpha1"
	"github.com/openserve/openserve/operator/internal/budget"
	"github.com/openserve/openserve/operator/internal/catalog"
	"github.com/openserve/openserve/operator/internal/controller"
	"github.com/openserve/openserve/operator/internal/gateway"
	"github.com/openserve/openserve/operator/internal/scaling"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(openservev1alpha1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(autoscalingv2.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
		catalogURL           string
		modelCacheBucket     string
		gatewayDomain        string
		bigqueryDataset      string
		namespace            string
		inferenceNamespace   string
		gcpProject           string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Address for the metrics endpoint.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Address for health probes.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. Prevents multiple reconcilers from running simultaneously.")
	flag.StringVar(&catalogURL, "catalog-url", "https://catalog.openserve.io",
		"Base URL of the openserve model catalog registry.")
	flag.StringVar(&modelCacheBucket, "model-cache-bucket", "",
		"GCS bucket name for caching model weights (required).")
	flag.StringVar(&gatewayDomain, "gateway-domain", "",
		"Domain under which inference endpoints are served, e.g. 'ai.acme.com' (required).")
	flag.StringVar(&bigqueryDataset, "bigquery-dataset", "",
		"BigQuery dataset for usage metering, e.g. 'openserve_usage' (required).")
	flag.StringVar(&namespace, "namespace", "openserve-system",
		"Kubernetes namespace where the operator runs.")
	flag.StringVar(&inferenceNamespace, "inference-namespace", "openserve-inference",
		"Kubernetes namespace where inference workloads run.")
	flag.StringVar(&gcpProject, "gcp-project", "",
		"GCP project ID for BigQuery and GCS access (required).")

	opts := zap.Options{
		Development: os.Getenv("DEBUG") == "true",
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := ctrl.Log.WithName("setup")

	if modelCacheBucket == "" || gatewayDomain == "" || bigqueryDataset == "" || gcpProject == "" {
		setupLog.Error(nil, "required flags missing: --model-cache-bucket, --gateway-domain, --bigquery-dataset, --gcp-project")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "openserve.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	cfg := controller.Config{
		CatalogURL:       catalogURL,
		ModelCacheBucket: modelCacheBucket,
		GatewayDomain:    gatewayDomain,
		BigQueryDataset:  bigqueryDataset,
	}

	// Create catalog client
	catalogClient := catalog.NewClient(catalogURL)

	// Create scaling reconciler
	scalingReconciler := scaling.NewReconciler(mgr.GetClient())

	// Create gateway syncer
	gatewaySyncer := gateway.NewSyncer(mgr.GetClient(), namespace, inferenceNamespace)

	// Create budget client
	budgetClient, err := budget.NewClient(context.Background(), gcpProject, bigqueryDataset)
	if err != nil {
		setupLog.Error(err, "unable to create budget client")
		os.Exit(1)
	}

	if err := (&controller.ModelDeploymentReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		Config:            cfg,
		CatalogClient:     catalogClient,
		ScalingReconciler: scalingReconciler,
		GatewaySyncer:     gatewaySyncer,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ModelDeployment")
		os.Exit(1)
	}

	if err := (&controller.APIKeyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Config: cfg,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "APIKey")
		os.Exit(1)
	}

	if err := (&controller.BudgetPolicyReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		Config:       cfg,
		BudgetClient: budgetClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "BudgetPolicy")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting openserve operator",
		"catalogURL", catalogURL,
		"modelCacheBucket", modelCacheBucket,
		"gatewayDomain", gatewayDomain,
	)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
