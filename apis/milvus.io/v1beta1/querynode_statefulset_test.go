package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMilvusValidateQueryNodeStatefulSet(t *testing.T) {
	newMilvus := func() *Milvus {
		mc := &Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc"}}
		mc.Spec.Mode = MilvusModeCluster
		mc.Default()
		return mc
	}
	replicas := int32(2)

	validTemplate := func() Values {
		v := Values{}
		if err := v.FromObject(corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "data"},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			},
		}); err != nil {
			t.Fatal(err)
		}
		return v
	}

	t.Run("valid statefulset spec", func(t *testing.T) {
		mc := newMilvus()
		mc.Spec.Com.QueryNode.StatefulSet = &QueryNodeStatefulSet{
			Enabled:              true,
			VolumeClaimTemplates: []Values{validTemplate()},
		}
		_, err := mc.ValidateCreate()
		assert.NoError(t, err)
	})

	t.Run("disabled statefulset is ignored", func(t *testing.T) {
		mc := newMilvus()
		mc.Spec.Com.QueryNode.StatefulSet = &QueryNodeStatefulSet{Enabled: false}
		mc.Spec.Com.QueryNode.Groups = []DeploymentGroup{{Name: "az-1", Replicas: &replicas}}
		_, err := mc.ValidateCreate()
		assert.NoError(t, err)
	})

	t.Run("valid statefulset combined with groups", func(t *testing.T) {
		mc := newMilvus()
		mc.Spec.Com.QueryNode.StatefulSet = &QueryNodeStatefulSet{
			Enabled:              true,
			VolumeClaimTemplates: []Values{validTemplate()},
		}
		mc.Spec.Com.QueryNode.Groups = []DeploymentGroup{{Name: "az-1", Replicas: &replicas}}
		_, err := mc.ValidateCreate()
		assert.NoError(t, err)
	})

	t.Run("rejects statefulset with rollingMode v3", func(t *testing.T) {
		mc := newMilvus()
		mc.Spec.Com.RollingMode = RollingModeV3
		mc.Spec.Com.QueryNode.StatefulSet = &QueryNodeStatefulSet{Enabled: true}
		_, err := mc.ValidateCreate()
		assert.Error(t, err)
	})

	t.Run("rejects invalid volumeClaimTemplate", func(t *testing.T) {
		mc := newMilvus()
		bad := Values{}
		bad.Data = map[string]any{"spec": "not-an-object"}
		mc.Spec.Com.QueryNode.StatefulSet = &QueryNodeStatefulSet{
			Enabled:              true,
			VolumeClaimTemplates: []Values{bad},
		}
		_, err := mc.ValidateCreate()
		assert.Error(t, err)
	})

	t.Run("StatefulSetEnabled helper", func(t *testing.T) {
		var qn *MilvusQueryNode
		assert.False(t, qn.StatefulSetEnabled())
		qn = &MilvusQueryNode{}
		assert.False(t, qn.StatefulSetEnabled())
		qn.StatefulSet = &QueryNodeStatefulSet{Enabled: false}
		assert.False(t, qn.StatefulSetEnabled())
		qn.StatefulSet.Enabled = true
		assert.True(t, qn.StatefulSetEnabled())
	})
}
