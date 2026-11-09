package helm

import (
	"maps"
	"slices"
	"strings"

	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/releaseutil"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// keepPVCs protects chart-managed claims without changing other release resources.
func keepPVCs(manifest string) (string, bool, error) {
	manifests := releaseutil.SplitManifests(manifest)
	changed := false
	var result strings.Builder
	for _, name := range slices.Sorted(maps.Keys(manifests)) {
		content := manifests[name]
		var obj unstructured.Unstructured
		if err := yaml.Unmarshal([]byte(content), &obj.Object); err != nil {
			return "", false, err
		}
		if obj.GetAPIVersion() == "v1" && obj.GetKind() == "PersistentVolumeClaim" {
			annotations := obj.GetAnnotations()
			if annotations[kube.ResourcePolicyAnno] != kube.KeepPolicy {
				if annotations == nil {
					annotations = make(map[string]string)
				}
				annotations[kube.ResourcePolicyAnno] = kube.KeepPolicy
				obj.SetAnnotations(annotations)
				data, err := yaml.Marshal(obj.Object)
				if err != nil {
					return "", false, err
				}
				content = string(data)
				changed = true
			}
		}
		result.WriteString("---\n")
		result.WriteString(content)
		result.WriteByte('\n')
	}
	if !changed {
		return manifest, false, nil
	}
	return result.String(), true, nil
}
