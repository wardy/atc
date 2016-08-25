// This file was generated by counterfeiter
package lockrunnerfakes

import (
	"sync"

	"code.cloudfoundry.org/lager"
	"github.com/concourse/atc/db"
	"github.com/concourse/atc/lockrunner"
)

type FakeRunnerDB struct {
	GetLockStub        func(logger lager.Logger, lockName string) (db.Lock, bool, error)
	getLockMutex       sync.RWMutex
	getLockArgsForCall []struct {
		logger   lager.Logger
		lockName string
	}
	getLockReturns struct {
		result1 db.Lock
		result2 bool
		result3 error
	}
	invocations      map[string][][]interface{}
	invocationsMutex sync.RWMutex
}

func (fake *FakeRunnerDB) GetLock(logger lager.Logger, lockName string) (db.Lock, bool, error) {
	fake.getLockMutex.Lock()
	fake.getLockArgsForCall = append(fake.getLockArgsForCall, struct {
		logger   lager.Logger
		lockName string
	}{logger, lockName})
	fake.recordInvocation("GetLock", []interface{}{logger, lockName})
	fake.getLockMutex.Unlock()
	if fake.GetLockStub != nil {
		return fake.GetLockStub(logger, lockName)
	} else {
		return fake.getLockReturns.result1, fake.getLockReturns.result2, fake.getLockReturns.result3
	}
}

func (fake *FakeRunnerDB) GetLockCallCount() int {
	fake.getLockMutex.RLock()
	defer fake.getLockMutex.RUnlock()
	return len(fake.getLockArgsForCall)
}

func (fake *FakeRunnerDB) GetLockArgsForCall(i int) (lager.Logger, string) {
	fake.getLockMutex.RLock()
	defer fake.getLockMutex.RUnlock()
	return fake.getLockArgsForCall[i].logger, fake.getLockArgsForCall[i].lockName
}

func (fake *FakeRunnerDB) GetLockReturns(result1 db.Lock, result2 bool, result3 error) {
	fake.GetLockStub = nil
	fake.getLockReturns = struct {
		result1 db.Lock
		result2 bool
		result3 error
	}{result1, result2, result3}
}

func (fake *FakeRunnerDB) Invocations() map[string][][]interface{} {
	fake.invocationsMutex.RLock()
	defer fake.invocationsMutex.RUnlock()
	fake.getLockMutex.RLock()
	defer fake.getLockMutex.RUnlock()
	return fake.invocations
}

func (fake *FakeRunnerDB) recordInvocation(key string, args []interface{}) {
	fake.invocationsMutex.Lock()
	defer fake.invocationsMutex.Unlock()
	if fake.invocations == nil {
		fake.invocations = map[string][][]interface{}{}
	}
	if fake.invocations[key] == nil {
		fake.invocations[key] = [][]interface{}{}
	}
	fake.invocations[key] = append(fake.invocations[key], args)
}

var _ lockrunner.RunnerDB = new(FakeRunnerDB)