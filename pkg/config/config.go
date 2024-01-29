package config

import (
	"io/ioutil"
	"os"

	corev1 "k8s.io/api/core/v1"
)

const (
	DefaultMilvusVersion   = "v2.3.7"
	DefaultMilvusBaseImage = "milvusdb/milvus"
	DefaultMilvusImage     = DefaultMilvusBaseImage + ":" + DefaultMilvusVersion
	DefaultImagePullPolicy = corev1.PullIfNotPresent
	MilvusConfigTpl        = "milvus.yaml.tmpl"
	MilvusClusterConfigTpl = "milvus-cluster.yaml.tmpl"
	MigrationConfigTpl     = "migration.yaml.tmpl"
)

const (
	TemplateRelativeDir = "config/assets/templates"
	ChartDir            = "config/assets/charts"
	ProviderName        = "milvus-operator"
)

var (
	defaultConfig *Config
	// set by run flag in main
	OperatorNamespace = "milvus-operator"
	OperatorName      = "milvus-operator"
	// param related to performance
	MaxConcurrentReconcile   = 10
	MaxConcurrentHealthCheck = 10
	SyncIntervalSec          = 600
)

func Init(workDir string) error {
	c, err := NewConfig(workDir)
	if err != nil {
		return err
	}
	defaultConfig = c
	if os.Getenv("DEBUG") == "true" {
		defaultConfig.debugMode = true
	}

	return nil
}

func IsDebug() bool {
	return defaultConfig.debugMode
}

func GetMilvusConfigTemplate() string {
	return defaultConfig.GetTemplate(MilvusConfigTpl)
}

func GetMilvusClusterConfigTemplate() string {
	return defaultConfig.GetTemplate(MilvusClusterConfigTpl)
}

func GetMigrationConfigTemplate() string {
	return defaultConfig.GetTemplate(MigrationConfigTpl)
}

type Config struct {
	debugMode bool
	templates map[string]string
}

func NewConfig(workDir string) (*Config, error) {
	config := &Config{
		templates: make(map[string]string),
	}

	templateDir := workDir + TemplateRelativeDir

	tmpls, err := ioutil.ReadDir(templateDir)
	if err != nil {
		return nil, err
	}
	for _, tmpl := range tmpls {
		data, err := ioutil.ReadFile(templateDir + "/" + tmpl.Name())
		if err != nil {
			return nil, err
		}

		config.templates[tmpl.Name()] = string(data)
	}

	return config, nil
}

func (c Config) GetTemplate(name string) string {
	return c.templates[name]
}
