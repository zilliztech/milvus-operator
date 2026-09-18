package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestMilvusValidateRollingUpdates(t *testing.T) {
	ptr := func(v intstr.IntOrString) *intstr.IntOrString { return &v }
	for _, tc := range []struct {
		name               string
		surge, unavailable *intstr.IntOrString
		invalid            bool
	}{
		{"defaults", nil, nil, false},
		{"zero surge", ptr(intstr.FromInt(0)), ptr(intstr.FromInt(1)), false},
		{"zero percent surge", ptr(intstr.FromString("0%")), ptr(intstr.FromString("20%")), false},
		{"small positive percentage", ptr(intstr.FromInt(0)), ptr(intstr.FromString("1%")), false},
		{"zero unavailable", ptr(intstr.FromInt(1)), ptr(intstr.FromInt(0)), false},
		{"default surge", nil, ptr(intstr.FromInt(0)), false},
		{"surge over 100 percent", ptr(intstr.FromString("200%")), nil, false},
		{"both zero", ptr(intstr.FromInt(0)), ptr(intstr.FromInt(0)), true},
		{"both zero percent", ptr(intstr.FromString("0%")), ptr(intstr.FromString("0%")), true},
		{"unavailable omitted", ptr(intstr.FromInt(0)), nil, true},
		{"negative surge", ptr(intstr.FromInt(-1)), nil, true},
		{"negative unavailable", nil, ptr(intstr.FromInt(-1)), true},
		{"negative percentage", ptr(intstr.FromString("-1%")), nil, true},
		{"invalid percentage", ptr(intstr.FromString("abc%")), nil, true},
		{"string integer", ptr(intstr.FromString("1")), nil, true},
		{"unavailable over 100 percent", nil, ptr(intstr.FromString("101%")), true},
		{"unknown type", ptr(intstr.IntOrString{Type: 2}), nil, true},
		{"overflow percentage", ptr(intstr.FromString("9999999999999999999999999999%")), nil, true},
	} {
		for _, scope := range []string{"global", "queryNode", "standalone", "streamingNode", "cdc"} {
			t.Run(tc.name+"/"+scope, func(t *testing.T) {
				mc := &Milvus{}
				mc.Spec.Mode = MilvusModeCluster
				mc.Default()
				ru := &appsv1.RollingUpdateDeployment{MaxSurge: tc.surge, MaxUnavailable: tc.unavailable}
				path := "spec.components."
				switch scope {
				case "global":
					mc.Spec.Com.RollingUpdate = ru
				case "queryNode":
					mc.Spec.Com.QueryNode.RollingUpdate = ru
					path += "queryNode."
				case "standalone":
					mc.Spec.Com.Standalone.RollingUpdate = ru
					path += "standalone."
				case "streamingNode":
					mc.Spec.Com.StreamingNode = &MilvusStreamingNode{Component: Component{ComponentSpec: ComponentSpec{RollingUpdate: ru}}}
					path += "streamingNode."
				case "cdc":
					mc.Spec.Com.Cdc = &MilvusCdc{Component: Component{ComponentSpec: ComponentSpec{RollingUpdate: ru}}}
					path += "cdc."
				}
				_, createErr := mc.ValidateCreate()
				_, updateErr := mc.ValidateUpdate(mc.DeepCopy())
				if tc.invalid {
					require.Error(t, createErr)
					require.Error(t, updateErr)
					assert.Contains(t, createErr.Error(), path+"rollingUpdate")
				} else {
					assert.NoError(t, createErr)
					assert.NoError(t, updateErr)
				}
			})
		}
	}
}

func TestRollingUpdateInheritanceValidation(t *testing.T) {
	zero, one, two := intstr.FromInt(0), intstr.FromInt(1), intstr.FromInt(2)
	for _, tc := range []struct {
		name              string
		global, component *appsv1.RollingUpdateDeployment
		invalid           bool
	}{
		{"inherit all", &appsv1.RollingUpdateDeployment{MaxSurge: &zero, MaxUnavailable: &one}, nil, false},
		{"inherit unavailable", &appsv1.RollingUpdateDeployment{MaxUnavailable: &one}, &appsv1.RollingUpdateDeployment{MaxSurge: &zero}, false},
		{"inherit surge", &appsv1.RollingUpdateDeployment{MaxSurge: &two, MaxUnavailable: &one}, &appsv1.RollingUpdateDeployment{MaxUnavailable: &zero}, false},
		{"override produces both zero", &appsv1.RollingUpdateDeployment{MaxSurge: &zero, MaxUnavailable: &one}, &appsv1.RollingUpdateDeployment{MaxUnavailable: &zero}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mc := &Milvus{}
			mc.Spec.Mode = MilvusModeCluster
			mc.Default()
			mc.Spec.Com.RollingUpdate = tc.global.DeepCopy()
			mc.Spec.Com.QueryNode.RollingUpdate = tc.component.DeepCopy()
			before := mc.DeepCopy()
			_, createErr := mc.ValidateCreate()
			_, updateErr := mc.ValidateUpdate(before)
			if tc.invalid {
				require.Error(t, createErr)
				require.Error(t, updateErr)
				assert.Contains(t, createErr.Error(), "queryNode.rollingUpdate")
			} else {
				require.NoError(t, createErr)
				require.NoError(t, updateErr)
			}
			assert.Equal(t, before.Spec.Com.RollingUpdate, mc.Spec.Com.RollingUpdate)
			assert.Equal(t, before.Spec.Com.QueryNode.RollingUpdate, mc.Spec.Com.QueryNode.RollingUpdate)
		})
	}
}
