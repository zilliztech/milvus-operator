package controllers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1alpha1"
	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func TestNamespacedName(t *testing.T) {
	key := NamespacedName("default", "test")
	assert.Equal(t, "default", key.Namespace)
	assert.Equal(t, "test", key.Name)
}

func TestMergeServicePort(t *testing.T) {
	// empty src
	src := []corev1.ServicePort{}
	dst := []corev1.ServicePort{{}}
	ret := MergeServicePort(src, dst)
	assert.Equal(t, dst, ret)

	// empty dst
	src = []corev1.ServicePort{{}}
	dst = []corev1.ServicePort{}
	ret = MergeServicePort(src, dst)
	assert.Equal(t, src, ret)

	// merge same
	src = []corev1.ServicePort{{}}
	dst = []corev1.ServicePort{{}}
	ret = MergeServicePort(src, dst)
	assert.Len(t, ret, 1)

	// merge same
	src = []corev1.ServicePort{{}}
	dst = []corev1.ServicePort{{}}
	ret = MergeServicePort(src, dst)
	assert.Len(t, ret, 1)

	// merge same port
	src = []corev1.ServicePort{{Name: "a", Port: 8080}}
	dst = []corev1.ServicePort{{Name: "b", Port: 8080}}
	ret = MergeServicePort(src, dst)
	assert.Len(t, ret, 1)

	// merge different
	src = []corev1.ServicePort{{Name: "a", Port: 8081}}
	dst = []corev1.ServicePort{{Name: "b", Port: 8080}}
	ret = MergeServicePort(src, dst)
	assert.Len(t, ret, 2)

	// merge same nodePort
	src = []corev1.ServicePort{{Name: "a", NodePort: 3000, Port: 8081}}
	dst = []corev1.ServicePort{{Name: "b", NodePort: 3000, Port: 8080}}
	ret = MergeServicePort(src, dst)
	assert.Len(t, ret, 1)

	// merge different nodePort
	src = []corev1.ServicePort{{Name: "a", NodePort: 3000, Port: 8081}}
	dst = []corev1.ServicePort{{Name: "b", NodePort: 3001, Port: 8080}}
	ret = MergeServicePort(src, dst)
	assert.Len(t, ret, 2)
}

func TestMergeVolumeMount(t *testing.T) {
	// empty src
	src := []corev1.VolumeMount{}
	dst := []corev1.VolumeMount{{}}
	ret := MergeVolumeMount(src, dst)
	assert.Equal(t, dst, ret)

	// empty dst
	src = []corev1.VolumeMount{{}}
	dst = []corev1.VolumeMount{}
	ret = MergeVolumeMount(src, dst)
	assert.Equal(t, src, ret)

	// merge same
	dst = []corev1.VolumeMount{{}}
	src = []corev1.VolumeMount{{}}
	ret = MergeVolumeMount(src, dst)
	assert.Len(t, ret, 1)

	// merge different
	dst = []corev1.VolumeMount{{MountPath: "/p1"}}
	src = []corev1.VolumeMount{{MountPath: "/p2"}}
	ret = MergeVolumeMount(src, dst)
	assert.Len(t, ret, 2)
}

func TestMergeContainerPort(t *testing.T) {
	// empty src
	src := []corev1.ContainerPort{}
	dst := []corev1.ContainerPort{{}}
	ret := MergeContainerPort(src, dst)
	assert.Equal(t, dst, ret)

	// empty dst
	src = []corev1.ContainerPort{{}}
	dst = []corev1.ContainerPort{}
	ret = MergeContainerPort(src, dst)
	assert.Equal(t, src, ret)

	// merge same
	dst = []corev1.ContainerPort{{Name: "a"}}
	src = []corev1.ContainerPort{{Name: "a"}}
	ret = MergeContainerPort(src, dst)
	assert.Len(t, ret, 1)

	// merge different
	dst = []corev1.ContainerPort{{Name: "a"}}
	src = []corev1.ContainerPort{{Name: "b"}}
	ret = MergeContainerPort(src, dst)
	assert.Len(t, ret, 2)
}

