package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/config"
	"github.com/zilliztech/milvus-operator/pkg/util"
	"github.com/stretchr/testify/assert"
)

func TestParallelGroupConciler_Run_Milvus(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())
	vals := [3]int{}
	func1 := func(context.Context, v1beta1.Milvus) error {
		vals[0] = 1
		return nil
	}
	func2 := func(context.Context, v1beta1.Milvus) error {
		vals[1] = 2
		return nil
	}
	func3 := func(context.Context, v1beta1.Milvus) error {
		vals[2] = 3
		return errors.New("test")
	}
	funcs := []Func{
		func1, func2, func3,
	}
	for i := 0; i < 3; i++ {
		err := defaultGroupRunner.Run(funcs, context.Background(), v1beta1.Milvus{})
		assert.Error(t, err)
		assert.Equal(t, [3]int{1, 2, 3}, vals)
	}
}

func TestParallelGroupConciler_RunDiff(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())
	func1 := func(ctx context.Context, milvus *v1beta1.Milvus) error {
		milvus.Spec.Com.Proxy = &v1beta1.MilvusProxy{}
		milvus.Spec.Com.Proxy.Replicas = int32Ptr(int(milvus.Generation))
		return nil
	}
	argsArray := []*v1beta1.Milvus{
		{},
		{},
		{},
	}
	for i := 1; i < len(argsArray)+1; i++ {
		argsArray[i-1].Generation = int64(i)
	}

	err := defaultGroupRunner.RunDiffArgs(func1, context.Background(), argsArray)
	assert.NoError(t, err)
	var total int
	for j := 0; j < len(argsArray); j++ {
		total += int(*argsArray[j].Spec.Com.Proxy.Replicas)
	}
	assert.Equal(t, 1+2+3, total)

	// short cut ok
	err = defaultGroupRunner.RunDiffArgs(func1, context.Background(), []*v1beta1.Milvus{})
	assert.NoError(t, err)
}

func TestParallelGroupConciler_RunWithResult(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())
	vals := [3]int{}
	// shortcut ok
	res := defaultGroupRunner.RunWithResult([]Func{}, context.TODO(), v1beta1.Milvus{})
	assert.Len(t, res, 0)
	assert.Equal(t, [3]int{}, vals)

	// real run ok
	func1 := func(context.Context, v1beta1.Milvus) (int, error) {
		vals[0] = 1
		return 0, nil
	}
	func2 := func(context.Context, v1beta1.Milvus) (int, error) {
		vals[1] = 2
		return 1, nil
	}
	func3 := func(context.Context, v1beta1.Milvus) (int, error) {
		vals[2] = 3
		return 2, errors.New("test")
	}
	funcs := []Func{
		func1, func2, func3,
	}
	res = defaultGroupRunner.RunWithResult(funcs, context.TODO(), v1beta1.Milvus{})
	assert.Equal(t, []Result{
		{0, nil},
		{1, nil},
		{2, errors.New("test")},
	}, res)

	// bad func
	res = defaultGroupRunner.RunWithResult([]Func{1}, context.Background(), v1beta1.Milvus{})
	assert.Error(t, res[0].Err)
}
