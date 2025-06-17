package config

import (
	"os"
)

const (
	// DefaultMilvusVersion is the default version used when a new Milvus deployment is created.
	DefaultMilvusVersion = "v2.6.0-rc1"
	// DefaultMilvusBaseImage is the default Miluvs container image.
	DefaultMilvusBaseImage = "milvusdb/milvus"
	// DefaultMilvusImage is the default container image:version.
	DefaultMilvusImage     = DefaultMilvusBaseImage + ":" + DefaultMilvusVersion
	milvusConfigTpl        = "milvus.yaml.tmpl"
	milvusClusterConfigTpl = "milvus-cluster.yaml.tmpl"
	migrationConfigTpl     = "migration.yaml.tmpl"
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
	return defaultConfig.GetTemplate(milvusConfigTpl)
}

func GetMilvusClusterConfigTemplate() string {
	return defaultConfig.GetTemplate(milvusClusterConfigTpl)
}

func GetMigrationConfigTemplate() string {
	return defaultConfig.GetTemplate(migrationConfigTpl)
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

	tmpls, err := os.ReadDir(templateDir)
	if err != nil {
		return nil, err
	}
	for _, tmpl := range tmpls {
		data, err := os.ReadFile(templateDir + "/" + tmpl.Name())
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
