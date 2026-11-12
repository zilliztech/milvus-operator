package v1beta1_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"

	v1alpha1 "github.com/zilliztech/milvus-operator/apis/milvus.io/v1alpha1"
	v1beta1 "github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

type webhookManager struct {
	manager.Manager
	scheme   *runtime.Scheme
	server   webhook.Server
	registry conversion.Registry
}

func (m *webhookManager) GetConfig() *rest.Config {
	return &rest.Config{Host: "http://127.0.0.1:65534"}
}
func (m *webhookManager) GetScheme() *runtime.Scheme                { return m.scheme }
func (m *webhookManager) GetWebhookServer() webhook.Server          { return m.server }
func (m *webhookManager) GetConverterRegistry() conversion.Registry { return m.registry }

func TestRegisteredResourceWebhooks(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1beta1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	mgr := &webhookManager{scheme: scheme, server: webhook.NewServer(webhook.Options{}), registry: conversion.NewRegistry()}
	require.NoError(t, (&v1beta1.Milvus{}).SetupWebhookWithManager(mgr))
	require.NoError(t, (&v1beta1.MilvusUpgrade{}).SetupWebhookWithManager(mgr))
	require.NoError(t, (&v1alpha1.Milvus{}).SetupWebhookWithManager(mgr))
	// Conversion registration must remain available alongside the typed hooks.
	handler, path := mgr.server.WebhookMux().Handler(httptest.NewRequest(http.MethodPost, "/convert", nil))
	require.NotNil(t, handler)
	require.Equal(t, "/convert", path)

	call := func(t *testing.T, path string, op admissionv1.Operation, obj, old []byte) *admissionv1.AdmissionResponse {
		t.Helper()
		review := admissionv1.AdmissionReview{
			TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"},
			Request:  &admissionv1.AdmissionRequest{UID: "fixture", Operation: op, Object: runtime.RawExtension{Raw: obj}, OldObject: runtime.RawExtension{Raw: old}},
		}
		body, err := json.Marshal(review)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		mgr.server.WebhookMux().ServeHTTP(recorder, req)
		require.Equal(t, http.StatusOK, recorder.Code)
		var response admissionv1.AdmissionReview
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.NotNil(t, response.Response)
		require.Equal(t, review.Request.UID, response.Response.UID)
		return response.Response
	}
	for _, tc := range []struct {
		name    string
		path    string
		raw     string
		invalid string
	}{
		{name: "Milvus", path: "milvus", raw: `{"apiVersion":"milvus.io/v1beta1","kind":"Milvus","metadata":{"name":"fixture"}}`, invalid: `{"apiVersion":"milvus.io/v1beta1","kind":"Milvus","metadata":{"name":"fixture"},"spec":{"components":{"rollingMode":99}}}`},
		{name: "MilvusUpgrade", path: "milvusupgrade", raw: `{"apiVersion":"milvus.io/v1beta1","kind":"MilvusUpgrade","metadata":{"name":"fixture"},"spec":{"sourceVersion":"2.1.0","targetVersion":"2.2.0"}}`, invalid: `{"apiVersion":"milvus.io/v1beta1","kind":"MilvusUpgrade","metadata":{"name":"fixture"},"spec":{"sourceVersion":"1.0.0","targetVersion":"2.2.0"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutation := call(t, "/mutate-milvus-io-v1beta1-"+tc.path, admissionv1.Create, []byte(tc.raw), nil)
			require.True(t, mutation.Allowed)
			require.NotEmpty(t, mutation.Patch)
			patch, err := jsonpatch.DecodePatch(mutation.Patch)
			require.NoError(t, err)
			defaulted, err := patch.Apply([]byte(tc.raw))
			require.NoError(t, err)
			validatePath := "/validate-milvus-io-v1beta1-" + tc.path
			require.True(t, call(t, validatePath, admissionv1.Create, defaulted, nil).Allowed)
			require.False(t, call(t, validatePath, admissionv1.Create, []byte(tc.invalid), nil).Allowed)
			require.True(t, call(t, validatePath, admissionv1.Update, defaulted, defaulted).Allowed)
			require.False(t, call(t, validatePath, admissionv1.Update, []byte(tc.invalid), defaulted).Allowed)
			require.True(t, call(t, validatePath, admissionv1.Delete, nil, defaulted).Allowed)
		})
	}
}
