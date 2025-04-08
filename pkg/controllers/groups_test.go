package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/zilliztech/milvus-operator/pkg/config"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

func TestGroup_Go_NoErr(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g, gtx := NewGroup(ctx)
	g.Go(func() error { return nil })
	g.Go(func() error { return nil })
	g.Go(func() error { return nil })

	err := g.Wait()
	assert.NoError(t, err)

	// ctx cancel
	wrapJobFunc := func(ctx context.Context) func() error {
		return func() error {
			select {
			case <-ctx.Done():
			case <-time.After(time.Hour):
			}
			return nil
		}
	}
	g.Go(wrapJobFunc(gtx))
	g.Go(wrapJobFunc(gtx))
	g.Go(wrapJobFunc(gtx))
	cancel()
	err = g.Wait()
	assert.NoError(t, err)
}

func TestGroup_Go_Err(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g, _ := NewGroup(ctx)
	g.Go(func() error { return errors.New("mock") })
	g.Go(func() error { return nil })
	g.Go(func() error { return nil })

	err := g.Wait()
	assert.Error(t, err)
}

func TestGroup_Go_Panic(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())
	ctx := context.Background()
	g, _ := NewGroup(ctx)
	g.Go(func() error { panic("mock") })
	g.Go(func() error { return nil })
	g.Go(func() error { return nil })

	err := g.Wait()
	assert.Error(t, err)
}
