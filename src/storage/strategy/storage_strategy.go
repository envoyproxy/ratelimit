package strategy

// Interface to abstract underlying storage like memcached and redis
// Implement bussiness level where we don't care how underlying storage doing it.\
type StorageStrategy interface {
	GetValue(key string) (uint64, error)
	SetValue(key string, value uint64, expirationSeconds uint64) error
	IncrementValue(key string, delta uint64) error
	SetExpire(key string, expirationSeconds uint64) error
}
