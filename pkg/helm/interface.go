package helm

import (
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
)

//go:generate mockgen -source=interface.go -destination=interface_mock.go -package=helm

// Client interface of helm
type Client interface {
	GetStatus(cfg *action.Configuration, releaseName string) (release.Status, error)
	GetValues(cfg *action.Configuration, releaseName string) (map[string]interface{}, error)
	ReleaseExist(cfg *action.Configuration, releaseName string) (bool, error)
	GetChartVersion(cfg *action.Configuration, releaseName string) (string, error)
	Upgrade(cfg *action.Configuration, request ChartRequest) error
	Update(cfg *action.Configuration, request ChartRequest) error
	Install(cfg *action.Configuration, request ChartRequest) error
	Uninstall(cfg *action.Configuration, releaseName string) error
}

// SetDefaultClient sets the default client
func SetDefaultClient(client Client) {
	defaultClient = client
}

// defaultClient for focade Client functions
var defaultClient Client = new(LocalClient)

func GetStatus(cfg *action.Configuration, releaseName string) (release.Status, error) {
	return defaultClient.GetStatus(cfg, releaseName)
}

func GetValues(cfg *action.Configuration, releaseName string) (map[string]interface{}, error) {
	return defaultClient.GetValues(cfg, releaseName)
}

func ReleaseExist(cfg *action.Configuration, releaseName string) (bool, error) {
	return defaultClient.ReleaseExist(cfg, releaseName)
}

func GetChartVersion(cfg *action.Configuration, releaseName string) (string, error) {
	return defaultClient.GetChartVersion(cfg, releaseName)
}

func Upgrade(cfg *action.Configuration, request ChartRequest) error {
	return defaultClient.Upgrade(cfg, request)
}

func Update(cfg *action.Configuration, request ChartRequest) error {
	return defaultClient.Update(cfg, request)
}

func Install(cfg *action.Configuration, request ChartRequest) error {
	return defaultClient.Install(cfg, request)
}

func Uninstall(cfg *action.Configuration, releaseName string) error {
	return defaultClient.Uninstall(cfg, releaseName)
}
