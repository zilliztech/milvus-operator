package values

import (
	"io/ioutil"

	"github.com/pkg/errors"
	"sigs.k8s.io/yaml"
)

type DependencyKind string
type Chart = string
type Values = map[string]interface{}
type ChartVersion string

const (
	ChartVersionPulsarV2 ChartVersion = "pulsar-v2"
	ChartVersionPulsarV3 ChartVersion = "pulsar-v3"
)

const (
	DependencyKindEtcd    DependencyKind = "Etcd"
	DependencyKindStorage DependencyKind = "Storage"
	DependencyKindPulsar  DependencyKind = "Pulsar"
	DependencyKindKafka   DependencyKind = "Kafka"

	// Chart names & values sub-fields in milvus-helm
	Etcd     = "etcd"
	Minio    = "minio"
	Pulsar   = "pulsar"
	PulsarV3 = "pulsarv3"
	Kafka    = "kafka"
)

const (
	ValuesRootPath = "config/assets/charts"
	// DefaultValuesPath is the path to the default values file
	DefaultValuesPath = ValuesRootPath + "/values.yaml"
)

type DefaultValuesProvider interface {
	GetDefaultValues(dependencyName DependencyKind, chartVersion ChartVersion) map[string]interface{}
}

var globalDefaultValues DefaultValuesProvider = &dummyValues{}

func GetDefaultValuesProvider() DefaultValuesProvider {
	return globalDefaultValues
}

// DefaultValuesProviderImpl is a DefaultValuesProvider implementation
type DefaultValuesProviderImpl struct {
	chartDefaultValues map[Chart]Values
}

func MustInitDefaultValuesProvider() {
	values, err := readValuesFromFile(DefaultValuesPath)
	if err != nil {
		err = errors.Wrapf(err, "failed to read default helm chart values from [%s]", DefaultValuesPath)
		panic(err)
	}
	pulsarV3Values := values[PulsarV3].(Values)
	// helm uses $milvus-pulsarv3 as release name for historical reasons
	// but milvus uses we use $milvus-pulsar
	pulsarV3Values["name"] = "pulsar"
	pulsarV3Values["nameOverride"] = ""

	globalDefaultValues = &DefaultValuesProviderImpl{
		chartDefaultValues: map[Chart]Values{
			Etcd:     values[Etcd].(Values),
			Minio:    values[Minio].(Values),
			Pulsar:   values[Pulsar].(Values),
			PulsarV3: pulsarV3Values,
			Kafka:    values[Kafka].(Values),
		},
	}
}

func (d DefaultValuesProviderImpl) GetDefaultValues(dependencyName DependencyKind, chartVersion ChartVersion) map[string]interface{} {
	switch dependencyName {
	case DependencyKindEtcd:
		return d.chartDefaultValues[Etcd]
	case DependencyKindStorage:
		return d.chartDefaultValues[Minio]
	case DependencyKindPulsar:
		if chartVersion == ChartVersionPulsarV3 {
			return d.chartDefaultValues[PulsarV3]
		}
		return d.chartDefaultValues[Pulsar]
	case DependencyKindKafka:
		return d.chartDefaultValues[Kafka]
	default:
		return map[string]interface{}{}
	}
}

func readValuesFromFile(file string) (Values, error) {
	data, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read file %s", file)
	}
	ret := Values{}
	err = yaml.Unmarshal(data, &ret)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal file %s", file)
	}
	return ret, nil
}
