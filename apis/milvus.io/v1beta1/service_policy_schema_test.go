package v1beta1

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/kube-openapi/pkg/validation/spec"
	"k8s.io/kube-openapi/pkg/validation/strfmt"
	"k8s.io/kube-openapi/pkg/validation/validate"
	"sigs.k8s.io/yaml"

	"github.com/zilliztech/milvus-operator/pkg/util"
)

func TestServiceIPFamilyPolicySchema(t *testing.T) {
	for _, name := range []string{"milvus.io_milvuses.yaml", "milvus.io_milvusclusters.yaml"} {
		data, err := os.ReadFile(filepath.Join(util.GetGitRepoRootDir(), "config/crd/bases", name))
		require.NoError(t, err)
		var crd apiextensionsv1.CustomResourceDefinition
		require.NoError(t, yaml.Unmarshal(data, &crd))
		for _, version := range crd.Spec.Versions {
			data, err := json.Marshal(version.Schema.OpenAPIV3Schema)
			require.NoError(t, err)
			var schema spec.Schema
			require.NoError(t, json.Unmarshal(data, &schema))
			paths := [][]string{{"spec", "components", "proxy"}, {"spec", "components", "standalone"}}
			if crd.Spec.Names.Kind == "Milvus" && version.Name == "v1alpha1" {
				paths = [][]string{{"spec"}}
			}
			for _, path := range paths {
				t.Run(crd.Spec.Names.Kind+"/"+version.Name+"/"+strings.Join(path, "."), func(t *testing.T) {
					parent := *version.Schema.OpenAPIV3Schema
					for _, key := range path {
						parent = parent.Properties[key]
					}
					policy, ok := parent.Properties["serviceIPFamilyPolicy"]
					require.True(t, ok, "the generated CRD must expose the field")
					assert.Nil(t, policy.Default, "existing installations must remain opt-in")
					assert.NotContains(t, parent.Required, "serviceIPFamilyPolicy")

					for _, value := range []string{"", "SingleStack", "PreferDualStack", "RequireDualStack", "InvalidPolicy"} {
						object := map[string]interface{}{
							"apiVersion": crd.Spec.Group + "/" + version.Name,
							"kind":       crd.Spec.Names.Kind,
							"metadata":   map[string]interface{}{"name": "policy-test"},
						}
						current := object
						for _, key := range path {
							child := map[string]interface{}{}
							current[key] = child
							current = child
						}
						if value != "" {
							current["serviceIPFamilyPolicy"] = value
						}
						err := validate.AgainstSchema(&schema, object, strfmt.Default)
						if value == "InvalidPolicy" {
							assert.ErrorContains(t, err, "serviceIPFamilyPolicy")
						} else {
							assert.NoError(t, err, "policy %q", value)
						}
					}
				})
			}
		}
	}
}