func TestMergeEnvVar(t *testing.T) {
	// empty src
	src := []corev1.EnvVar{}
	dst := []corev1.EnvVar{{}}
	ret := MergeEnvVar(src, dst)
	assert.Equal(t, dst, ret)

	// empty dst
	src = []corev1.EnvVar{{}}
	dst = []corev1.EnvVar{}
	ret = MergeEnvVar(src, dst)
	assert.Equal(t, src, ret)

	// merge same
	dst = []corev1.EnvVar{{Name: "a"}}
	src = []corev1.EnvVar{{Name: "a"}}
	ret = MergeEnvVar(src, dst)
	assert.Len(t, ret, 1)

	// merge different
	dst = []corev1.EnvVar{{Name: "a"}}
	src = []corev1.EnvVar{{Name: "b"}}
	ret = MergeEnvVar(src, dst)
	assert.Len(t, ret, 2)
}

func TestGetContainerIndex(t *testing.T) {
	containers := []corev1.Container{
		{Name: "a"},
		{Name: "b"},
	}
	// found
	assert.Equal(t, 0, GetContainerIndex(containers, "a"))
	// not found
	assert.Equal(t, -1, GetContainerIndex(containers, "z"))
}

func TestGetVolumeIndex(t *testing.T) {
	volumes := []corev1.Volume{
		{Name: "a"},
		{Name: "b"},
	}
	// found
	assert.Equal(t, 0, GetVolumeIndex(volumes, "a"))
	// not found
	assert.Equal(t, -1, GetVolumeIndex(volumes, "z"))
}

func TestGetMinioBucket(t *testing.T) {
	config := map[string]interface{}{
		"minio": map[string]interface{}{
			"bucketName": "bucket1",
		},
	}
	// not default
	assert.Equal(t, "bucket1", GetMinioBucket(config))

	// default
	config["minio"] = nil
	assert.Equal(t, defaultBucketName, GetMinioBucket(config))
}

func TestGetVolumeMountIndex(t *testing.T) {
	volumeMounts := []corev1.VolumeMount{
		{MountPath: "a"},
		{MountPath: "b"},
	}
	// found
	assert.Equal(t, 0, GetVolumeMountIndex(volumeMounts, "a"))
	// not found
	assert.Equal(t, -1, GetVolumeMountIndex(volumeMounts, "z"))
}

func TestNewComponentAppLabels(t *testing.T) {
	labels := NewComponentAppLabels("a", "b")
	assert.Equal(t, "a", labels[AppLabelInstance])
	assert.Equal(t, "b", labels[AppLabelComponent])
	assert.Equal(t, "milvus", labels[AppLabelName])
}

func TestNewAppLabels(t *testing.T) {
	labels := NewAppLabels("a")
	assert.Equal(t, "a", labels[AppLabelInstance])
	assert.Equal(t, "milvus", labels[AppLabelName])
}

func TestMergeLabels(t *testing.T) {
	labels := MergeLabels(map[string]string{"a": "b"}, map[string]string{"c": "d"})
	assert.Equal(t, "b", labels["a"])
	assert.Equal(t, "d", labels["c"])
}

func TestIsEqual(t *testing.T) {
	assert.True(t, IsEqual(map[string]string{"a": "b"}, map[string]string{"a": "b"}))
}

func TestDeploymentReady(t *testing.T) {
	status := appsv1.DeploymentStatus{}

	t.Run("notReady", func(t *testing.T) {
		status.Conditions = []appsv1.DeploymentCondition{
			{
				Type:   appsv1.DeploymentAvailable,
				Status: corev1.ConditionFalse,
			},
		}
		assert.False(t, DeploymentReady(status))
	})

	t.Run("ready", func(t *testing.T) {
		status.Conditions = []appsv1.DeploymentCondition{
			{
				Type:   appsv1.DeploymentAvailable,
				Status: corev1.ConditionTrue,
			},
		}
		assert.True(t, DeploymentReady(status))
	})

	t.Run("failure", func(t *testing.T) {
		status.Conditions = []appsv1.DeploymentCondition{
			{
				Type:   appsv1.DeploymentReplicaFailure,
				Status: corev1.ConditionTrue,
			},
		}
		assert.False(t, DeploymentReady(status))
	})
}

func TestPodRunningAndReady(t *testing.T) {
	t.Run("no condition", func(t *testing.T) {
		pod := new(corev1.Pod)
		assert.False(t, PodReady(*pod))
	})
	t.Run("condition not ready", func(t *testing.T) {
		pod := corev1.Pod{
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{
					{
						Type:   corev1.PodReady,
						Status: corev1.ConditionFalse,
					},
				},
			},
		}
		assert.False(t, PodReady(pod))
	})
	t.Run("condition ready", func(t *testing.T) {
		pod := corev1.Pod{
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{
					{
						Type:   corev1.PodReady,
						Status: corev1.ConditionTrue,
					},
				},
			},
		}
		assert.True(t, PodReady(pod))
	})
}

