package controllers

import (
	"context"
	"reflect"
	"sync"

	"github.com/pkg/errors"
	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

//go:generate mockgen -source=./group_runner.go -destination=./group_runner_mock.go -package=controllers

type MilvusReconcileFunc func(context.Context, *v1beta1.Milvus) error

// GroupRunner does a group of funcs in parallel
type GroupRunner interface {
	// Run runs a group of funcs by same args, if any func fail, it should return err
	Run(funcs []Func, ctx context.Context, args ...interface{}) error
	// RunWithResult runs a group of funcs by same args, returns results with data & err for each func called
	RunWithResult(funcs []Func, ctx context.Context, args ...interface{}) []Result
	// RunDiffArgs runs a func by groups of args from @argsArray multiple times, if any failed, it should return err
	RunDiffArgs(f MilvusReconcileFunc, ctx context.Context, argsArray []*v1beta1.Milvus) error
}

// Func any callable func
type Func = interface{}

// Args array of args for a func
type Args = []interface{}

// Result contains data & err for a func's return
type Result struct {
	Data interface{}
	Err  error
}

var defaultGroupRunner GroupRunner = new(ParallelGroupRunner)

// ParallelGroupRunner is a group runner that run funcs in parallel
type ParallelGroupRunner struct {
}

// Run a group of funcs by same args in parallel, if any func fail, it should return err
func (ParallelGroupRunner) Run(funcs []Func, ctx context.Context, args ...interface{}) error {
	// shortcut
	length := len(funcs)
	if length == 0 {
		return nil
	}

	g, gtx := NewGroup(ctx)
	args = append([]interface{}{gtx}, args...)

	for i := 0; i < length; i++ {
		g.Go(WrappedFunc(funcs[i], args...))
	}
	err := g.Wait()
	return errors.Wrap(err, "run group failed")
}

// RunWithResult runs a group of funcs by same args, returns results with data & err for each func called
func (ParallelGroupRunner) RunWithResult(funcs []Func, ctx context.Context, args ...interface{}) []Result {
	// shortcut
	length := len(funcs)
	if length == 0 {
		return []Result{}
	}

	res := make([]Result, length)
	var wait sync.WaitGroup

	for i := 0; i < length; i++ {
		f := funcs[i]
		if reflect.TypeOf(f).Kind() != reflect.Func {
			err := errors.Errorf("fatal error wrap func want func, got[%s]", reflect.TypeOf(f).Kind())
			res[i] = Result{Err: err}
			continue
		}
		wait.Add(1)
		go func(index int, w *sync.WaitGroup) {
			defer w.Done()
			args := append([]interface{}{ctx}, args...)
			argValues := make([]reflect.Value, len(args))
			for j, arg := range args {
				argValues[j] = reflect.ValueOf(arg)
			}
			ret := reflect.ValueOf(f).Call(argValues)
			res[index] = Result{
				Data: ret[0].Interface(),
				Err:  valueAsError(ret[1]),
			}
		}(i, &wait)
	}
	wait.Wait()
	return res
}

// RunDiffArgs runs a func by groups of args from @argsArray multiple times, if any failed, it should return err
func (ParallelGroupRunner) RunDiffArgs(f MilvusReconcileFunc, ctx context.Context, argsArray []*v1beta1.Milvus) error {
	// shortcut
	if len(argsArray) == 0 {
		return nil
	}

	g, gtx := NewGroup(ctx)

	for i := 0; i < len(argsArray); i++ {
		idx := i
		g.Go(func() error {
			return f(gtx, argsArray[idx])
		})
	}

	err := g.Wait()
	return errors.Wrap(err, "run group")
}

func getDummyErr(err error) func() error {
	return func() error { return err }
}

var gLogger = logf.Log.WithName("group_runner")

func WrappedFunc(f interface{}, args ...interface{}) func() error {
	if reflect.TypeOf(f).Kind() != reflect.Func {
		err := errors.Errorf("fatal error wrap func want func, got[%s]", reflect.TypeOf(f).Kind())
		gLogger.Error(err, "wrap func")
		return getDummyErr(err)
	}

	argValues := make([]reflect.Value, len(args))
	for i, arg := range args {
		argValues[i] = reflect.ValueOf(arg)
	}

	return func() error {
		res := reflect.ValueOf(f).Call(argValues)[0]
		return valueAsError(res)
	}
}

func valueAsError(v reflect.Value) error {
	if v.IsNil() {
		return nil
	}
	return v.Interface().(error)
}
