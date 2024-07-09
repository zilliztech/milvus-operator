package controllers

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestMilvusReconciler_ReconcilePVCs(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	ctx := env.ctx
	m := env.Inst
	errNotFound := kerrors.NewNotFound(schema.GroupResource{}, "")

	t.Run("persistence_disabled", func(t *testing.T) {
		err := r.ReconcilePVCs(ctx, m)
		assert.NoError(t, err)
	})

	m.Spec.Dep.RocksMQ.Persistence.Enabled = true
	m.Spec.Dep.RocksMQ.Persistence.PersistentVolumeClaim.ExistingClaim = "claim"
	t.Run("using_existing", func(t *testing.T) {
		err := r.ReconcilePVCs(ctx, m)
		assert.NoError(t, err)
	})

	m.Spec.Dep.RocksMQ.Persistence.PersistentVolumeClaim.ExistingClaim = ""
	m.Namespace = "ns"
	m.Name = "name"
	errMock := errors.New("mock")
	t.Run("sync:get_old_failed", func(t *testing.T) {
		defer env.Ctrl.Finish()
		mockClient.EXPECT().Get(ctx, NamespacedName(m.Namespace, getPVCNameByInstName(m.Name)), gomock.Any()).Return(errMock)
		err := r.ReconcilePVCs(ctx, m)
		assert.Error(t, err)
	})

	t.Run("sync:create_new", func(t *testing.T) {
		defer env.Ctrl.Finish()
		mockClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
		mockClient.EXPECT().Create(ctx, gomock.Any()).Return(nil)
		err := r.ReconcilePVCs(ctx, m)
		assert.NoError(t, err)
	})

	t.Run("sync:no_update", func(t *testing.T) {
		defer env.Ctrl.Finish()
		mockClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Do(func(_, _, obj interface{}, opts ...any) {
			pvc := obj.(*corev1.PersistentVolumeClaim)
			r.syncPVC(ctx, m.Spec.Dep.RocksMQ.Persistence.PersistentVolumeClaim, pvc)
		}).Return(nil)
		err := r.ReconcilePVCs(ctx, m)
		assert.NoError(t, err)
	})

	m.Spec.Dep.RocksMQ.Persistence.PersistentVolumeClaim.Annotations = map[string]string{"bla": "bla"}
	t.Run("sync:update", func(t *testing.T) {
		defer env.Ctrl.Finish()
		mockClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil)
		mockClient.EXPECT().Update(ctx, gomock.Any()).Return(nil)
		err := r.ReconcilePVCs(ctx, m)
		assert.NoError(t, err)
	})
}