func TestGetConditionStatus(t *testing.T) {
	assert.Equal(t, corev1.ConditionFalse, GetConditionStatus(false))
	assert.Equal(t, corev1.ConditionTrue, GetConditionStatus(true))
}

func TestIsClusterDependencyReady(t *testing.T) {
	// 1 not ready -> not ready
	status := v1beta1.MilvusStatus{
		Conditions: []v1beta1.MilvusCondition{
			{
				Type:   v1beta1.EtcdReady,
				Status: corev1.ConditionTrue,
			},
			{
				Type:   v1beta1.MsgStreamReady,
				Status: corev1.ConditionTrue,
			},
			{
				Type:   v1beta1.StorageReady,
				Status: corev1.ConditionFalse,
			},
		},
	}
	assert.False(t, IsDependencyReady(status.Conditions))
	// all ready -> ready
	status.Conditions[2].Status = corev1.ConditionTrue
	assert.True(t, IsDependencyReady(status.Conditions))
}

func TestGetNotReadyDependencyConditions(t *testing.T) {
	t.Run("all condition not set", func(t *testing.T) {
		conds := []v1beta1.MilvusCondition{}
		ret := GetNotReadyDependencyConditions(conds)
		assert.Len(t, ret, 3)
		assert.Nil(t, ret[v1beta1.EtcdReady])
		assert.Nil(t, ret[v1beta1.MsgStreamReady])
		assert.Nil(t, ret[v1beta1.StorageReady])
	})

	t.Run("all condition ready", func(t *testing.T) {
		conds := []v1beta1.MilvusCondition{
			{
				Type:   v1beta1.EtcdReady,
				Status: corev1.ConditionTrue,
			},
			{
				Type:   v1beta1.MsgStreamReady,
				Status: corev1.ConditionTrue,
			},
			{
				Type:   v1beta1.StorageReady,
				Status: corev1.ConditionTrue,
			},
		}
		ret := GetNotReadyDependencyConditions(conds)
		assert.Len(t, ret, 0)
	})

	t.Run("some condition not ready", func(t *testing.T) {
		conds := []v1beta1.MilvusCondition{
			{
				Type:   v1beta1.EtcdReady,
				Status: corev1.ConditionTrue,
			},
			{
				Type:    v1beta1.MsgStreamReady,
				Status:  corev1.ConditionFalse,
				Reason:  v1beta1.ReasonMsgStreamReady,
				Message: "mq msg",
			},
			{
				Type:   v1beta1.StorageReady,
				Status: corev1.ConditionTrue,
			},
		}
		ret := GetNotReadyDependencyConditions(conds)
		assert.Len(t, ret, 1)
		for k, v := range ret {
			print(k, v.Type)
		}
		assert.NotNil(t, ret[v1beta1.MsgStreamReady])
		assert.Equal(t, v1beta1.ReasonMsgStreamReady, ret[v1beta1.MsgStreamReady].Reason)
		assert.Equal(t, "mq msg", ret[v1beta1.MsgStreamReady].Message)
	})

	t.Run("milvus condition ignored", func(t *testing.T) {
		conds := []v1beta1.MilvusCondition{
			{
				Type:   v1beta1.MilvusReady,
				Status: corev1.ConditionFalse,
			},
			{
				Type:   v1beta1.EtcdReady,
				Status: corev1.ConditionTrue,
			},
		}
		ret := GetNotReadyDependencyConditions(conds)
		assert.Len(t, ret, 2)
	})
}

func TestUpdateCondition(t *testing.T) {
	// append if not existed
	status := v1beta1.MilvusStatus{
		Conditions: []v1beta1.MilvusCondition{
			{
				Type:   v1beta1.StorageReady,
				Status: corev1.ConditionFalse,
			},
		},
	}
	condition := v1beta1.MilvusCondition{
		Type:    v1beta1.MsgStreamReady,
		Status:  corev1.ConditionFalse,
		Reason:  "NotReady",
		Message: "Pulsar is not ready",
	}
	UpdateCondition(&status, condition)
	assert.Len(t, status.Conditions, 2)
	assert.Equal(t, status.Conditions[1].Status, condition.Status)
	assert.Equal(t, status.Conditions[1].Type, condition.Type)

	// update existed
	condition2 := v1beta1.MilvusCondition{
		Type:    v1beta1.MsgStreamReady,
		Status:  corev1.ConditionTrue,
		Reason:  "Ready",
		Message: "Pulsar is ready",
	}
	UpdateCondition(&status, condition2)
	assert.Len(t, status.Conditions, 2)
	assert.Equal(t, status.Conditions[1].Status, condition2.Status)
	assert.Equal(t, status.Conditions[1].Type, condition2.Type)
}

