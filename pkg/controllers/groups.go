package controllers

import (
	"context"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/config"
	"github.com/pkg/errors"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var groupLog = logf.Log.WithName("group")

type Group struct {
	wait   sync.WaitGroup
	cancel context.CancelFunc

	// Collect all the errors
	errors []error
	locker *sync.Mutex
}

// NewGroup creates a new group.
func NewGroup(ctx context.Context) (*Group, context.Context) {
	gtx, cancel := context.WithCancel(ctx)

	return &Group{
		cancel: cancel,
		errors: make([]error, 0),
		locker: &sync.Mutex{},
	}, gtx
}

// Go to run a func.
func (g *Group) Go(f func() error) {
	g.wait.Add(1)

	go func() {
		defer g.wait.Done()
		if !config.IsDebug() {
			defer func() {
				if r := recover(); r != nil {
					stack := string(debug.Stack())
					err := errors.Errorf("%s", r)
					groupLog.Error(err, "panic captured", "stack", stack)

					g.locker.Lock()
					g.errors = append(g.errors, err)
					g.locker.Unlock()
				}
			}()
		}

		// Run and handle error
		if err := f(); err != nil {
			g.locker.Lock()
			defer g.locker.Unlock()

			g.errors = append(g.errors, err)
		}
	}()
}

// Wait until all the go functions are returned
// If errors occurred, they'll be combined with ":" and returned.
func (g *Group) Wait() error {
	g.wait.Wait()

	defer func() {
		if g.cancel != nil {
			// Send signals to the downstream components
			g.cancel()
		}
	}()

	errTexts := make([]string, 0)
	for _, e := range g.errors {
		if errors.Is(e, ErrRequeue) {
			return e
		}
		errTexts = append(errTexts, e.Error())
	}

	if len(errTexts) > 0 {
		return errors.Errorf("groups error: %s", strings.Join(errTexts, ";"))
	}

	return nil
}

func WarppedReconcileComponentFunc(
	f func(context.Context, v1beta1.Milvus, MilvusComponent) error,
	ctx context.Context, mc v1beta1.Milvus, c MilvusComponent) func() error {
	return func() error {
		return f(ctx, mc, c)
	}
}
