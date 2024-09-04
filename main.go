/*
Copyright 2021.

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
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/config"
	"github.com/milvus-io/milvus-operator/pkg/controllers"
	"github.com/milvus-io/milvus-operator/pkg/helm/values"
	"github.com/milvus-io/milvus-operator/pkg/manager"
)

var (
	setupLog = ctrl.Log.WithName("setup")
)

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var stopReconcilers string
	var enablePprof bool
	var probeAddr string
	var workDir string
	var k8sQps int = 100
	var k8sBurst int = 100
	var enableWebhook bool
	showVersion := flag.Bool("version", false, "Show version")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&stopReconcilers, "stop-reconcilers", "", "stop reconcilers, split by comma, supports: all, milvus, milvusupgrade")
	flag.StringVar(&workDir, "work-dir", "", "The work directory where the config assets locate")
	flag.StringVar(&controllers.ToolImage, "tool-image", controllers.ToolImage, "default tool image for setup milvus")
	flag.StringVar(&config.OperatorNamespace, "namespace", config.OperatorNamespace, "The namespace of self")
	flag.StringVar(&config.OperatorName, "name", config.OperatorName, "The name of self")
	flag.IntVar(&config.MaxConcurrentReconcile, "concurrent-reconcile", config.MaxConcurrentReconcile, "The max concurrent reconcile")
	flag.IntVar(&config.MaxConcurrentHealthCheck, "concurrent-healthcheck", config.MaxConcurrentHealthCheck, "The max concurrent healthcheck")
	flag.IntVar(&config.SyncIntervalSec, "sync-interval", config.SyncIntervalSec, "The interval of sync milvus")
	flag.BoolVar(&enablePprof, "pprof", enablePprof, "Enable pprof")
	flag.IntVar(&k8sQps, "k8s-qps", k8sQps, "The qps of k8s client")
	flag.IntVar(&k8sBurst, "k8s-burst", k8sQps, "The burst of k8s client")
	flag.BoolVar(&controllers.Debug, "debug", controllers.Debug, "Enable debug")
	flag.BoolVar(&enableWebhook, "webhook", true, "Enable webhook for support of v1alpha1 crd & validation")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	if *showVersion {
		fmt.Println("version: " + v1beta1.Version)
		fmt.Println("milvus-helm version: " + v1beta1.MilvusHelmVersion)
		os.Exit(0)
	}

	if enablePprof {
		go func() {
			setupLog.Error(http.ListenAndServe(":6060", nil), "serve pprof")
		}()
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if err := config.Init(workDir); err != nil {
		setupLog.Error(err, "unable to init config")
		os.Exit(1)
	}

	values.MustInitDefaultValuesProvider()

	mgr, err := manager.NewManager(k8sQps, k8sBurst, metricsAddr, probeAddr, enableLeaderElection)
	if err != nil {
		setupLog.Error(err, "new manager")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	controllers.InitializeMetrics()

	if err := controllers.SetupControllers(ctx, mgr, strings.Split(stopReconcilers, ","), enableWebhook); err != nil {
		setupLog.Error(err, "unable to setup controller with manager")
		os.Exit(1)
	}

	setupLog.Info("starting manager", "version", v1beta1.Version, "milvus-helm version", v1beta1.MilvusHelmVersion)
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
