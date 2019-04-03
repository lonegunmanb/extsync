// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package collection_test

import (
	. "github.com/lonegunmanb/extsync/collection"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"
	"github.com/tylerb/gls"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"testing/quick"
	"time"
)

type mapOp string

const (
	opLoad        = mapOp("Load")
	opStore       = mapOp("Store")
	opLoadOrStore = mapOp("LoadOrStore")
	opDelete      = mapOp("Delete")
)

var mapOps = [...]mapOp{opLoad, opStore, opLoadOrStore, opDelete}

// mapCall is a quick.Generator for calls on mapInterface.
type mapCall struct {
	op   mapOp
	k, v interface{}
}

func TestMapMatchesDeepCopy(t *testing.T) {
	if err := quick.CheckEqual(applyNewMap, applyDeepCopyMap, nil); err != nil {
		t.Error(err)
	}
}

func TestMapMatches(t *testing.T) {
	if err := quick.CheckEqual(applyNewMap, applyOriginMap, nil); err != nil {
		t.Error(err)
	}
}

func TestConcurrentRange(t *testing.T) {
	const mapSize = 1 << 10

	m := new(sync.Map)
	for n := int64(1); n <= mapSize; n++ {
		m.Store(n, int64(n))
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	defer func() {
		close(done)
		wg.Wait()
	}()
	for g := int64(runtime.GOMAXPROCS(0)); g > 0; g-- {
		r := rand.New(rand.NewSource(g))
		wg.Add(1)
		go func(g int64) {
			defer wg.Done()
			for i := int64(0); ; i++ {
				select {
				case <-done:
					return
				default:
				}
				for n := int64(1); n < mapSize; n++ {
					if r.Int63n(mapSize) == 0 {
						m.Store(n, n*i*g)
					} else {
						m.Load(n)
					}
				}
			}
		}(g)
	}

	iters := 1 << 10
	if testing.Short() {
		iters = 16
	}
	for n := iters; n > 0; n-- {
		seen := make(map[int64]bool, mapSize)

		m.Range(func(ki, vi interface{}) bool {
			k, v := ki.(int64), vi.(int64)
			if v%k != 0 {
				t.Fatalf("while Storing multiples of %v, Range saw value %v", k, v)
			}
			if seen[k] {
				t.Fatalf("Range visited Key %v twice", k)
			}
			seen[k] = true
			return true
		})

		if len(seen) != mapSize {
			t.Fatalf("Range visited %v elements of %v-element Map", len(seen), mapSize)
		}
	}
}

type GetOrSetTestGoroutine struct {
	getReadyWGLatch      chan struct{}
	factoryLatch         chan struct{}
	factorySignal        chan struct{}
	doneLatch            chan struct{}
	beforeWaitForWgLatch chan struct{}
	afterWaitForWgLatch  chan struct{}
}

func NewGetOrSetTestGoroutine() *GetOrSetTestGoroutine {
	return &GetOrSetTestGoroutine{
		getReadyWGLatch:      make(chan struct{}),
		factoryLatch:         make(chan struct{}),
		factorySignal:        make(chan struct{}, 1),
		doneLatch:            make(chan struct{}),
		beforeWaitForWgLatch: make(chan struct{}),
		afterWaitForWgLatch:  make(chan struct{}),
	}
}

func (g *GetOrSetTestGoroutine) WaitForGetReadyWG() {
	<-g.getReadyWGLatch
}

func (g *GetOrSetTestGoroutine) LeaveGetReadyWG() {
	g.getReadyWGLatch <- struct{}{}
}

func (g *GetOrSetTestGoroutine) WaitForBeforeWaitForWgLatch() {
	<-g.beforeWaitForWgLatch
}

func (g *GetOrSetTestGoroutine) LeaveBeforeWaitForWgLatch() {
	g.beforeWaitForWgLatch <- struct{}{}
}

func (g *GetOrSetTestGoroutine) WaitForAfterWaitForWgLatch() {
	<-g.afterWaitForWgLatch
}

func (g *GetOrSetTestGoroutine) LeaveAfterWaitForWgLatch() {
	g.afterWaitForWgLatch <- struct{}{}
}

func (g *GetOrSetTestGoroutine) WaitUntilEnterFactory() {
	<-g.factorySignal
}

func (g *GetOrSetTestGoroutine) EnterFactory() {
	g.factorySignal <- struct{}{}
}

func (g *GetOrSetTestGoroutine) WaitToLeaveFactory() {
	<-g.factoryLatch
}

func (g *GetOrSetTestGoroutine) LeaveFactory() {
	g.factoryLatch <- struct{}{}
}

func (g *GetOrSetTestGoroutine) WaitUntilEnd() {
	<-g.doneLatch
}

func (g *GetOrSetTestGoroutine) Done() {
	g.doneLatch <- struct{}{}
}

func (g *GetOrSetTestGoroutine) Go(action func()) {
	gls.With(gls.Values{ReadyWGLatchKey: g}, func() {
		gls.Go(action)
	})
}

const ReadyWGLatchKey = "ReadyWGLatch"
const ExpectedValue = "ExpectedValue"
const Key = "Key"

func testGetOrSet(t *testing.T, sequence func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine)) {
	originReadyWG := GetReadyWG
	originWaitForWg := WaitForWg
	stub := gostub.Stub(&GetReadyWG, func() *sync.WaitGroup {
		currentGoroutine := gls.Get(ReadyWGLatchKey).(*GetOrSetTestGoroutine)
		wg1 := originReadyWG()
		currentGoroutine.WaitForGetReadyWG()
		return wg1
	}).Stub(&WaitForWg, func(wg *sync.WaitGroup) {
		currentGoroutine := gls.Get(ReadyWGLatchKey).(*GetOrSetTestGoroutine)
		currentGoroutine.WaitForBeforeWaitForWgLatch()
		originWaitForWg(wg)
		currentGoroutine.WaitForAfterWaitForWgLatch()
	})
	defer stub.Reset()
	g1 := NewGetOrSetTestGoroutine()
	g2 := NewGetOrSetTestGoroutine()
	sut := new(Map)
	g1.Go(func() {
		actual, loaded := sut.GetOrSet(Key, func() interface{} {
			g1.EnterFactory()
			g1.WaitToLeaveFactory()
			return ExpectedValue
		})
		assert.False(t, loaded, "g1 should face empty map, loaded should be false")
		assert.Equal(t, ExpectedValue, actual)
		g1.Done()
	})
	g2.Go(func() {
		actual, loaded := sut.GetOrSet(Key, func() interface{} {
			panic("should not invoke factory twice")
		})
		assert.True(t, loaded, "g2 should read cached value")
		assert.Equal(t, ExpectedValue, actual)
		g2.Done()
	})
	// now two goroutine all block at GetReadyWG
	mustFinishIn(1*time.Hour, func() {
		sequence(g1, g2)
	})
}

