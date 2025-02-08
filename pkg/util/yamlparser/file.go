package yamlparser

import (
	"fmt"
	"io"
	"os"
	"reflect"

	"gopkg.in/yaml.v3"
)

var UserYamlPath = "/milvus/configs/operator/user.yaml"

type UserYaml struct {
	Minio struct {
		Address       string `yaml:"address"`
		Port          int    `yaml:"port"`
		UseSSL        bool   `yaml:"useSSL"`
		UseIAM        bool   `yaml:"useIAM"`
		CloudProvider string `yaml:"cloudProvider"`
		AccessKeyID   string `yaml:"accessKeyID"`
		BucketName    string `yaml:"bucketName"`
		Region        string `yaml:"region"`
	} `yaml:"minio"`
}

func ParseUserYaml() (*UserYaml, error) {
	userYaml := new(UserYaml)
	err := ParseObjectFromFile(UserYamlPath, userYaml)
	return userYaml, err
}

func ParseObjectFromFile(path string, obj interface{}) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open file[%s] failed: %w", path, err)
	}
	defer file.Close()
	fileData, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("read file[%s] failed: %w", path, err)
	}
	err = yaml.Unmarshal(fileData, obj)
	return fmt.Errorf("unmarshal file[%s] as type[%s] failed: %w", path, reflect.TypeOf(obj).String(), err)
}
