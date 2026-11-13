package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func TestMilvus_ServiceIPFamilyPolicyConversion(t *testing.T) {
	tests := []struct {
		name   string
		policy *corev1.IPFamilyPolicy
	}{
		{name: "omitted"},
		{name: "single stack", policy: ptr.To(corev1.IPFamilyPolicySingleStack)},
		{name: "prefer dual stack", policy: ptr.To(corev1.IPFamilyPolicyPreferDualStack)},
		{name: "require dual stack", policy: ptr.To(corev1.IPFamilyPolicyRequireDualStack)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := &Milvus{Spec: MilvusSpec{ServiceIPFamilyPolicy: tt.policy}}
			hub := &v1beta1.Milvus{}
			require.NoError(t, original.ConvertTo(hub))
			assert.Equal(t, tt.policy, hub.Spec.Com.Standalone.ServiceIPFamilyPolicy)
			roundTrip := &Milvus{}
			require.NoError(t, roundTrip.ConvertFrom(hub))
			assert.Equal(t, tt.policy, roundTrip.Spec.ServiceIPFamilyPolicy)

			if tt.policy != nil {
				want := *tt.policy
				alphaCopy := original.DeepCopy()
				*alphaCopy.Spec.ServiceIPFamilyPolicy = "changed"
				assert.Equal(t, want, *original.Spec.ServiceIPFamilyPolicy, "deep copy should not alias the policy")
				hubCopy := hub.DeepCopy()
				*hubCopy.Spec.Com.Standalone.ServiceIPFamilyPolicy = "changed"
				assert.Equal(t, want, *hub.Spec.Com.Standalone.ServiceIPFamilyPolicy, "deep copy should not alias the policy")
			}
		})
	}
}

func TestMilvusAdditionalPortConversion(t *testing.T) {
	for _, port := range []int32{0, 19632} {
		original := &Milvus{Spec: MilvusSpec{AdditionalPort: port}}
		hub := &v1beta1.Milvus{}
		require.NoError(t, original.ConvertTo(hub))
		require.Equal(t, port, hub.Spec.Com.Standalone.AdditionalPort)
		roundTrip := &Milvus{}
		require.NoError(t, roundTrip.ConvertFrom(hub))
		require.Equal(t, port, roundTrip.Spec.AdditionalPort)
	}
}