func TestG1FinishThenG2(t *testing.T) {
	testGetOrSet(t, func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine) {
		g1.LeaveGetReadyWG()
		g1.WaitUntilEnterFactory()
		g1.LeaveFactory()
		g1.LeaveBeforeWaitForWgLatch()
		g1.LeaveAfterWaitForWgLatch()
		g1.WaitUntilEnd()
		g2.LeaveGetReadyWG()
		g2.LeaveBeforeWaitForWgLatch()
		g2.LeaveAfterWaitForWgLatch()
		g2.WaitUntilEnd()
	})
}

func TestG1BlockInFactoryG2BeginToReadG1FinishThenG2(t *testing.T) {
	t.Run("sequence1", func(t *testing.T) {
		testGetOrSet(t, func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine) {
			g1.LeaveGetReadyWG()
			g1.WaitUntilEnterFactory()
			g2.LeaveGetReadyWG()
			g1.LeaveFactory()
			g1.LeaveBeforeWaitForWgLatch()
			g1.LeaveAfterWaitForWgLatch()
			g2.LeaveBeforeWaitForWgLatch()
			g2.LeaveAfterWaitForWgLatch()
			g1.WaitUntilEnd()
			g2.WaitUntilEnd()
		})
	})
	t.Run("sequence2", func(t *testing.T) {
		testGetOrSet(t, func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine) {
			g1.LeaveGetReadyWG()
			g1.WaitUntilEnterFactory()
			g2.LeaveGetReadyWG()
			g1.LeaveFactory()
			g1.LeaveBeforeWaitForWgLatch()
			g1.LeaveAfterWaitForWgLatch()
			g2.LeaveBeforeWaitForWgLatch()
			g1.WaitUntilEnd()
			g2.LeaveAfterWaitForWgLatch()
			g2.WaitUntilEnd()
		})
	})
	t.Run("sequence3", func(t *testing.T) {
		testGetOrSet(t, func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine) {
			g1.LeaveGetReadyWG()
			g1.WaitUntilEnterFactory()
			g2.LeaveGetReadyWG()
			g1.LeaveFactory()
			g1.LeaveBeforeWaitForWgLatch()
			g1.LeaveAfterWaitForWgLatch()
			g1.WaitUntilEnd()
			g2.LeaveBeforeWaitForWgLatch()
			g2.LeaveAfterWaitForWgLatch()
			g2.WaitUntilEnd()
		})
	})
	t.Run("sequence4", func(t *testing.T) {
		testGetOrSet(t, func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine) {
			g1.LeaveGetReadyWG()
			g1.WaitUntilEnterFactory()
			g2.LeaveGetReadyWG()
			g1.LeaveFactory()
			g2.LeaveBeforeWaitForWgLatch()
			g2.LeaveAfterWaitForWgLatch()
			g1.LeaveBeforeWaitForWgLatch()
			g1.LeaveAfterWaitForWgLatch()
			g2.WaitUntilEnd()
			g1.WaitUntilEnd()
		})
	})
	t.Run("sequence5", func(t *testing.T) {
		testGetOrSet(t, func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine) {
			g1.LeaveGetReadyWG()
			g1.WaitUntilEnterFactory()
			g2.LeaveGetReadyWG()
			g1.LeaveFactory()
			g2.LeaveBeforeWaitForWgLatch()
			g2.LeaveAfterWaitForWgLatch()
			g1.LeaveBeforeWaitForWgLatch()
			g2.WaitUntilEnd()
			g1.LeaveAfterWaitForWgLatch()
			g1.WaitUntilEnd()
		})
	})
	t.Run("sequence6", func(t *testing.T) {
		testGetOrSet(t, func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine) {
			g1.LeaveGetReadyWG()
			g1.WaitUntilEnterFactory()
			g2.LeaveGetReadyWG()
			g1.LeaveFactory()
			g2.LeaveBeforeWaitForWgLatch()
			g2.LeaveAfterWaitForWgLatch()
			g2.WaitUntilEnd()
			g1.LeaveBeforeWaitForWgLatch()
			g1.LeaveAfterWaitForWgLatch()
			g1.WaitUntilEnd()
		})
	})
	t.Run("sequence7", func(t *testing.T) {
		testGetOrSet(t, func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine) {
			g1.LeaveGetReadyWG()
			g1.WaitUntilEnterFactory()
			g2.LeaveGetReadyWG()
			g1.LeaveFactory()
			g2.LeaveBeforeWaitForWgLatch()
			g2.LeaveAfterWaitForWgLatch()
			g1.LeaveBeforeWaitForWgLatch()
			g1.LeaveAfterWaitForWgLatch()
			g1.WaitUntilEnd()
			g2.WaitUntilEnd()
		})
	})
	t.Run("sequence8", func(t *testing.T) {
		testGetOrSet(t, func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine) {
			g1.LeaveGetReadyWG()
			g1.WaitUntilEnterFactory()
			g2.LeaveGetReadyWG()
			g1.LeaveFactory()
			g2.LeaveBeforeWaitForWgLatch()
			g2.LeaveAfterWaitForWgLatch()
			g1.LeaveBeforeWaitForWgLatch()
			g1.LeaveAfterWaitForWgLatch()
			g2.WaitUntilEnd()
			g1.WaitUntilEnd()
		})
	})
	t.Run("sequence9", func(t *testing.T) {
		testGetOrSet(t, func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine) {
			g1.LeaveGetReadyWG()
			g1.WaitUntilEnterFactory()
			g2.LeaveGetReadyWG()
			g1.LeaveFactory()
			g2.LeaveBeforeWaitForWgLatch()
			g2.LeaveAfterWaitForWgLatch()
			g1.LeaveBeforeWaitForWgLatch()
			g2.WaitUntilEnd()
			g1.LeaveAfterWaitForWgLatch()
			g1.WaitUntilEnd()
		})
	})
	t.Run("sequence10", func(t *testing.T) {
		testGetOrSet(t, func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine) {
			g1.LeaveGetReadyWG()
			g1.WaitUntilEnterFactory()
			g2.LeaveGetReadyWG()
			g1.LeaveFactory()
			g2.LeaveBeforeWaitForWgLatch()
			g2.LeaveAfterWaitForWgLatch()
			g2.WaitUntilEnd()
			g1.LeaveBeforeWaitForWgLatch()
			g1.LeaveAfterWaitForWgLatch()
			g1.WaitUntilEnd()
		})
	})
	t.Run("sequence11", func(t *testing.T) {
		testGetOrSet(t, func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine) {
			g1.LeaveGetReadyWG()
			g1.WaitUntilEnterFactory()
			g2.LeaveGetReadyWG()
			g2.LeaveBeforeWaitForWgLatch()
			g1.LeaveFactory()
			g2.LeaveAfterWaitForWgLatch()
			g2.WaitUntilEnd()
			g1.LeaveBeforeWaitForWgLatch()
			g1.LeaveAfterWaitForWgLatch()
			g1.WaitUntilEnd()
		})
	})
	t.Run("sequence12", func(t *testing.T) {
		testGetOrSet(t, func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine) {
			g1.LeaveGetReadyWG()
			g1.WaitUntilEnterFactory()
			g2.LeaveGetReadyWG()
			g2.LeaveBeforeWaitForWgLatch()
			g1.LeaveFactory()
			g2.LeaveAfterWaitForWgLatch()
			g1.LeaveBeforeWaitForWgLatch()
			g1.LeaveAfterWaitForWgLatch()
			g1.WaitUntilEnd()
			g2.WaitUntilEnd()
		})
	})
	t.Run("sequence13", func(t *testing.T) {
		testGetOrSet(t, func(g1 *GetOrSetTestGoroutine, g2 *GetOrSetTestGoroutine) {
			g1.LeaveGetReadyWG()
			g1.WaitUntilEnterFactory()
			g2.LeaveGetReadyWG()
			g2.LeaveBeforeWaitForWgLatch()
			g1.LeaveFactory()
			g1.LeaveBeforeWaitForWgLatch()
			g1.LeaveAfterWaitForWgLatch()
			g1.WaitUntilEnd()
			g2.LeaveAfterWaitForWgLatch()
			g2.WaitUntilEnd()
		})
	})
}

func mustFinishIn(d time.Duration, action func()) {
	done := make(chan struct{})
	timeout := time.NewTimer(d)
	go func() {
		action()
		done <- struct{}{}
	}()
	select {
	case <-done:
		{

		}
	case <-timeout.C:
		{
			panic("timeout")
		}
	}
}
