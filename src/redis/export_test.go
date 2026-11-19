package redis

import "testing"

// FakeRedisForTest exposes fakeRedis to the external redis_test package.
type FakeRedisForTest struct {
	Addr         string
	SetBlackhole func(bool)
	// Close closes the server and every connection to it.
	Close func()
}

func NewFakeRedisForTest(t *testing.T) FakeRedisForTest {
	f := newFakeRedis(t, nil)
	return FakeRedisForTest{Addr: f.addr(), SetBlackhole: f.setBlackhole, Close: f.close}
}
