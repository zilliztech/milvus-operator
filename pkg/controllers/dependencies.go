package controllers

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/disk"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/helm"
	"github.com/zilliztech/milvus-operator/pkg/helm/values"
)

func newDeleteOptionsOnlySts() *metav1.DeleteOptions {
	gracePeriodSeconds := int64(0)
	propagationPolicy := metav1.DeletePropagationOrphan
	return &metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriodSeconds,
		PropagationPolicy:  &propagationPolicy,
	}
}

//go:generate mockgen -package=controllers -source=dependencies.go -destination=dependencies_mock.go HelmReconciler

const (
	Etcd     = "etcd"
	Minio    = "minio"
	Pulsar   = "pulsar"
	PulsarV3 = "pulsar-v3"
	Kafka    = "kafka"
	Tei      = "tei"
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
	mgr          manager.Manager
	clientset    kubernetes.Interface
}

func MustNewLocalHelmReconciler(helmSettings *cli.EnvSettings, logger logr.Logger, mgr manager.Manager) *LocalHelmReconciler {
	config := mgr.GetConfig()
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(fmt.Sprintf("Failed to create Kubernetes clientset: %v", err))
	}

	return &LocalHelmReconciler{
		helmSettings: helmSettings,
		logger:       logger,
		mgr:          mgr,
		clientset:    clientset,
	}
}

func (l LocalHelmReconciler) NewHelmCfg(namespace string) *action.Configuration {
	cfg := new(action.Configuration)
	helmLogger := func(format string, v ...interface{}) {
		l.logger.Info(fmt.Sprintf(format, v...))
	}

	// cfg.Init will never return err, only panic if bad driver
	_ = cfg.Init(
		getRESTClientGetterFromClient(l.helmSettings, namespace, l.mgr),
		namespace,
		os.Getenv("HELM_DRIVER"),
		helmLogger,
	)

	return cfg
}

func getRESTClientGetterFromClient(env *cli.EnvSettings, namespace string, mgr manager.Manager) genericclioptions.RESTClientGetter {
	return &clientRESTClientGetter{
		namespace:  namespace,
		kubeConfig: env.KubeConfig,
		mgr:        mgr,
	}
}

type clientRESTClientGetter struct {
	namespace  string
	kubeConfig string
	mgr        manager.Manager
}

func (c *clientRESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	// Get the config from the client
	return c.mgr.GetConfig(), nil
}

func (c *clientRESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	config, err := c.ToRESTConfig()
	if err != nil {
		return nil, err
	}

	return disk.NewCachedDiscoveryClientForConfig(
		config,
		"",
		"",
		45*time.Minute,
	)
}

func (c *clientRESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := c.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient), nil
}

func (c *clientRESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if c.kubeConfig != "" {
		loadingRules.ExplicitPath = c.kubeConfig
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&clientcmd.ConfigOverrides{
			CurrentContext: "",
			Context: clientcmdapi.Context{
				Namespace: c.namespace,
			},
		})
}

func IsPulsarChartPath(chartPath string) bool {
	return chartPath == helm.GetChartPathByName(Pulsar)
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

	if strings.Contains(request.ReleaseName, Etcd) {
		oldSize := vals["persistence"].(map[string]interface{})["size"].(string)
		newSize := request.Values["persistence"].(map[string]interface{})["size"].(string)

		if parseSize(newSize) != parseSize(oldSize) {
			l.logger.Info("reconcile PVC", "old size:", oldSize, "new size:", newSize, "release", request.ReleaseName)
			if err := l.reconcilePVCs(ctx, request.Namespace, request.ReleaseName, oldSize, newSize); err != nil {
				return err
			}
		}
	}

	return helm.Update(cfg, request)
}

