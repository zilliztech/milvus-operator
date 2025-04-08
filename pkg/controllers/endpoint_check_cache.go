package controllers

import (
	"log"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/patrickmn/go-cache"
)

// endpointCheckCache singleton
var endpointCheckCache EndpointCheckCache = NewEndpointCheckCacheImpl()

// EndpointCheckCache coordinates endpoint check to avoid duplicated check for same endpoint
type EndpointCheckCache interface {
	TryStartProbeFor(endpoint []string) bool
	EndProbeFor(endpoint []string)
	Get(endpoint []string) (condition *v1beta1.MilvusCondition, found bool)
	Set(endpoints []string, condition *v1beta1.MilvusCondition)
}

// EndpointCheckCacheImpl implements EndpointCheckCache
type EndpointCheckCacheImpl struct {
	cache *cache.Cache
}

func NewEndpointCheckCacheImpl() EndpointCheckCache {
	return EndpointCheckCacheImpl{cache: cache.New(-1, time.Hour)}
}

func strSliceAsKey(input []string) string {
	// sort slice & combine with comma
	sortable := sort.StringSlice(input)
	sort.Sort(sortable)
	return strings.Join(sortable, ",")
}

// TryStartProbeFor use an atomic int32 to lock the endpoint
func (e EndpointCheckCacheImpl) TryStartProbeFor(endpoints []string) bool {
	probeLockKey := strSliceAsKey(endpoints) + "_probe_lock"
	lockPtrRaw, found := e.cache.Get(probeLockKey)
	if !found {
		e.cache.Set(probeLockKey, new(int32), -1)
		lockPtrRaw, found = e.cache.Get(probeLockKey)
		if !found {
			// should not happen
			log.Println("ERROR Failed to get probe lock")
			return false
		}
	}
	lockPtr := lockPtrRaw.(*int32)
	return atomic.CompareAndSwapInt32(lockPtr, 0, 1)
}

func (e EndpointCheckCacheImpl) EndProbeFor(endpoints []string) {
	if len(endpoints) == 0 {
		return
	}
	probeLockKey := strSliceAsKey(endpoints) + "_probe_lock"
	lockPtrRaw, found := e.cache.Get(probeLockKey)
	if !found {
		// should not happen
		log.Println("ERROR Failed to get probe lock")
		return
	}
	lockPtr := lockPtrRaw.(*int32)
	atomic.StoreInt32(lockPtr, 0)
}

func (e EndpointCheckCacheImpl) Get(endpoints []string) (condition *v1beta1.MilvusCondition, isUpToDate bool) {
	if len(endpoints) == 0 {
		return nil, false
	}
	item, found := e.cache.Get(strSliceAsKey(endpoints))
	if !found {
		return nil, false
	}
	return item.(*v1beta1.MilvusCondition), true
}

func (e EndpointCheckCacheImpl) Set(endpoints []string, condition *v1beta1.MilvusCondition) {
	if len(endpoints) == 0 {
		return
	}
	e.cache.Set(strSliceAsKey(endpoints), condition, 0)
}
