package controllers

import (
	"context"
	"sync"

	"github.com/milvus-io/milvus-operator/pkg/config"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/cli"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	milvusv1alpha1 "github.com/milvus-io/milvus-operator/apis/milvus.io/v1alpha1"
	milvusv1beta1 "github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	milvuscluster "github.com/milvus-io/milvus-operator/pkg/controllers/milvuscluster"
)

var configFlagInsecure = true

type reconciler interface {
	SetupWithManager(mgr ctrl.Manager) error
}

func listHasElement(list []string, elem string) bool {
	for _, e := range list {
		if e == elem {
			return true
		}
	}
	return false
}

var reconcilers = map[string]reconciler{}

func stopReconcilers(stopReoconcilers []string) map[string]bool {
	stopReconcilersMap := make(map[string]bool)
	for _, stopReconciler := range stopReoconcilers {
		stopReconcilersMap[stopReconciler] = true
	}
	return stopReconcilersMap
}

func SetupControllers(ctx context.Context, mgr manager.Manager, stopReconcilers []string, enableHook bool) error {
	logger := ctrl.Log.WithName("controller")

	if len(stopReconcilers) == 0 || stopReconcilers[0] != "all" {
		conf := mgr.GetConfig()
		settings := cli.New()
		settings.KubeAPIServer = conf.Host
		settings.MaxHistory = 2
		settings.KubeToken = conf.BearerToken
		getter := settings.RESTClientGetter()
		config := getter.(*genericclioptions.ConfigFlags)
		config.Insecure = &configFlagInsecure
		helmReconciler := MustNewLocalHelmReconciler(settings, logger.WithName("helm"))

		// should be run after mgr started to make sure the client is ready
		statusSyncer := NewMilvusStatusSyncer(ctx, mgr.GetClient(), logger.WithName("status-syncer"))

		reconciler := &MilvusReconciler{
			Client:         mgr.GetClient(),
			Scheme:         mgr.GetScheme(),
			logger:         logger.WithName("milvus"),
			helmReconciler: helmReconciler,
			statusSyncer:   statusSyncer,
		}
		k8sUtil := NewK8sUtil(mgr.GetClient())
		bizUtil := NewDeployControllerBizUtil(mgr.GetClient(), k8sUtil)
		modeChanger := NewDeployModeChanger(mgr.GetClient(), bizUtil)
		qnBiz := NewDeployControllerBizImpl(statusSyncer, bizUtil, modeChanger, mgr.GetClient())
		qnController := NewDeployController(qnBiz, NewCommonComponentReconciler(reconciler))
		reconciler.qnController = qnController
		reconcilers["milvus"] = reconciler

		reconcilers["milvusupgrade"] = NewMilvusUpgradeReconciler(mgr.GetClient(), mgr.GetScheme())

		reconcilers["milvuscluster"] = milvuscluster.NewMilvusClusterReconciler(
			mgr.GetClient(),
			mgr.GetScheme(),
			logger.WithName("milvuscluster"),
		)

		for name, reconciler := range reconcilers {
			if listHasElement(stopReconcilers, name) {
				continue
			}
			if err := reconciler.SetupWithManager(mgr); err != nil {
				logger.Error(err, "unable to setup controller with manager", "controller", name)
				return err
			}
		}
	}

	if enableHook {
		if err := (&milvusv1beta1.Milvus{}).SetupWebhookWithManager(mgr); err != nil {
			logger.Error(err, "unable to create webhook", "webhook", "Milvus")
			return err
		}
		if err := (&milvusv1alpha1.Milvus{}).SetupWebhookWithManager(mgr); err != nil {
			logger.Error(err, "unable to create webhook", "webhook", "Milvus")
			return err
		}
		if err := (&milvusv1beta1.MilvusUpgrade{}).SetupWebhookWithManager(mgr); err != nil {
			logger.Error(err, "unable to create webhook", "webhook", "MilvusUpgrade")
			return err
		}
	}

	return nil
}

// CommonInfo should be init when before time reconcile
type CommonInfo struct {
	OperatorImageInfo ImageInfo
	once              sync.Once
}

// global common info singleton
var globalCommonInfo CommonInfo

func (c *CommonInfo) InitIfNot(cli client.Client) {
	c.once.Do(func() {
		imageInfo, err := getOperatorImageInfo(cli)
		if err != nil {
			logf.Log.WithName("CommonInfo").Error(err, "get operator image info fail, use default")
			imageInfo = &DefaultOperatorImageInfo
		}
		c.OperatorImageInfo = *imageInfo
	})
}

// ImageInfo for image pulling
type ImageInfo struct {
	Image           string
	ImagePullPolicy corev1.PullPolicy
}

var (
	DefaultOperatorImageInfo = ImageInfo{
		Image:           "milvusdb/milvus-operator:main-latest",
		ImagePullPolicy: corev1.PullAlways,
	}
	ToolImage = ""
)

func getOperatorImageInfo(cli client.Client) (*ImageInfo, error) {
	if ToolImage != "" {
		return &ImageInfo{
			Image:           ToolImage,
			ImagePullPolicy: corev1.PullIfNotPresent,
		}, nil
	}
	deploy := appsv1.Deployment{}
	err := cli.Get(context.TODO(), NamespacedName(config.OperatorNamespace, config.OperatorName), &deploy)
	if err != nil {
		return nil, errors.Wrapf(err, "get operator deployment[%s/%s] fail", config.OperatorNamespace, config.OperatorName)
	}
	if len(deploy.Spec.Template.Spec.Containers) < 1 {
		return nil, errors.New("operator deployment has no container")
	}
	container := deploy.Spec.Template.Spec.Containers[0]
	return &ImageInfo{
		Image:           container.Image,
		ImagePullPolicy: container.ImagePullPolicy,
	}, nil
}