func parseSize(size string) int64 {
	size = strings.TrimSpace(size)
	if len(size) == 0 {
		return 0
	}

	unit := size[len(size)-2:]
	value, err := strconv.ParseInt(size[:len(size)-2], 10, 64)
	if err != nil {
		return 0
	}

	switch strings.ToLower(unit) {
	case "ti":
		return value * 1024 * 1024 * 1024 * 1024
	case "gi":
		return value * 1024 * 1024 * 1024
	case "mi":
		return value * 1024 * 1024
	case "ki":
		return value * 1024
	default:
		return value
	}
}

func (l *LocalHelmReconciler) reconcilePVCs(ctx context.Context, namespace, releaseName, oldSize, newSize string) error {
	l.logger.Info("Reconciling PVCs", "namespace", namespace, "release", releaseName, "oldSize", oldSize, "newSize", newSize)

	// 1. Get the old StatefulSet
	stsName := releaseName
	oldSts, err := l.clientset.AppsV1().StatefulSets(namespace).Get(ctx, stsName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get old StatefulSet: %v", err)
	}

	newQuantity, err := resource.ParseQuantity(newSize)
	if err != nil {
		return fmt.Errorf("failed to parse new size: %v", err)
	}

	// 2. Update all related PVCs
	for i := 0; i < int(*oldSts.Spec.Replicas); i++ {
		pvcName := fmt.Sprintf("data-%s-%d", releaseName, i)
		pvc, err := l.clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get PVC %s: %v", pvcName, err)
		}

		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = newQuantity
		_, err = l.clientset.CoreV1().PersistentVolumeClaims(namespace).Update(ctx, pvc, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update PVC %s: %v", pvcName, err)
		}
		l.logger.Info("Updated PVC size", "pvc", pvcName, "newSize", newSize)
	}

	// 3. Recreate the StatefulSet
	if err := l.recreateStatefulSet(ctx, namespace, stsName, oldSts, newQuantity); err != nil {
		return err
	}

	l.logger.Info("Successfully resized PVCs and recreated StatefulSet", "namespace", namespace, "release", releaseName)
	return nil
}

func (l *LocalHelmReconciler) recreateStatefulSet(ctx context.Context, namespace, name string, oldSts *appsv1.StatefulSet, newQuantity resource.Quantity) error {
	// Delete the old StatefulSet
	deleteOptions := newDeleteOptionsOnlySts()
	err := l.clientset.AppsV1().StatefulSets(namespace).Delete(ctx, name, *deleteOptions)
	if err != nil {
		return fmt.Errorf("failed to delete StatefulSet: %v", err)
	}

	// Wait for the StatefulSet to be deleted
	err = l.waitForStatefulSetDeletion(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to wait for StatefulSet deletion: %v", err)
	}

	// Create a new StatefulSet
	newSts := oldSts.DeepCopy()
	newSts.ResourceVersion = ""
	for i := range newSts.Spec.VolumeClaimTemplates {
		newSts.Spec.VolumeClaimTemplates[i].Spec.Resources.Requests[corev1.ResourceStorage] = newQuantity
	}

	_, err = l.clientset.AppsV1().StatefulSets(namespace).Create(ctx, newSts, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create new StatefulSet: %v", err)
	}

	return nil
}

func (l *LocalHelmReconciler) waitForStatefulSetDeletion(ctx context.Context, namespace, name string) error {
	for {
		_, err := l.clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for StatefulSet deletion")
		case <-time.After(5 * time.Second):
			// Continue waiting
		}
	}
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
	case v1beta1.MsgStreamTypePulsar:
		return r.ReconcilePulsar(ctx, mc)
	default:
		// built in mq or custom mq, do nothing
		return nil
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

func (r *MilvusReconciler) ReconcileTei(ctx context.Context, mc v1beta1.Milvus) error {
	if !mc.Spec.Dep.Tei.Enabled {
		return nil
	}
	request := helm.GetChartRequest(mc, values.DependencyKindTei, Tei)

	return r.helmReconciler.Reconcile(ctx, request)
}
