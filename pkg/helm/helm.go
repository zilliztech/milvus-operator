package helm

import (
	"errors"
	"reflect"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/helm/values"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
)

type ChartRequest struct {
	ReleaseName string
	Namespace   string
	Chart       string
	Values      map[string]interface{}
}

func NeedUpdate(status release.Status) bool {
	return status == release.StatusFailed ||
		status == release.StatusUnknown ||
		status == release.StatusUninstalled
}

// LocalClient is the local implementation of the Client interface.
type LocalClient struct{}

func (d *LocalClient) GetStatus(cfg *action.Configuration, releaseName string) (release.Status, error) {
	client := action.NewStatus(cfg)
	rel, err := client.Run(releaseName)
	if err != nil {
		return release.StatusUnknown, err
	}

	return rel.Info.Status, nil
}

func (d *LocalClient) GetValues(cfg *action.Configuration, releaseName string) (map[string]interface{}, error) {
	client := action.NewGetValues(cfg)
	vals, err := client.Run(releaseName)
	if err != nil {
		return nil, err
	}

	if vals == nil {
		return map[string]interface{}{}, nil
	}

	return vals, nil
}

func (d *LocalClient) ReleaseExist(cfg *action.Configuration, releaseName string) (bool, error) {
	histClient := action.NewHistory(cfg)
	histClient.Max = 1
	_, err := histClient.Run(releaseName)
	if err == driver.ErrReleaseNotFound {
		return false, nil
	}

	return err == nil, err
}

func (d *LocalClient) Upgrade(cfg *action.Configuration, request ChartRequest) error {
	exist, err := ReleaseExist(cfg, request.ReleaseName)
	if err != nil {
		return err
	}
	if !exist {
		return d.Install(cfg, request)
	}

	return d.Update(cfg, request)
}

func (d *LocalClient) Update(cfg *action.Configuration, request ChartRequest) error {
	client := action.NewUpgrade(cfg)
	client.Namespace = request.Namespace
	chartRequested, err := loader.Load(request.Chart)
	if err != nil {
		return err
	}
	if len(request.Values) == 0 {
		client.ResetValues = true
	}

	_, err = client.Run(request.ReleaseName, chartRequested, request.Values)
	return err
}

func (d *LocalClient) Install(cfg *action.Configuration, request ChartRequest) error {
	client := action.NewInstall(cfg)
	client.ReleaseName = request.ReleaseName
	client.Namespace = request.Namespace
	if client.Version == "" && client.Devel {
		client.Version = ">0.0.0-0"
	}
	// operator doesn't have permission to create CRDs, leave it to the cluster admin
	client.SkipCRDs = true

	chartRequested, err := loader.Load(request.Chart)
	if err != nil {
		return err
	}

	_, err = client.Run(chartRequested, request.Values)
	return err
}

func (d *LocalClient) Uninstall(cfg *action.Configuration, releaseName string) error {
	_, err := cfg.Releases.History(releaseName)
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return nil
	}

	client := action.NewUninstall(cfg)
	client.DisableHooks = true
	_, err = client.Run(releaseName)
	if err != nil {
		return err
	}

	return nil
}

func GetChartPathByName(chart string) string {
	return "config/assets/charts/" + chart
}

func GetChartRequest(mc v1beta1.Milvus, dep values.DependencyKind, chart string) ChartRequest {
	inCluster := reflect.ValueOf(mc.Spec.Dep).FieldByName(string(dep)).
		FieldByName("InCluster").Interface().(*v1beta1.InClusterConfig)
	chartKind := chart
	switch inCluster.ChartVersion {
	case values.ChartVersionPulsarV3:
		chart = values.PulsarV3
		chartKind = values.Pulsar
	}
	return ChartRequest{
		ReleaseName: mc.Name + "-" + chartKind,
		Namespace:   mc.Namespace,
		Chart:       GetChartPathByName(chart),
		Values:      inCluster.Values.Data,
	}
}
