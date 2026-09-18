package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

// Exercise the actual manager cache against a WatchList stream: without
// allowWatchBookmarks the server cannot signal the end of initial events.
func TestManagerCacheWatchListSync(t *testing.T) {
	requests := make(chan url.Values, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		switch r.URL.Path {
		case "/api":
			_ = encoder.Encode(metav1.APIVersions{Versions: []string{"v1"}})
		case "/apis":
			_ = encoder.Encode(metav1.APIGroupList{Groups: []metav1.APIGroup{{
				Name:             "milvus.io",
				Versions:         []metav1.GroupVersionForDiscovery{{GroupVersion: "milvus.io/v1beta1", Version: "v1beta1"}},
				PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: "milvus.io/v1beta1", Version: "v1beta1"},
			}}})
		case "/api/v1":
			_ = encoder.Encode(metav1.APIResourceList{GroupVersion: "v1"})
		case "/apis/milvus.io/v1beta1":
			_ = encoder.Encode(metav1.APIResourceList{GroupVersion: "milvus.io/v1beta1", APIResources: []metav1.APIResource{{
				Name: "milvuses", Kind: "Milvus", Namespaced: true, Verbs: []string{"get", "list", "watch"},
			}}})
		case "/apis/milvus.io/v1beta1/milvuses":
			query := r.URL.Query()
			select {
			case requests <- query:
			default:
			}
			if query.Get("watch") != "true" {
				http.Error(w, "test requires initial WatchList", http.StatusBadRequest)
				return
			}
			if query.Get("sendInitialEvents") == "true" {
				_ = encoder.Encode(map[string]any{"type": "ADDED", "object": map[string]any{
					"apiVersion": "milvus.io/v1beta1", "kind": "Milvus",
					"metadata": map[string]any{"name": "existing", "namespace": "default", "resourceVersion": "1"},
				}})
				if query.Get("allowWatchBookmarks") == "true" {
					_ = encoder.Encode(map[string]any{"type": "BOOKMARK", "object": map[string]any{
						"apiVersion": "milvus.io/v1beta1", "kind": "Milvus",
						"metadata": map[string]any{"resourceVersion": "1", "annotations": map[string]string{"k8s.io/initial-events-end": "true"}},
					}})
				}
			}
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	kubeconfig := clientcmdapi.Config{
		Clusters:       map[string]*clientcmdapi.Cluster{"test": {Server: server.URL}},
		Contexts:       map[string]*clientcmdapi.Context{"test": {Cluster: "test"}},
		CurrentContext: "test",
	}
	path := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, clientcmd.WriteToFile(kubeconfig, path))
	t.Setenv("KUBECONFIG", path)

	mgr, err := NewManager(100, 100, "0", "0", false)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = mgr.GetCache().GetInformer(ctx, &v1beta1.Milvus{}, cache.BlockUntilSynced(false))
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- mgr.GetCache().Start(ctx) }()
	require.True(t, mgr.GetCache().WaitForCacheSync(ctx), "WatchList must receive the initial-events-end bookmark")
	obj := &v1beta1.Milvus{}
	require.NoError(t, mgr.GetCache().Get(ctx, client.ObjectKey{Namespace: "default", Name: "existing"}, obj))
	select {
	case query := <-requests:
		require.Equal(t, "true", query.Get("sendInitialEvents"))
		require.Equal(t, "true", query.Get("allowWatchBookmarks"))
	default:
		t.Fatal("expected a WatchList request")
	}
	cancel()
	require.NoError(t, <-done)
}
