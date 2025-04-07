package controllers

import (
	"context"
	"testing"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlRuntime "sigs.k8s.io/controller-runtime"
)

func Test_MilvusUpgradeReconciler_Reconcile(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockClient := NewMockK8sClient(ctrl)
	mockStatusCli := NewMockK8sStatusClient(ctrl)
	scheme := runtime.NewScheme()
	v1beta1.AddToScheme(scheme)

	r := MilvusUpgradeReconciler{
		Client: mockClient,
		Scheme: scheme,
	}

	ctx := context.Background()
	req := ctrlRuntime.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test",
			Namespace: "ns",
		},
	}
	errMock := errors.New("mock")
	t.Run("Get MilvusUpgrade failed", func(t *testing.T) {
		defer ctrl.Finish()
		mockClient.EXPECT().Get(ctx, req.NamespacedName, gomock.Any()).Return(kerrors.NewInternalError(errMock))
		_, err := r.Reconcile(ctx, req)
		assert.Error(t, err)
	})

	t.Run("Get MilvusUpgrade not found ok", func(t *testing.T) {
		defer ctrl.Finish()
		mockClient.EXPECT().Get(ctx, req.NamespacedName, gomock.Any()).Return(kerrors.NewNotFound(v1beta1.Resource(v1beta1.MilvusUpgradeKind), "test"))
		_, err := r.Reconcile(ctx, req)
		assert.NoError(t, err)
	})

	milvusUp := v1beta1.MilvusUpgrade{}
	milvusUp.Spec.Milvus = v1beta1.ObjectReference{
		Name:      "milvus1",
		Namespace: "ns1",
	}
	mockGetMilvusUpSuccess := func() {
		mockClient.EXPECT().Get(ctx, req.NamespacedName, gomock.Any()).DoAndReturn(func(ctx context.Context, name types.NamespacedName, obj runtime.Object, opt ...any) error {
			assert.Equal(t, name, req.NamespacedName)
			mu := obj.(*v1beta1.MilvusUpgrade)
			*mu = milvusUp
			return nil
		})
	}
	commonPrepareFn := []func(){
		mockGetMilvusUpSuccess,
	}
	t.Run("Milvus not found err, update status failed", func(t *testing.T) {
		defer ctrl.Finish()
		for _, fn := range commonPrepareFn {
			fn()
		}
		mockClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(kerrors.NewNotFound(v1beta1.Resource("Milvus"), "milvus1"))
		mockClient.EXPECT().Status().Return(mockStatusCli)
		mockStatusCli.EXPECT().Update(ctx, gomock.Any()).Return(kerrors.NewInternalError(errMock))
		_, err := r.Reconcile(ctx, req)
		assert.Error(t, err)
	})

	t.Run("Milvus not found err, update status failed", func(t *testing.T) {
		defer ctrl.Finish()
		for _, fn := range commonPrepareFn {
			fn()
		}
		mockClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(kerrors.NewNotFound(v1beta1.Resource("Milvus"), "milvus1"))
		mockClient.EXPECT().Status().Return(mockStatusCli)
		mockStatusCli.EXPECT().Update(ctx, gomock.Any()).Return(nil)
		_, err := r.Reconcile(ctx, req)
		assert.Error(t, err)
	})
}
