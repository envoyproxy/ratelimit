package redis

// Errors that may be raised during config parsing.
type RedisError string

func (e RedisError) Error() string {
	return string(e)
}

// Interface for a redis client.
type Client interface {
	// DoCmd is used to perform a redis command and retrieve a result.
	//
	// @param rcv supplies receiver for the result.
	// @param cmd supplies the command to append.
	// @param key supplies the key to append.
	// @param args supplies the additional arguments.
	DoCmd(rcv interface{}, cmd, key string, args ...interface{}) error

	// Once Close() is called all future method calls on the Client will return
	// an error
	Close() error

	// NumActiveConns return number of active connections, used in testing.
	NumActiveConns() int
}
