/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	v1beta1 "github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func (r *Milvus) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// ConvertTo converts this Milvus to the Hub version (v1beta1).
func (r *Milvus) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*v1beta1.Milvus)
	dst.ObjectMeta = r.ObjectMeta
	r.Spec.ConvertSpecTo(&dst.Spec)
	dst.Status = r.Status
	return nil
}

func (r *MilvusSpec) ConvertSpecTo(dst *v1beta1.MilvusSpec) {
	dst.Mode = v1beta1.MilvusModeStandalone
	dst.Conf = r.Conf
	dst.Dep = r.Dep
	dst.Dep.RocksMQ.Persistence = r.Persistence
	dst.Com = v1beta1.MilvusComponents{
		DisableMetric: r.DisableMetric,
		Standalone: &v1beta1.MilvusStandalone{
			ServiceComponent: v1beta1.ServiceComponent{
				Component: v1beta1.Component{
					ComponentSpec: r.ComponentSpec,
					Replicas:      r.Replicas,
				},
				ServiceType:        r.ServiceType,
				ServiceLabels:      r.ServiceLabels,
				ServiceAnnotations: r.ServiceAnnotations,
				Ingress:            r.Ingress,
			},
		},
	}
}

// ConvertFrom converts from the Hub version to this version.
func (r *Milvus) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*v1beta1.Milvus)
	r.Spec.Conf = src.Spec.Conf
	r.Spec.Dep = src.Spec.Dep
	r.Spec.DisableMetric = src.Spec.Com.DisableMetric
	if src.Spec.Com.Standalone == nil {
		src.Spec.Com.Standalone = &v1beta1.MilvusStandalone{}
	}
	r.Spec.ComponentSpec = src.Spec.Com.Standalone.ComponentSpec

	r.Spec.Replicas = src.Spec.Com.Standalone.Replicas
	r.Spec.ServiceType = src.Spec.Com.Standalone.ServiceType
	r.Spec.ServiceLabels = src.Spec.Com.Standalone.ServiceLabels
	r.Spec.ServiceAnnotations = src.Spec.Com.Standalone.ServiceAnnotations
	r.Spec.Ingress = src.Spec.Com.Standalone.Ingress

	r.ObjectMeta = src.ObjectMeta
	r.Status = src.Status
	return nil
}
