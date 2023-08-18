package controllers

import (
	"context"
	"fmt"
	"os"
	"reflect"

	"github.com/go-logr/logr"
	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/helm"
	"github.com/zilliztech/milvus-operator/pkg/helm/values"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

//go:generate mockgen -package=controllers -source=dependencies.go -destination=dependencies_mock.go HelmReconciler

const (
	Etcd   = "etcd"
	Minio  = "minio"
	Pulsar = "pulsar"
	Kafka  = "kafka"
)

var (
	// DefaultValuesPath is the path to the default values file
	// variable in test, const in runtime
	DefaultValuesPath = "config/assets/charts/values.yaml"
)

// HelmReconciler reconciles Helm releases
type HelmReconciler interface {
	NewHelmCfg(namespace string) *action.Configuration
	Reconcile(ctx context.Context, request helm.ChartRequest) error
	GetValues(namespace, release string) (map[string]interface{}, error)
}

type Chart = string
type Values = map[string]interface{}

// LocalHelmReconciler implements HelmReconciler at local
type LocalHelmReconciler struct {
	helmSettings *cli.EnvSettings
	logger       logr.Logger
}

func MustNewLocalHelmReconciler(helmSettings *cli.EnvSettings, logger logr.Logger) *LocalHelmReconciler {
	return &LocalHelmReconciler{
		helmSettings: helmSettings,
		logger:       logger,
	}
}

func (l LocalHelmReconciler) NewHelmCfg(namespace string) *action.Configuration {
	cfg := new(action.Configuration)
	helmLogger := func(format string, v ...interface{}) {
		l.logger.Info(fmt.Sprintf(format, v...))
	}

	// cfg.Init will never return err, only panic if bad driver
	cfg.Init(
		getRESTClientGetterWithNamespace(l.helmSettings, namespace),
		namespace,
		os.Getenv("HELM_DRIVER"),
		helmLogger,
	)

	return cfg
}

func getRESTClientGetterWithNamespace(env *cli.EnvSettings, namespace string) genericclioptions.RESTClientGetter {
	return &genericclioptions.ConfigFlags{
		Namespace:        &namespace,
		Context:          &env.KubeContext,
		BearerToken:      &env.KubeToken,
		APIServer:        &env.KubeAPIServer,
		CAFile:           &env.KubeCaFile,
		KubeConfig:       &env.KubeConfig,
		Impersonate:      &env.KubeAsUser,
		ImpersonateGroup: &env.KubeAsGroups,
		Insecure:         &configFlagInsecure,
	}
}

// ReconcileHelm reconciles Helm releases
func (l LocalHelmReconciler) Reconcile(ctx context.Context, request helm.ChartRequest) error {
	cfg := l.NewHelmCfg(request.Namespace)

	exist, err := helm.ReleaseExist(cfg, request.ReleaseName)
	if err != nil {
		return err
	}

	if !exist {
		if request.Chart == helm.GetChartPathByName(Pulsar) {
			request.Values["initialize"] = true
		}
		l.logger.Info("helm install values", "values", request.Values)
		return helm.Install(cfg, request)
	}

	vals, err := helm.GetValues(cfg, request.ReleaseName)
	if err != nil {
		return err
	}

	status, err := helm.GetStatus(cfg, request.ReleaseName)
	if err != nil {
		return err
	}

	if request.Chart == helm.GetChartPathByName(Pulsar) {
		delete(vals, "initialize")
	}

	deepEqual := reflect.DeepEqual(vals, request.Values)
	needUpdate := helm.NeedUpdate(status)
	if deepEqual && !needUpdate {
		return nil
	}

	if request.Chart == helm.GetChartPathByName(Pulsar) {
		request.Values["initialize"] = false
	}

	l.logger.Info("update helm", "namespace", request.Namespace, "release", request.ReleaseName, "needUpdate", needUpdate, "deepEqual", deepEqual)
	if !deepEqual {
		l.logger.Info("update helm values", "old", vals, "new", request.Values)
	}

	return helm.Update(cfg, request)
}

func (l *LocalHelmReconciler) GetValues(namespace, release string) (map[string]interface{}, error) {
	cfg := l.NewHelmCfg(namespace)
	exist, err := helm.ReleaseExist(cfg, release)
	if err != nil {
		return nil, err
	}
	if !exist {
		return map[string]interface{}{}, nil
	}
	return helm.GetValues(cfg, release)
}

func (r *MilvusReconciler) ReconcileEtcd(ctx context.Context, mc v1beta1.Milvus) error {
	if mc.Spec.Dep.Etcd.External {
		return nil
	}
	request := helm.GetChartRequest(mc, values.DependencyKindEtcd, Etcd)

	return r.helmReconciler.Reconcile(ctx, request)
}

func (r *MilvusReconciler) ReconcileMsgStream(ctx context.Context, mc v1beta1.Milvus) error {
	switch mc.Spec.Dep.MsgStreamType {
	case v1beta1.MsgStreamTypeKafka:
		return r.ReconcileKafka(ctx, mc)
	case v1beta1.MsgStreamTypeRocksMQ, v1beta1.MsgStreamTypeNatsMQ:
		// built in, do nothing
		return nil
	default:
		return r.ReconcilePulsar(ctx, mc)
	}
}

func (r *MilvusReconciler) ReconcileKafka(ctx context.Context, mc v1beta1.Milvus) error {
	if mc.Spec.Dep.Kafka.External {
		return nil
	}
	request := helm.GetChartRequest(mc, values.DependencyKindKafka, Kafka)

	return r.helmReconciler.Reconcile(ctx, request)
}

func (r *MilvusReconciler) ReconcilePulsar(ctx context.Context, mc v1beta1.Milvus) error {
	if mc.Spec.Dep.Pulsar.External {
		return nil
	}
	request := helm.GetChartRequest(mc, values.DependencyKindPulsar, Pulsar)

	return r.helmReconciler.Reconcile(ctx, request)
}

func (r *MilvusReconciler) ReconcileMinio(ctx context.Context, mc v1beta1.Milvus) error {
	if mc.Spec.Dep.Storage.External {
		return nil
	}
	request := helm.GetChartRequest(mc, values.DependencyKindStorage, Minio)

	return r.helmReconciler.Reconcile(ctx, request)
}