func TestGetMinioSecure(t *testing.T) {
	conf := map[string]interface{}{
		"minio": map[string]interface{}{
			"useSSL": true,
		},
	}
	assert.True(t, GetMinioSecure(conf))

	conf = map[string]interface{}{
		"minio": map[string]interface{}{
			"useSSL": false,
		},
	}
	assert.False(t, GetMinioSecure(conf))

	conf = map[string]interface{}{}
	assert.False(t, GetMinioSecure(conf))
}

func TestGetMinioUseIAM(t *testing.T) {
	conf := map[string]interface{}{
		"minio": map[string]interface{}{
			"useIAM": true,
		},
	}
	assert.True(t, GetMinioUseIAM(conf))

	conf = map[string]interface{}{}
	assert.False(t, GetMinioUseIAM(conf))
}

func TestGetMinioIAMEndpoint(t *testing.T) {
	conf := map[string]interface{}{
		"minio": map[string]interface{}{
			"iamEndpoint": "e",
		},
	}
	assert.Equal(t, "e", GetMinioIAMEndpoint(conf))

	conf = map[string]interface{}{}
	assert.Empty(t, GetMinioIAMEndpoint(conf))
}

func TestDiffObject(t *testing.T) {
	obj1 := &v1beta1.Milvus{
		ObjectMeta: metav1.ObjectMeta{
			Name: "obj1",
		},
	}
	obj2 := &v1beta1.Milvus{
		ObjectMeta: metav1.ObjectMeta{
			Name: "obj2",
		},
	}
	diff, err := diffObject(obj1, obj2)
	assert.NoError(t, err)
	assert.Equal(t, `{"metadata":{"name":"obj2"}}`, string(diff))
}

func TestInt32Ptr(t *testing.T) {
	assert.Equal(t, int32(1), *int32Ptr(1))
	assert.Equal(t, int32(0), *int32Ptr(0))
}

func TestGetFuncName(t *testing.T) {
	assert.Equal(t, "getFuncName", getFuncName(getFuncName))
}

func TestLoopWithInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var callCnt int32 = 0
	loopFunc := func() error {
		atomic.AddInt32(&callCnt, 1)
		return errors.New("test")
	}
	logger := logf.Log.WithName("test")
	go LoopWithInterval(ctx, loopFunc, time.Second/10, logger)
	time.Sleep(time.Second)
	assert.Less(t, int32(9), atomic.LoadInt32(&callCnt))
}

func TestSetControllerReference(t *testing.T) {
	t.Run("MilvusCluster to v1beta1 Milvus OK", func(t *testing.T) {
		controlled := &appsv1.Deployment{}
		oldScheme, err := v1alpha1.SchemeBuilder.Build()
		assert.NoError(t, err)
		err = SetControllerReference(&v1alpha1.MilvusCluster{}, controlled, oldScheme)
		assert.NoError(t, err)
		scheme, err := v1beta1.SchemeBuilder.Build()
		assert.NoError(t, err)
		err = SetControllerReference(&v1beta1.Milvus{}, controlled, scheme)
		assert.NoError(t, err)
	})

	t.Run("v1alpha1 Milvus to v1beta1 Milvus error", func(t *testing.T) {
		controlled := &appsv1.Deployment{}
		oldScheme, err := v1alpha1.SchemeBuilder.Build()
		assert.NoError(t, err)
		err = SetControllerReference(&v1alpha1.Milvus{}, controlled, oldScheme)
		assert.NoError(t, err)
		scheme, err := v1beta1.SchemeBuilder.Build()
		assert.NoError(t, err)
		err = SetControllerReference(&v1beta1.Milvus{}, controlled, scheme)
		assert.NoError(t, err)
	})
}

func Test_int64Ptr(t *testing.T) {
	assert.Equal(t, int64(10), *int64Ptr(10))
}

func Test_boolPtr(t *testing.T) {
	assert.True(t, *boolPtr(true))
}
