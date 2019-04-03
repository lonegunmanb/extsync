package collection

import "sync"

type Map struct {
	sync.Map
}

type valueFunc = func() interface{}

func (m *Map) GetOrSet(key interface{}, factory func() interface{}) (actual interface{}, loaded bool) {
	var value interface{}
	wg := GetReadyWG()
	vFunc, loaded := m.Map.LoadOrStore(key, func() interface{} {
		WaitForWg(wg)
		return value
	})
	if !loaded {
		value = factory()
		wg.Done()
	}
	return vFunc.(valueFunc)(), loaded
}

var WaitForWg = func(wg *sync.WaitGroup) {
	wg.Wait()
}

var GetReadyWG = func() *sync.WaitGroup {
	wg := &sync.WaitGroup{}
	wg.Add(1)
	return wg
}

func (m *Map) Load(key interface{}) (value interface{}, ok bool) {
	vFunc, ok := m.Map.Load(key)
	if ok {
		return vFunc.(valueFunc)(), true
	}
	return nil, false
}

func (m *Map) Store(key, value interface{}) {
	vFunc := func() interface{} {
		return value
	}
	m.Map.Store(key, vFunc)
}

func (m *Map) LoadOrStore(key, value interface{}) (actual interface{}, loaded bool) {
	return m.GetOrSet(key, func() interface{} {
		return value
	})
}

func (m *Map) Delete(key interface{}) {
	m.Map.Delete(key)
}

func (m *Map) Range(f func(key, value interface{}) bool) {
	vf := func(key, value interface{}) bool {
		vFunc := value.(valueFunc)
		return f(key, vFunc())
	}
	m.Map.Range(vf)
}
