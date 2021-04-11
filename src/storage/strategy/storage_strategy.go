package strategy

type StorageStrategy interface {
	GetValue(key string) (uint64, error)
	SetValue(key string, value uint64, expirationSeconds uint64) error
	IncrementValue(key string, delta uint64) error
}
