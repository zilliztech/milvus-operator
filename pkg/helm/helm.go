package helm

import (
	"errors"
	"fmt"
	"reflect"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/helm/values"
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
	rel, err := cfg.Releases.Last(releaseName)
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	// Helm reads retention annotations from the stored manifest, not the live PVC.
	// Protect older releases too; Finalize owns explicit deletion via pvcDeletion.
	if rel.Info.Status != release.StatusUninstalled {
		manifest, changed, err := keepPVCs(rel.Manifest)
		if err != nil {
			return fmt.Errorf("protect PVCs in release %s: %w", releaseName, err)
		}
		if changed {
			updated := *rel
			updated.Manifest = manifest
			if err := cfg.Releases.Update(&updated); err != nil {
				return fmt.Errorf("store PVC retention for release %s: %w", releaseName, err)
			}
		}
	}

	client := action.NewUninstall(cfg)
	client.DisableHooks = true
	_, err = client.Run(releaseName)
	if err != nil {
		return err
	}

	return nil
}

// GetChartVersion returns the chart version of a Helm release
func (d *LocalClient) GetChartVersion(cfg *action.Configuration, releaseName string) (string, error) {
	client := action.NewStatus(cfg)
	rel, err := client.Run(releaseName)
	if err != nil {
		return "", err
	}
	if rel.Chart == nil || rel.Chart.Metadata == nil {
		return "", errors.New("chart metadata not found in release")
	}
	return rel.Chart.Metadata.Version, nil
}

func GetChartPathByName(chart string) string {
	return "config/assets/charts/" + chart
}

func GetChartRequest(mc v1beta1.Milvus, dep values.DependencyKind, chart string) ChartRequest {
	inCluster := reflect.ValueOf(mc.Spec.Dep).FieldByName(string(dep)).
		FieldByName("InCluster").Interface().(*v1beta1.InClusterConfig)
	chartKind := chart
	if dep == values.DependencyKindStorage {
		chart = values.Silo
	}

	// Handle Pulsar versions
	if dep == values.DependencyKindPulsar {
		if inCluster.ChartVersion == values.ChartVersionPulsarV3 {
			chart = values.PulsarV3
			chartKind = values.Pulsar
		}
	}

	// Handle Etcd versions
	if dep == values.DependencyKindEtcd {
		switch inCluster.ChartVersion {
		case values.ChartVersionEtcdV6:
			chart = values.EtcdV6
		case values.ChartVersionEtcdV8:
			chart = values.EtcdV8
		default:
			chart = values.EtcdV8 // Default to v8 for new deployments
		}
	}

	chartValues := inCluster.Values.DeepCopy().Data
	if chartValues == nil {
		chartValues = make(map[string]interface{})
	}
	if dep == values.DependencyKindStorage {
		// The CR retains MinIO's credential names; Silo consumes rootUser/rootPassword.
		if _, ok := chartValues["rootUser"]; !ok {
			chartValues["rootUser"] = chartValues["accessKey"]
		}
		if _, ok := chartValues["rootPassword"]; !ok {
			chartValues["rootPassword"] = chartValues["secretKey"]
		}
		chartValues["fullnameOverride"] = mc.Name + "-minio"
		chartValues["nameOverride"] = "minio"
		// Silo's fixed "silo-sa" default conflicts across releases in one namespace.
		serviceAccount, _ := chartValues["serviceAccount"].(map[string]interface{})
		if serviceAccount == nil {
			serviceAccount = make(map[string]interface{})
			chartValues["serviceAccount"] = serviceAccount
		}
		if name, _ := serviceAccount["name"].(string); name == "" {
			serviceAccount["name"] = mc.Name + "-minio"
		}
		// Keep PVC deletion under the operator's pvcDeletion policy for every chart revision.
		persistence, _ := chartValues["persistence"].(map[string]any)
		if persistence == nil {
			persistence = make(map[string]any)
			chartValues["persistence"] = persistence
		}
		annotations, _ := persistence["annotations"].(map[string]any)
		if annotations == nil {
			annotations = make(map[string]any)
			persistence["annotations"] = annotations
		}
		annotations[kube.ResourcePolicyAnno] = kube.KeepPolicy
	}
	return ChartRequest{
		ReleaseName: mc.Name + "-" + chartKind,
		Namespace:   mc.Namespace,
		Chart:       GetChartPathByName(chart),
		Values:      chartValues,
	}
}
