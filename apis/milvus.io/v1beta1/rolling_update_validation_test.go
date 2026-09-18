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
