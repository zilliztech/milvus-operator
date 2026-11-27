package controllers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

// Check the rendered ConfigMap and Pod template together: a valid PVC example
// can otherwise silently leave Milvus writing to the container filesystem.
func TestStatefulSetStoragePathSamples(t *testing.T) {
	for _, filename := range []string{"milvus_storage_paths_2_5.yaml", "milvus_storage_paths_2_6.yaml", "milvus_querynode_statefulset.yaml"} {
		data, err := os.ReadFile("../../config/samples/" + filename)
		require.NoError(t, err)
		decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
		for document := 0; ; document++ {
			var mc v1beta1.Milvus
			err := decoder.Decode(&mc)
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			t.Run(fmt.Sprintf("%s/%d", filename, document), func(t *testing.T) {
				r, _, _, _ := storageRemovalFixture(t, false)
				ctx := context.Background()
				mc.Namespace = "sample-check"
				mc.UID = "sample-check"
				mc.Default()
				_, err := mc.ValidateCreate()
				require.NoError(t, err)
				cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: mc.Name, Namespace: mc.Namespace}}
				require.NoError(t, r.updateConfigMap(ctx, mc, cm))
				var conf struct {
					LocalStorage struct {
						Path string `json:"path"`
					} `json:"localStorage"`
					QueryNode struct {
						EnableDisk bool `json:"enableDisk"`
						Mmap       struct {
							Dir         string `json:"mmapDirPath"`
							VectorField bool   `json:"vectorField"`
							ScalarField bool   `json:"scalarField"`
							ScalarIndex bool   `json:"scalarIndex"`
							VectorIndex bool   `json:"vectorIndex"`
						} `json:"mmap"`
						Segcore struct {
							TieredStorage map[string]interface{} `json:"tieredStorage"`
						} `json:"segcore"`
					} `json:"queryNode"`
				}
				require.NoError(t, yaml.Unmarshal([]byte(cm.Data[UserYaml]), &conf))
				require.Equal(t, "/var/lib/milvus/data", conf.LocalStorage.Path)
				var rendered []string
				// Also check configured components that a newer default image may offline.
				for _, component := range statefulSetCapableComponents() {
					if !componentUsesStatefulSet(mc, component) {
						continue
					}
					sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: component.GetDeploymentName(mc.Name), Namespace: mc.Namespace}}
					require.NoError(t, r.updateStatefulSet(ctx, mc, sts, component))
					require.Len(t, sts.Spec.VolumeClaimTemplates, 1)
					claim := sts.Spec.VolumeClaimTemplates[0]
					require.True(t, claim.Spec.VolumeMode == nil || *claim.Spec.VolumeMode == corev1.PersistentVolumeFilesystem)
					mainIndex := GetContainerIndex(sts.Spec.Template.Spec.Containers, component.Name)
					require.GreaterOrEqual(t, mainIndex, 0)
					var dataMounts []corev1.VolumeMount
					for _, mount := range sts.Spec.Template.Spec.Containers[mainIndex].VolumeMounts {
						if mount.MountPath == conf.LocalStorage.Path {
							dataMounts = append(dataMounts, mount)
						}
					}
					require.Len(t, dataMounts, 1, component.Name)
					require.Equal(t, claim.Name, dataMounts[0].Name)
					require.False(t, dataMounts[0].ReadOnly)
					require.Empty(t, dataMounts[0].SubPath)
					// No other mount may shadow the data subtree used by these features.
					for _, mount := range sts.Spec.Template.Spec.Containers[mainIndex].VolumeMounts {
						if mount.Name != claim.Name {
							require.NotContains(t, mount.MountPath, conf.LocalStorage.Path+"/")
						}
					}
					rendered = append(rendered, component.Name)
				}
				require.NotEmpty(t, rendered)
				if filename == "milvus_querynode_statefulset.yaml" {
					return
				}
				require.True(t, conf.QueryNode.EnableDisk)
				require.True(t, conf.QueryNode.Mmap.VectorField)
				require.True(t, conf.QueryNode.Mmap.ScalarField)
				require.True(t, conf.QueryNode.Mmap.ScalarIndex)
				require.False(t, conf.QueryNode.Mmap.VectorIndex)
				var active []string
				for _, component := range GetComponentsBySpec(mc.Spec) {
					active = append(active, component.Name)
				}
				for _, name := range rendered {
					require.Contains(t, active, name)
				}
				if filename == "milvus_storage_paths_2_5.yaml" {
					require.ElementsMatch(t, []string{QueryNodeName, IndexNodeName}, rendered)
					require.Equal(t, path.Join(conf.LocalStorage.Path, "mmap"), conf.QueryNode.Mmap.Dir)
					require.Empty(t, conf.QueryNode.Segcore.TieredStorage)
				} else {
					require.ElementsMatch(t, []string{QueryNodeName, DataNodeName}, rendered)
					require.NotContains(t, active, IndexNodeName)
					require.Empty(t, conf.QueryNode.Mmap.Dir, "2.6 cache paths use localStorage.path")
					tiered := conf.QueryNode.Segcore.TieredStorage
					require.Equal(t, true, tiered["evictionEnabled"])
					require.Equal(t, true, tiered["backgroundEvictionEnabled"])
					require.Equal(t, 0.75, tiered["diskLowWatermarkRatio"])
					require.Equal(t, 0.8, tiered["diskHighWatermarkRatio"])
					require.Equal(t, map[string]interface{}{"scalarField": "sync", "scalarIndex": "sync", "vectorField": "disable", "vectorIndex": "sync"}, tiered["warmup"])
				}
			})
		}
	}
}
