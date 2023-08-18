package controllers

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	networkingv1 "k8s.io/api/networking/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *MilvusReconciler) ReconcileIngress(ctx context.Context, mc v1beta1.Milvus) error {
	ingress := mc.Spec.GetServiceComponent().Ingress
	if ingress == nil {
		return nil
	}
	return reconcileIngress(ctx, r.logger, r.Client, r.Scheme, &mc, *ingress)
}

func reconcileIngress(ctx context.Context, logger logr.Logger,
	cli client.Client, scheme *runtime.Scheme, crd client.Object, ingress v1beta1.MilvusIngress) error {

	new := ingressRenderer.Render(crd, ingress)

	if err := SetControllerReference(crd, new, scheme); err != nil {
		return errors.Wrap(err, "failed to set controller reference")
	}

	old := &networkingv1.Ingress{}
	key := client.ObjectKeyFromObject(crd)
	key.Name = key.Name + "-milvus"
	err := cli.Get(ctx, key, old)
	if kerrors.IsNotFound(err) {
		logger.Info("Create Ingress")
		err = cli.Create(ctx, new)
		return errors.Wrap(err, "failed to create ingress")
	} else if err != nil {
		return errors.Wrap(err, "failed to get ingress")
	}

	if IsEqual(old.Spec, new.Spec) && IsEqual(old.ObjectMeta, new.ObjectMeta) {
		return nil
	}
	// we don't change status & type
	new.TypeMeta = old.TypeMeta
	// merge metadata
	meta := old.ObjectMeta.DeepCopy()
	meta.Labels = MergeLabels(old.Labels, new.Labels)
	meta.Annotations = MergeLabels(old.Annotations, new.Annotations)
	new.ObjectMeta = *meta
	new.Status = *old.Status.DeepCopy()

	logger.Info("Update Ingress")
	err = cli.Update(ctx, new)
	return errors.Wrap(err, "failed to update ingress")
}

//go:generate mockgen -package=controllers -source=ingress.go -destination=ingress_mock.go ingressRendererInterface
type ingressRendererInterface interface {
	Render(crd client.Object, spec v1beta1.MilvusIngress) *networkingv1.Ingress
}

// ingressRender singleton
var ingressRenderer ingressRendererInterface = new(ingressRendererImpl)

type ingressRendererImpl struct {
}

// Render implements ingressRenderInterface
func (r *ingressRendererImpl) Render(crd client.Object, spec v1beta1.MilvusIngress) *networkingv1.Ingress {
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crd.GetName() + "-milvus",
			Namespace: crd.GetNamespace(),
			// TODO: we need provide merge common labels of milvus
			Labels:      spec.Labels,
			Annotations: spec.Annotations,
		},
	}

	ingress.Spec.IngressClassName = spec.IngressClassName

	for secret, hosts := range spec.TLSSecretRefs {
		ingress.Spec.TLS = append(ingress.Spec.TLS, networkingv1.IngressTLS{
			Hosts:      hosts,
			SecretName: secret,
		})
	}

	pathType := networkingv1.PathTypePrefix

	for _, host := range spec.Hosts {
		ingress.Spec.Rules = append(ingress.Spec.Rules, networkingv1.IngressRule{
			Host: host,
			IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{
						{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: GetServiceInstanceName(crd.GetName()),
									Port: networkingv1.ServiceBackendPort{
										Number: MilvusPort,
									},
								},
							},
						},
					},
				},
			},
		})
	}
	return ingress
}
